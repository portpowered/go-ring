package main

import (
	"sort"
	"testing"
)

func decodeTestJSON(t *testing.T, raw string) *orderedObject {
	t.Helper()
	value, err := decodeJSONOrdered([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return testObject(t, value)
}

func testObject(t *testing.T, value any) *orderedObject {
	t.Helper()
	switch value := value.(type) {
	case *orderedObject:
		return value
	case map[string]any:
		object := newOrderedObject()
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			object.set(key, value[key])
		}
		return object
	default:
		t.Fatalf("expected object, got %T", value)
		return nil
	}
}

func testValue(t *testing.T, object any, key string) any {
	t.Helper()
	value, exists := objectValue(object, key)
	if !exists {
		t.Fatalf("object is missing key %q", key)
	}
	return value
}
