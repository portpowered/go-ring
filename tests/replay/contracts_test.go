package replay_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/internal/protocol"
	"gopkg.in/yaml.v3"
)

type recordedHTTPContractExchange struct {
	Request  recordedHTTPContractRequest  `json:"request"`
	Response recordedHTTPContractResponse `json:"response"`
}

type recordedHTTPContractRequest struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Origin string `json:"origin"`
}

type recordedHTTPContractResponse struct {
	Status int `json:"status"`
}

type recordedSignalingContract struct {
	Messages []recordedSignalingContractMessage `json:"messages"`
}

type recordedSignalingContractMessage struct {
	Direction string         `json:"direction"`
	Payload   map[string]any `json:"payload"`
}

func loadYAML(t *testing.T, path string) map[string]any {
	t.Helper()

	documentBytes, readErr := os.ReadFile(path) // #nosec G304 -- the test supplies a repository-owned OpenAPI path.
	if readErr != nil {
		t.Fatal(readErr)
	}

	var document map[string]any

	yamlErr := yaml.Unmarshal(documentBytes, &document)
	if yamlErr != nil {
		t.Fatal(yamlErr)
	}

	return document
}
func object(value any) map[string]any {
	result, _ := value.(map[string]any)

	return result
}
func TestRecordedHTTPMethodsAreInOpenAPI(t *testing.T) {
	t.Parallel()

	doc := loadYAML(t, filepath.Join("..", "..", "api", "openapi.yaml"))
	paths := object(doc["paths"])

	files, globErr := filepath.Glob(filepath.Join("fixtures", "http", "historical", "*.json"))
	if globErr != nil {
		t.Fatal(globErr)
	}

	variants, globErr := filepath.Glob(filepath.Join("fixtures", "http", "historical", "variants", "*.json"))
	if globErr != nil {
		t.Fatal(globErr)
	}

	files = append(files, variants...)
	if len(files) == 0 {
		t.Fatal("HTTP recordings missing")
	}

	for _, fixturePath := range files {
		t.Run(filepath.Base(fixturePath), func(t *testing.T) {
			t.Parallel()
			checkRecordedHTTPFixture(t, fixturePath, paths, doc)
		})
	}
}

func checkRecordedHTTPFixture(t *testing.T, fixturePath string, paths, doc map[string]any) {
	t.Helper()

	// #nosec G304 -- fixturePath comes from the fixed captured-fixture glob above.
	fixtureBytes, readErr := os.ReadFile(fixturePath)
	if readErr != nil {
		t.Fatal(readErr)
	}

	var exchange recordedHTTPContractExchange

	readErr = json.Unmarshal(fixtureBytes, &exchange)
	if readErr != nil {
		t.Fatal(readErr)
	}

	operations := templatePath(exchange.Request.Path, paths)
	if operations == nil {
		t.Fatalf("recorded path %s is absent from OpenAPI", exchange.Request.Path)
	}

	if _, ok := operations[strings.ToLower(exchange.Request.Method)]; !ok {
		t.Fatalf("recorded %s %s lacks operation", exchange.Request.Method, exchange.Request.Path)
	}

	operation := object(operations[strings.ToLower(exchange.Request.Method)])
	if _, ok := object(operation["responses"])[strconv.Itoa(exchange.Response.Status)]; !ok {
		t.Fatalf("recorded status %d absent", exchange.Response.Status)
	}

	servers, ok := operation["servers"].([]any)
	if !ok {
		servers, _ = doc["servers"].([]any)
	}

	for _, server := range servers {
		if object(server)["url"] == exchange.Request.Origin {
			return
		}
	}

	t.Fatal("recorded origin differs from specification")
}
func templatePath(actualPath string, paths map[string]any) map[string]any {
	if exact, ok := paths[actualPath]; ok {
		return object(exact)
	}

	for template, operations := range paths {
		if samePath(template, actualPath) {
			return object(operations)
		}
	}

	return nil
}
func samePath(template, actual string) bool {
	templateParts := strings.Split(strings.Trim(template, "/"), "/")
	actualParts := strings.Split(strings.Trim(actual, "/"), "/")

	if len(templateParts) != len(actualParts) {
		return false
	}

	for i := range templateParts {
		if strings.HasPrefix(templateParts[i], "{") && strings.HasSuffix(templateParts[i], "}") {
			continue
		}

		if templateParts[i] != actualParts[i] {
			return false
		}
	}

	return true
}

