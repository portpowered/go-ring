package replay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const maxSDPTemplateBytes = 128 * 1024

// Templates only bind named UUIDs on an expected client frame. A later frame
// must refer to that exact value, so the replay never accepts arbitrary IDs.
func matchFrameTemplate(want, got []byte, bindings map[string]string) error {
	left, err := decodeTemplateJSON(want)
	if err != nil {
		return err
	}

	right, err := decodeTemplateJSON(got)
	if err != nil {
		return err
	}

	return matchTemplateValue(left, right, bindings)
}

func decodeTemplateJSON(body []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()

	var value any

	decodeErr := dec.Decode(&value)
	if decodeErr != nil {
		return nil, webSocketReplayError{message: "decode WebSocket template JSON", cause: decodeErr}
	}

	var extra any

	trailingErr := dec.Decode(&extra)
	if trailingErr != io.EOF {
		return nil, webSocketReplayError{message: "extra JSON value", cause: trailingErr}
	}

	return value, nil
}

func matchTemplateValue(want, got any, bindings map[string]string) error {
	switch value := want.(type) {
	case string:
		return matchTemplateString(value, got, bindings)
	case map[string]any:
		actual, ok := got.(map[string]any)
		if !ok || len(value) != len(actual) {
			return webSocketReplayError{message: "JSON object shape differs"}
		}

		for key, expected := range value {
			field, exists := actual[key]
			if !exists {
				return webSocketReplayError{message: fmt.Sprintf("missing JSON key %q", key)}
			}

			err := matchTemplateValue(expected, field, bindings)
			if err != nil {
				return webSocketReplayError{message: key, cause: err}
			}
		}

		return nil
	case []any:
		actual, ok := got.([]any)
		if !ok || len(value) != len(actual) {
			return webSocketReplayError{message: "JSON array shape differs"}
		}

		for i, expected := range value {
			err := matchTemplateValue(expected, actual[i], bindings)
			if err != nil {
				return webSocketReplayError{message: fmt.Sprintf("[%d]", i), cause: err}
			}
		}

		return nil
	}

	if !reflect.DeepEqual(want, got) {
		return webSocketReplayError{message: fmt.Sprintf("JSON value differs: want %v, got %v", want, got)}
	}

	return nil
}

func matchTemplateString(value string, got any, bindings map[string]string) error {
	if name, ok := strings.CutPrefix(value, "$sdp:"); ok {
		return matchSDPBinding(name, got, bindings)
	}

	if specification, ok := strings.CutPrefix(value, "$rpcid:"); ok {
		return matchRPCIDBinding(specification, got, bindings)
	}

	if value == "$epochMillis" {
		actual, ok := got.(json.Number)
		if !ok {
			return webSocketReplayError{message: "expected epoch millisecond number"}
		}

		milliseconds, err := actual.Int64()
		if err != nil || abs64(time.Now().UnixMilli()-milliseconds) > int64(time.Minute/time.Millisecond) {
			return webSocketReplayError{message: "epoch millisecond value outside one-minute window", cause: err}
		}

		return nil
	}

	actual, ok := got.(string)
	if !ok {
		return webSocketReplayError{message: fmt.Sprintf("expected string %q, got %T", value, got)}
	}

	if name, ok := strings.CutPrefix(value, "$uuid:"); ok {
		if name == "" {
			return webSocketReplayError{message: "empty UUID binding name"}
		}

		{
			_, err := uuid.Parse(actual)
			if err != nil {
				return webSocketReplayError{message: fmt.Sprintf("binding %s is not a UUID", name), cause: err}
			}
		}

		if bound, exists := bindings[name]; exists && bound != actual {
			return webSocketReplayError{message: fmt.Sprintf("binding %s changed", name)}
		}

		bindings[name] = actual

		return nil
	}

	if name, ok := strings.CutPrefix(value, "$ref:"); ok {
		bound, exists := bindings[name]
		if !exists || actual != bound {
			return webSocketReplayError{message: fmt.Sprintf("binding %s mismatch or missing", name)}
		}

		return nil
	}

	if value != actual {
		return webSocketReplayError{message: fmt.Sprintf("JSON value differs: want %q, got %q", value, actual)}
	}

	return nil
}

func matchSDPBinding(name string, got any, bindings map[string]string) error {
	if name == "" {
		return webSocketReplayError{message: "empty SDP binding name"}
	}

	actual, isString := got.(string)
	if !isString {
		return webSocketReplayError{message: fmt.Sprintf("expected bounded SDP string, got %T", got)}
	}

	err := validateDecodedSDP(actual)
	if err != nil {
		return webSocketReplayError{message: "invalid bounded SDP", cause: err}
	}

	if bound, exists := bindings[name]; exists && bound != actual {
		return webSocketReplayError{message: fmt.Sprintf("SDP binding %s changed", name)}
	}

	bindings[name] = actual

	return nil
}

