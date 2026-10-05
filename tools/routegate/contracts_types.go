package routegate

import "strings"

type HTTPRoute struct {
	OperationID string
	Method      string
	Path        string
	HasBody     bool
}

func stringMap(value any) map[string]any {
	if values, ok := value.(map[string]any); ok {
		return values
	}

	return nil
}

func anySlice(value any) []any {
	if values, ok := value.([]any); ok {
		return values
	}

	return nil
}

func routeKey(method, path string) string {
	return strings.ToUpper(method) + " " + path
}
