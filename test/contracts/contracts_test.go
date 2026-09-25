package contracts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func loadYAML(t *testing.T, path string) map[string]any {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var v map[string]any
	if e = yaml.Unmarshal(b, &v); e != nil {
		t.Fatal(e)
	}
	return v
}
func object(v any) map[string]any { m, _ := v.(map[string]any); return m }
func TestRecordedHTTPMethodsAreInOpenAPI(t *testing.T) {
	doc := loadYAML(t, filepath.Join("..", "..", "api", "openapi.yaml"))
	paths := object(doc["paths"])
	files, e := filepath.Glob(filepath.Join("..", "recordings", "http", "*.json"))
	if e != nil {
		t.Fatal(e)
	}
	if len(files) == 0 {
		t.Fatal("HTTP recordings missing")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			b, e := os.ReadFile(f)
			if e != nil {
				t.Fatal(e)
			}
			var x struct {
				Request struct{ Method, Path string } `json:"request"`
			}
			if e = json.Unmarshal(b, &x); e != nil {
				t.Fatal(e)
			}
			p := templatePath(x.Request.Path, paths)
			if p == nil {
				t.Fatalf("recorded path %s is absent from OpenAPI", x.Request.Path)
			}
			if _, ok := p[strings.ToLower(x.Request.Method)]; !ok {
				t.Fatalf("recorded %s %s lacks operation", x.Request.Method, x.Request.Path)
			}
		})
	}
}
func templatePath(actual string, paths map[string]any) map[string]any {
	for k, v := range paths {
		if samePath(k, actual) {
			return object(v)
		}
	}
	return nil
}
func samePath(template, actual string) bool {
	a, b := strings.Split(strings.Trim(template, "/"), "/"), strings.Split(strings.Trim(actual, "/"), "/")
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.HasPrefix(a[i], "{") && strings.HasSuffix(a[i], "}") {
			continue
		}
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSessionRecordingsUseAsyncAPIEnvelopeAndPTZMethods(t *testing.T) {
	doc := loadYAML(t, filepath.Join("..", "..", "api", "asyncapi.yaml"))
	schemas := object(object(doc["components"])["schemas"])
	client := enumSet(t, object(schemas["ClientEnvelope"]))
	server := enumSet(t, object(schemas["ServerEnvelope"]))
	ptz := enumSet(t, object(schemas["PTZRPC"]))
	files, e := filepath.Glob(filepath.Join("..", "recordings", "sessions", "*.json"))
	if e != nil {
		t.Fatal(e)
	}
	if len(files) == 0 {
		t.Fatal("session recordings missing")
	}
	found := map[string]bool{}
	for _, f := range files {
		b, e := os.ReadFile(f)
		if e != nil {
			t.Fatal(e)
		}
		var x struct {
			Messages []struct {
				Direction string         `json:"direction"`
				Payload   map[string]any `json:"payload"`
			} `json:"messages"`
		}
		if e = json.Unmarshal(b, &x); e != nil {
			t.Fatal(e)
		}
		for _, msg := range x.Messages {
			m, _ := msg.Payload["method"].(string)
			if m == "" {
				t.Fatalf("%s has message without method", f)
			}
			found[m] = true
			set := client
			if msg.Direction == "server_to_client" {
				set = server
			} else if msg.Direction != "client_to_server" {
				t.Fatalf("unknown direction %q", msg.Direction)
			}
			if !set[m] {
				t.Errorf("%s message %q missing from AsyncAPI direction schema", filepath.Base(f), m)
			}
			if m == "rpc" {
				body := object(msg.Payload["body"])
				command := object(body["command"])
				method, _ := command["method"].(string)
				if strings.HasPrefix(method, "PTZ.") && !ptz[method] {
					t.Errorf("captured RPC method %q missing from PTZ schema", method)
				}
			}
		}
	}
	for _, m := range []string{"PTZ.Pan.Step", "PTZ.Pan.Continuous", "PTZ.Tilt.Step", "PTZ.Tilt.Continuous", "PTZ.Pan.Halted"} {
		if !ptz[m] {
			t.Errorf("recorded PTZ method %q absent", m)
		}
	}
	if !found["ping"] || !found["pong"] {
		t.Fatal("captured application ping/pong coverage missing")
	}
}

func enumSet(t *testing.T, schema map[string]any) map[string]bool {
	t.Helper()
	props := object(schema["properties"])
	method := object(props["method"])
	values, ok := method["enum"].([]any)
	if !ok {
		t.Fatal("schema method enum missing")
	}
	out := map[string]bool{}
	for _, v := range values {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("non-string method enum %v", v)
		}
		out[s] = true
	}
	return out
}
