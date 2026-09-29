package websocket

import (
	"bytes"
	"encoding/json"
	"strconv"
)

func normalizeCloseReasonCode(encoded []byte) ([]byte, error) {
	var frame map[string]json.RawMessage

	err := json.Unmarshal(encoded, &frame)
	if err != nil {
		return nil, wrapSignalingWireError(err, "decode generated close frame")
	}

	body, err := rawObjectField(frame, "body")
	if err != nil {
		return nil, err
	}

	reason, exists := body["reason"]
	if !exists {
		return encoded, nil
	}

	var reasonFields map[string]json.RawMessage

	err = json.Unmarshal(reason, &reasonFields)
	if err != nil || reasonFields == nil {
		return nil, signalingWireError("close reason must be an object")
	}

	code, exists := reasonFields["code"]
	if !exists {
		return encoded, nil
	}

	if !validCloseReasonCode(code) {
		return nil, signalingWireError("close reason code must be an integer or string")
	}

	delete(reasonFields, "code")

	reason, err = json.Marshal(reasonFields)
	if err != nil {
		return nil, wrapSignalingWireError(err, "normalize generated close reason")
	}

	body["reason"] = reason

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, wrapSignalingWireError(err, "normalize generated close body")
	}

	frame["body"] = bodyBytes

	normalized, err := json.Marshal(frame)
	if err != nil {
		return nil, wrapSignalingWireError(err, "normalize generated close frame")
	}

	return normalized, nil
}

func rawObjectField(object map[string]json.RawMessage, name string) (map[string]json.RawMessage, error) {
	encoded, exists := object[name]
	if !exists {
		return nil, signalingWireError("signaling frame is missing %q", name)
	}

	var fields map[string]json.RawMessage

	err := json.Unmarshal(encoded, &fields)
	if err != nil || fields == nil {
		return nil, signalingWireError("signaling frame field %q must be an object", name)
	}

	return fields, nil
}

func validCloseReasonCode(encoded json.RawMessage) bool {
	var text string
	if json.Unmarshal(encoded, &text) == nil {
		return true
	}

	var number json.Number

	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()

	err := decoder.Decode(&number)
	if err != nil || number.String() == "" {
		return false
	}

	integer, err := strconv.ParseInt(number.String(), 10, 64)

	return err == nil && strconv.FormatInt(integer, 10) == number.String()
}
