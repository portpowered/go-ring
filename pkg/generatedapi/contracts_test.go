package generatedapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGeneratedRoutesMatchOpenAPI(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			OperationID string `yaml:"operationId"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	seen := map[HTTPOperation]bool{}
	for path, methods := range spec.Paths {
		for method, operation := range methods {
			if operation.OperationID == "" {
				continue
			}
			id := HTTPOperation(operation.OperationID)
			route, ok := HTTPRoutes[id]
			if !ok {
				t.Errorf("missing generated operation %s", id)
				continue
			}
			if route.Path != path || route.Method != strings.ToUpper(method) {
				t.Errorf("route %s does not match schema", id)
			}
			seen[id] = true
		}
	}
	if len(seen) != len(HTTPRoutes) {
		t.Fatalf("generated routes=%d, schema operations=%d", len(HTTPRoutes), len(seen))
	}
}
func TestBuildPathEscapesParameters(t *testing.T) {
	path, err := BuildPath(OperationGetDevice, map[string]string{"device_id": "a/b"})
	if err != nil || path != "/device_info/v3/devices/a%2Fb" {
		t.Fatalf("path=%q err=%v", path, err)
	}
	if _, err := BuildPath(OperationGetDevice, nil); err == nil {
		t.Fatal("missing path parameter accepted")
	}
}