func TestSessionRecordingsUseAsyncAPIEnvelopeAndPTZMethods(t *testing.T) {
	t.Parallel()

	doc := loadYAML(t, filepath.Join("..", "..", "api", "asyncapi.yaml"))
	schemas := object(object(doc["components"])["schemas"])
	client := enumSet(t, object(schemas["ClientEnvelope"]))
	server := enumSet(t, object(schemas["ServerEnvelope"]))
	ptz := enumSet(t, object(schemas["PTZRPC"]))

	files, globErr := filepath.Glob(filepath.Join("fixtures", "signaling", "historical", "*.json"))
	if globErr != nil {
		t.Fatal(globErr)
	}

	if len(files) == 0 {
		t.Fatal("session recordings missing")
	}

	found := map[string]bool{}

	for _, fixturePath := range files {
		checkRecordedSignalingFixture(t, fixturePath, client, server, ptz, found)
	}

	for _, method := range []string{
		"PTZ.Pan.Step",
		"PTZ.Pan.Continuous",
		"PTZ.Tilt.Step",
		"PTZ.Tilt.Continuous",
		"PTZ.Pan.Halted",
	} {
		if !ptz[method] {
			t.Errorf("recorded PTZ method %q absent", method)
		}
	}

	if !found["ping"] || !found["pong"] {
		t.Fatal("captured application ping/pong coverage missing")
	}
}

func checkRecordedSignalingFixture(
	t *testing.T, fixturePath string, client, server, ptz, found map[string]bool,
) {
	t.Helper()

	// #nosec G304 -- fixturePath comes from the fixed signaling-fixture glob above.
	fixtureBytes, readErr := os.ReadFile(fixturePath)
	if readErr != nil {
		t.Fatal(readErr)
	}

	var recording recordedSignalingContract

	readErr = json.Unmarshal(fixtureBytes, &recording)
	if readErr != nil {
		t.Fatal(readErr)
	}

	for _, message := range recording.Messages {
		method, _ := message.Payload["method"].(string)
		if method == "" {
			t.Fatalf("%s has message without method", fixturePath)
		}

		found[method] = true

		set := client
		if message.Direction == "server_to_client" {
			set = server
		} else if message.Direction != "client_to_server" {
			t.Fatalf("unknown direction %q", message.Direction)
		}

		if !set[method] {
			t.Errorf("%s message %q missing from AsyncAPI direction schema", filepath.Base(fixturePath), method)
		}

		if method == "rpc" {
			body := object(message.Payload["body"])
			command := object(body["command"])

			rpcMethod, _ := command["method"].(string)
			if strings.HasPrefix(rpcMethod, "PTZ.") && !ptz[rpcMethod] {
				t.Errorf("captured RPC method %q missing from PTZ schema", rpcMethod)
			}
		}
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

	for _, rawValue := range values {
		methodName, ok := rawValue.(string)
		if !ok {
			t.Fatalf("non-string method enum %v", rawValue)
		}

		out[methodName] = true
	}

	return out
}

func TestRuntimeSignalingRegistryMatchesAsyncAPI(t *testing.T) {
	t.Parallel()

	doc := loadYAML(t, filepath.Join("..", "..", "api", "asyncapi.yaml"))
	schemas := object(object(doc["components"])["schemas"])

	client, server := enumSet(t, object(schemas["ClientEnvelope"])), enumSet(t, object(schemas["ServerEnvelope"]))
	for _, method := range []string{
		protocol.MethodLiveView,
		protocol.MethodPlayback,
		protocol.MethodSDP,
		protocol.MethodICE,
		protocol.MethodSessionCreated,
		protocol.MethodActivateSession,
		protocol.MethodCameraStarted,
		protocol.MethodCameraOptions,
		protocol.MethodMicEnable,
		protocol.MethodStreamOptions,
		protocol.MethodClose,
		protocol.MethodPing,
		protocol.MethodPong,
		protocol.MethodRPC,
	} {
		if !client[method] && !server[method] {
			t.Errorf("runtime wire method %s missing from schema", method)
		}
	}

	ptz := enumSet(t, object(schemas["PTZRPC"]))
	for _, method := range []string{
		protocol.RPCPanStep,
		protocol.RPCTiltStep,
		protocol.RPCPanContinuous,
		protocol.RPCTiltContinuous,
	} {
		if !ptz[method] {
			t.Errorf("runtime PTZ method %s missing from schema", method)
		}
	}
}
