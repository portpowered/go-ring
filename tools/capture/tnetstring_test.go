package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"testing"
)

func TestDecodeTNetstringSupportsMitmproxyValueTypes(t *testing.T) {
	t.Parallel()

	want := map[string]any{
		"bytes":   []byte{0, 1, 255},
		"text":    cameraConnectedNotification,
		"number":  int64(42),
		"float":   1.25,
		"enabled": true,
		"empty":   nil,
		"array":   []any{int64(1), "two"},
	}
	encoded := encodeTestTNetstring(t, want)

	got, next, err := decodeTNetstring(encoded, 0, 0)
	if err != nil {
		t.Fatal(err)
	}

	if next != len(encoded) {
		t.Fatalf("decoder stopped at %d of %d bytes", next, len(encoded))
	}

	object := testObject(t, got)
	if !reflect.DeepEqual(testValue(t, object, "bytes"), want["bytes"]) {
		t.Fatalf("byte string changed: %#v", testValue(t, object, "bytes"))
	}

	if testValue(t, object, "text") != cameraConnectedNotification ||
		testValue(t, object, "number") != int64(42) ||
		testValue(t, object, "enabled") != true {
		t.Fatalf("typed values changed: %#v", object.values)
	}

	gotFloat := testValueAs[float64](t, object, "float").value
	if math.Abs(gotFloat-1.25) > 0.00001 {
		t.Fatalf("float changed: %v", gotFloat)
	}

	if testValue(t, object, "empty") != nil {
		t.Fatalf("null changed: %#v", testValue(t, object, "empty"))
	}
}

func TestDecodeFlowStreamReadsMultipleRecordsAndRejectsOtherVersions(t *testing.T) {
	t.Parallel()

	first := encodeTestTNetstring(t, map[string]any{"version": int64(21), "type": "http"})
	second := encodeTestTNetstring(t, map[string]any{"version": int64(21), "type": "http"})

	flows, err := decodeFlowStream(append(first, second...))
	if err != nil {
		t.Fatal(err)
	}

	if len(flows) != 2 {
		t.Fatalf("got %d records, want 2", len(flows))
	}

	unsupported := encodeTestTNetstring(t, map[string]any{"version": int64(20)})
	{
		_, err := decodeFlowStream(unsupported)
		if err == nil {
			t.Fatal("expected unsupported flow version error")
		}
	}
}

func TestDecodeTNetstringRejectsMalformedRecords(t *testing.T) {
	t.Parallel()

	for _, malformed := range [][]byte{[]byte("not a flow"), []byte("4:abc,"), []byte("5:true!")} {
		{
			_, _, err := decodeTNetstring(malformed, 0, 0)
			if err == nil {
				t.Fatalf("expected %q to fail", malformed)
			}
		}
	}
}

func TestDecodeJSONBodyReadsGzipJSON(t *testing.T) {
	t.Parallel()

	var buffer bytes.Buffer

	writer := gzip.NewWriter(&buffer)
	{
		_, err := writer.Write([]byte(`{"ok":true}`))
		if err != nil {
			t.Fatal(err)
		}
	}

	{
		err := writer.Close()
		if err != nil {
			t.Fatal(err)
		}
	}

	body, isJSON, err := decodeJSONBody(buffer.Bytes(), []headerField{{name: "Content-Encoding", value: "gzip"}})
	if err != nil {
		t.Fatal(err)
	}

	if !isJSON || testValue(t, body, "ok") != true {
		t.Fatalf("gzip JSON was not decoded: json=%v body=%#v", isJSON, body)
	}
}

func encodeTestTNetstring(t *testing.T, value any) []byte {
	t.Helper()

	var (
		payload []byte
		tag     byte
	)

	switch value := value.(type) {
	case []byte:
		payload, tag = value, ','
	case string:
		payload, tag = []byte(value), ';'
	case int:
		payload, tag = []byte(strconv.Itoa(value)), '#'
	case int64:
		payload, tag = []byte(strconv.FormatInt(value, 10)), '#'
	case float64:
		payload, tag = []byte(fmt.Sprint(value)), '^'
	case bool:
		payload, tag = []byte(strconv.FormatBool(value)), '!'
	case nil:
		payload, tag = nil, '~'
	case []any:
		for _, child := range value {
			payload = append(payload, encodeTestTNetstring(t, child)...)
		}

		tag = ']'
	case map[string]any:
		for key, child := range value {
			payload = append(payload, encodeTestTNetstring(t, key)...)
			payload = append(payload, encodeTestTNetstring(t, child)...)
		}

		tag = '}'
	default:
		t.Fatalf("unsupported test typed-netstring value %T", value)
	}

	result := []byte(fmt.Sprintf("%d:", len(payload)))
	result = append(result, payload...)

	return append(result, tag)
}