func matchRPCIDBinding(specification string, got any, bindings map[string]string) error {
	parts := strings.Split(specification, ":")
	if len(parts) != 3 || parts[0] == "" || parts[2] == "" {
		return webSocketReplayError{message: "RPC ID binding must name a prefix, sequence, and binding"}
	}

	sequence, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil || sequence == 0 || strconv.FormatUint(sequence, 10) != parts[1] {
		return webSocketReplayError{message: "RPC ID sequence must be a positive canonical integer", cause: err}
	}

	actual, isString := got.(string)
	if !isString || actual != parts[0]+"-"+parts[1] {
		return webSocketReplayError{
			message: fmt.Sprintf("RPC ID differs: want %q-%s, got %v", parts[0], parts[1], got),
		}
	}

	if bound, exists := bindings[parts[2]]; exists && bound != actual {
		return webSocketReplayError{message: fmt.Sprintf("RPC ID binding %s changed", parts[2])}
	}

	bindings[parts[2]] = actual

	return nil
}

func validateDecodedSDP(value string) error {
	if len(value) == 0 || len(value) > maxSDPTemplateBytes ||
		!utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		return webSocketReplayError{message: "SDP is empty, too large, or contains invalid text"}
	}

	err := validateSDPLineFraming(value)
	if err != nil {
		return err
	}

	requirements := inspectSDPRequirements(value)
	if !requirements.complete() {
		return webSocketReplayError{
			message: "SDP is missing required WebRTC session, video, ICE, fingerprint, or DTLS fields",
		}
	}

	return nil
}

func validateSDPLineFraming(value string) error {
	if !strings.HasPrefix(value, "v=0\r\n") || !strings.HasSuffix(value, "\r\n") ||
		strings.ContainsAny(strings.ReplaceAll(value, "\r\n", ""), "\r\n") {
		return webSocketReplayError{message: "SDP must use bounded CRLF-framed session lines"}
	}

	return nil
}

type sdpRequirements struct {
	origin      bool
	session     bool
	timing      bool
	video       bool
	iceUfrag    bool
	icePassword bool
	fingerprint bool
	setup       bool
}

func inspectSDPRequirements(value string) sdpRequirements {
	var requirements sdpRequirements

	for _, line := range strings.Split(strings.TrimSuffix(value, "\r\n"), "\r\n") {
		switch {
		case strings.HasPrefix(line, "o="):
			requirements.origin = true
		case strings.HasPrefix(line, "s="):
			requirements.session = true
		case strings.HasPrefix(line, "t="):
			requirements.timing = true
		case strings.HasPrefix(line, "m=video "):
			requirements.video = true
		case strings.HasPrefix(line, "a=ice-ufrag:") && len(strings.TrimPrefix(line, "a=ice-ufrag:")) > 0:
			requirements.iceUfrag = true
		case strings.HasPrefix(line, "a=ice-pwd:") && len(strings.TrimPrefix(line, "a=ice-pwd:")) > 0:
			requirements.icePassword = true
		case strings.HasPrefix(line, "a=fingerprint:") && len(strings.TrimPrefix(line, "a=fingerprint:")) > 0:
			requirements.fingerprint = true
		case strings.HasPrefix(line, "a=setup:") && len(strings.TrimPrefix(line, "a=setup:")) > 0:
			requirements.setup = true
		}
	}

	return requirements
}

func (requirements sdpRequirements) complete() bool {
	return requirements.origin && requirements.session && requirements.timing && requirements.video &&
		requirements.iceUfrag && requirements.icePassword && requirements.fingerprint && requirements.setup
}

func abs64(value int64) int64 {
	if value < 0 {
		return -value
	}

	return value
}

func renderFrameTemplate(body []byte, bindings map[string]string) ([]byte, error) {
	value, err := decodeTemplateJSON(body)
	if err != nil {
		return nil, err
	}

	value, err = renderTemplateValue(value, bindings)
	if err != nil {
		return nil, err
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, webSocketReplayError{message: "marshal templated WebSocket frame", cause: err}
	}

	return encoded, nil
}

func renderTemplateValue(value any, bindings map[string]string) (any, error) {
	switch currentValue := value.(type) {
	case string:
		if name, ok := strings.CutPrefix(currentValue, "$ref:"); ok {
			bound, exists := bindings[name]
			if !exists {
				return nil, webSocketReplayError{message: "missing binding " + name}
			}

			return bound, nil
		}

		if strings.HasPrefix(currentValue, "$uuid:") {
			return nil, webSocketReplayError{message: "server frame cannot bind UUID"}
		}
	case map[string]any:
		for key, field := range currentValue {
			var err error

			currentValue[key], err = renderTemplateValue(field, bindings)
			if err != nil {
				return nil, webSocketReplayError{message: key, cause: err}
			}
		}
	case []any:
		for i, field := range currentValue {
			var err error

			currentValue[i], err = renderTemplateValue(field, bindings)
			if err != nil {
				return nil, webSocketReplayError{message: fmt.Sprintf("[%d]", i), cause: err}
			}
		}
	}

	return value, nil
}
