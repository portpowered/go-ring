package main

import (
	"bytes"
	"strconv"
	"unicode/utf8"
)

const maxTNetstringDepth = 256

type tnetNull struct{}

// decodeFlowStream reads the concatenated typed-netstring records written by
// mitmproxy's FlowWriter.
func decodeFlowStream(data []byte) ([]*capturedFlow, error) {
	flows := make([]*capturedFlow, 0)
	for offset := 0; offset < len(data); {
		state, next, err := decodeTNetstring(data, offset, 0)
		if err != nil {
			return nil, err
		}
		flowState, ok := asObject(state)
		if !ok {
			return nil, captureErrorf("invalid mitmproxy flow: expected object")
		}
		version, ok := integerValue(flowState["version"])
		if !ok || version != 21 {
			return nil, captureErrorf("unsupported mitmproxy flow format version %v (expected 21)", flowState["version"])
		}
		flow, err := flowFromState(flowState)
		if err != nil {
			return nil, err
		}
		flows = append(flows, flow)
		offset = next
	}
	return flows, nil
}

func decodeTNetstring(data []byte, offset, depth int) (any, int, error) {
	if depth > maxTNetstringDepth {
		return nil, offset, captureErrorf("mitmproxy flow nesting exceeds %d levels", maxTNetstringDepth)
	}
	if offset >= len(data) {
		return nil, offset, captureErrorf("unexpected end of mitmproxy flow")
	}
	colon := bytes.IndexByte(data[offset:], ':')
	if colon < 0 {
		return nil, offset, captureErrorf("invalid typed netstring: missing length separator")
	}
	colon += offset
	lengthText := data[offset:colon]
	if len(lengthText) == 0 || len(lengthText) > 12 {
		return nil, offset, captureErrorf("invalid typed netstring length")
	}
	for _, char := range lengthText {
		if char < '0' || char > '9' {
			return nil, offset, captureErrorf("invalid typed netstring length")
		}
	}
	length, err := strconv.Atoi(string(lengthText))
	if err != nil {
		return nil, offset, wrapCaptureError("invalid typed netstring length", err)
	}
	start := colon + 1
	end := start + length
	if length < 0 || end >= len(data) {
		return nil, offset, captureErrorf("invalid typed netstring length %d", length)
	}
	tag := data[end]
	next := end + 1

	value, err := parseTNetstringValue(data, start, end, tag, depth)
	if err != nil {
		return nil, offset, err
	}
	if _, isNull := value.(tnetNull); isNull {
		value = nil
	}
	return value, next, nil
}

func parseTNetstringValue(data []byte, start, end int, tag byte, depth int) (any, error) {
	payload := data[start:end]
	switch tag {
	case ',':
		return bytes.Clone(payload), nil
	case ';':
		if !utf8.Valid(payload) {
			return nil, captureErrorf("typed netstring contains invalid UTF-8 text")
		}
		return string(payload), nil
	case '#':
		value, err := strconv.ParseInt(string(payload), 10, 64)
		if err != nil {
			return nil, wrapCaptureError("invalid typed netstring integer", err)
		}
		return value, nil
	case '^':
		value, err := strconv.ParseFloat(string(payload), 64)
		if err != nil {
			return nil, wrapCaptureError("invalid typed netstring float", err)
		}
		return value, nil
	case '!':
		switch string(payload) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		default:
			return nil, captureErrorf("invalid typed netstring boolean")
		}
	case '~':
		if len(payload) != 0 {
			return nil, captureErrorf("invalid typed netstring null")
		}
		return tnetNull{}, nil
	case ']':
		return parseTNetstringList(data, start, end, depth)
	case '}':
		return parseTNetstringMap(data, start, end, depth)
	default:
		return nil, captureErrorf("unknown typed netstring tag %q", tag)
	}
}

func parseTNetstringList(data []byte, start, end, depth int) ([]any, error) {
	values := make([]any, 0)
	for childOffset := start; childOffset < end; {
		value, after, err := decodeTNetstring(data[:end], childOffset, depth+1)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
		childOffset = after
	}
	return values, nil
}

func parseTNetstringMap(data []byte, start, end, depth int) (map[string]any, error) {
	values := make(map[string]any)
	for childOffset := start; childOffset < end; {
		key, afterKey, err := decodeTNetstring(data[:end], childOffset, depth+1)
		if err != nil {
			return nil, err
		}
		value, afterValue, err := decodeTNetstring(data[:end], afterKey, depth+1)
		if err != nil {
			return nil, err
		}
		keyText, ok := keyString(key)
		if !ok {
			return nil, captureErrorf("invalid typed netstring dictionary key")
		}
		values[keyText] = value
		childOffset = afterValue
	}
	return values, nil
}

func keyString(value any) (string, bool) {
	switch value := value.(type) {
	case string:
		return value, true
	case []byte:
		return string(value), true
	default:
		return "", false
	}
}

func asObject(value any) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	return object, ok
}

func integerValue(value any) (int64, bool) {
	integer, ok := value.(int64)
	return integer, ok
}
