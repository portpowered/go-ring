package replay_test

import "testing"

func replayObjectField(t *testing.T, fields map[string]any, name string) map[string]any {
	t.Helper()

	return replayObjectValue(t, fields[name], name)
}

func replayObjectValue(t *testing.T, value any, name string) map[string]any {
	t.Helper()

	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("replay value %q has type %T, want object", name, value)
	}

	return object
}

func replayStringField(t *testing.T, fields map[string]any, name string) string {
	t.Helper()

	return replayStringValue(t, fields[name], name)
}

func replayStringValue(t *testing.T, value any, name string) string {
	t.Helper()

	text, ok := value.(string)
	if !ok {
		t.Fatalf("replay value %q has type %T, want string", name, value)
	}

	return text
}

func replayArrayField(t *testing.T, fields map[string]any, name string) []any {
	t.Helper()

	value, ok := fields[name].([]any)
	if !ok {
		t.Fatalf("replay field %q has type %T, want array", name, fields[name])
	}

	return value
}

func replayFloatField(t *testing.T, fields map[string]any, name string) float64 {
	t.Helper()

	value, ok := fields[name].(float64)
	if !ok {
		t.Fatalf("replay field %q has type %T, want number", name, fields[name])
	}

	return value
}
