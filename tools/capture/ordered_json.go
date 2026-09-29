package main

import (
	"bytes"
	"encoding/json"
	"io"
)

// orderedObject keeps JSON member order so identifiers assigned during
// sanitization stay stable and fixture bodies retain their captured shape.
type orderedObject struct {
	keys   []string
	values map[string]any
}

func newOrderedObject() *orderedObject {
	return &orderedObject{values: make(map[string]any), keys: nil}
}

func (object *orderedObject) MarshalJSON() ([]byte, error) {
	var buffer bytes.Buffer

	buffer.WriteByte('{')

	for index, key := range object.keys {
		if index > 0 {
			buffer.WriteByte(',')
		}

		encodedKey, err := marshalNoHTML(key)
		if err != nil {
			return nil, wrapCaptureError("marshal ordered JSON member name", err)
		}

		buffer.Write(encodedKey)
		buffer.WriteByte(':')

		encodedValue, err := marshalNoHTML(object.values[key])
		if err != nil {
			return nil, wrapCaptureError("marshal ordered JSON member value", err)
		}

		buffer.Write(encodedValue)
	}

	buffer.WriteByte('}')

	return buffer.Bytes(), nil
}

func (object *orderedObject) set(key string, value any) {
	if _, exists := object.values[key]; !exists {
		object.keys = append(object.keys, key)
	}

	object.values[key] = value
}

func (object *orderedObject) get(key string) (any, bool) {
	value, exists := object.values[key]

	return value, exists
}

func marshalNoHTML(value any) ([]byte, error) {
	var buffer bytes.Buffer

	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)

	err := encoder.Encode(value)
	if err != nil {
		return nil, wrapCaptureError("encode ordered JSON value", err)
	}

	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

func decodeJSONOrdered(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	value, err := decodeJSONValue(decoder)
	if err != nil {
		return nil, wrapCaptureError("decode ordered JSON value", err)
	}

	{
		_, err := decoder.Token()
		if err != io.EOF {
			if err == nil {
				return nil, captureErrorf("unexpected data after JSON value")
			}

			return nil, wrapCaptureError("check for trailing JSON data", err)
		}
	}

	return value, nil
}

func decodeJSONValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, wrapCaptureError("read JSON token", err)
	}

	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return token, nil
	}

	switch delimiter {
	case '{':
		object := newOrderedObject()

		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, wrapCaptureError("read JSON object member name", err)
			}

			key, ok := keyToken.(string)
			if !ok {
				return nil, captureErrorf("JSON object member name is not a string")
			}

			value, err := decodeJSONValue(decoder)
			if err != nil {
				return nil, err
			}

			object.set(key, value)
		}

		{
			_, err := decoder.Token()
			if err != nil {
				return nil, wrapCaptureError("read JSON object terminator", err)
			}
		}

		return object, nil
	case '[':
		values := make([]any, 0)

		for decoder.More() {
			value, err := decodeJSONValue(decoder)
			if err != nil {
				return nil, err
			}

			values = append(values, value)
		}

		{
			_, err := decoder.Token()
			if err != nil {
				return nil, wrapCaptureError("read JSON array terminator", err)
			}
		}

		return values, nil
	default:
		return nil, captureErrorf("unexpected JSON delimiter %q", delimiter)
	}
}

func objectValue(value any, key string) (any, bool) {
	switch value := value.(type) {
	case *orderedObject:
		return value.get(key)
	case map[string]any:
		child, exists := value[key]

		return child, exists
	default:
		return nil, false
	}
}

func objectKeys(value any) []string {
	switch value := value.(type) {
	case *orderedObject:
		return append([]string(nil), value.keys...)
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}

		return keys
	default:
		return nil
	}
}
