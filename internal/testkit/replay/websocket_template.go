package replay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
)

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
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, webSocketReplayError{message: "extra JSON value", cause: err}
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
			if err := matchTemplateValue(expected, field, bindings); err != nil {
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
			if err := matchTemplateValue(expected, actual[i], bindings); err != nil {
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
		if _, err := uuid.Parse(actual); err != nil {
			return webSocketReplayError{message: fmt.Sprintf("binding %s is not a UUID", name), cause: err}
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
	return json.Marshal(value)
}

func renderTemplateValue(value any, bindings map[string]string) (any, error) {
	switch v := value.(type) {
	case string:
		if name, ok := strings.CutPrefix(v, "$ref:"); ok {
			bound, exists := bindings[name]
			if !exists {
				return nil, webSocketReplayError{message: fmt.Sprintf("missing binding %s", name)}
			}
			return bound, nil
		}
		if strings.HasPrefix(v, "$uuid:") {
			return nil, webSocketReplayError{message: "server frame cannot bind UUID"}
		}
	case map[string]any:
		for key, field := range v {
			var err error
			v[key], err = renderTemplateValue(field, bindings)
			if err != nil {
				return nil, webSocketReplayError{message: key, cause: err}
			}
		}
	case []any:
		for i, field := range v {
			var err error
			v[i], err = renderTemplateValue(field, bindings)
			if err != nil {
				return nil, webSocketReplayError{message: fmt.Sprintf("[%d]", i), cause: err}
			}
		}
	}
	return value, nil
}
