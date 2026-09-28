package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var capturedFixtureRoot = filepath.Join(repositoryRoot(), "tests", "replay", "fixtures")

func TestCapturedFixturesContainNoObviousPersonalNetworkValues(t *testing.T) {
	files := capturedJSONFiles(t)
	if len(files) < 30 {
		t.Fatalf("found %d captured fixtures, want at least 30", len(files))
	}
	ipv4 := regexp.MustCompile(`(^|[^\d.])((?:\d{1,3}\.){3}\d{1,3})(?:$|[^\d.])`)
	email := regexp.MustCompile(`(?i)[\w.+-]+@[\w.-]+\.[A-Za-z]{2,}`)
	uuid := regexp.MustCompile(`(?i)\b[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}\b`)
	for _, path := range files {
		// #nosec G304 -- file paths originate from the checked-in fixture tree.
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range ipv4.FindAllStringSubmatch(string(content), -1) {
			address := match[2]
			if address != "0.0.0.0" && address != "192.0.2.1" {
				t.Errorf("%s contains an unredacted IPv4 address", filepath.Base(path))
			}
		}
		for _, pattern := range []*regexp.Regexp{email, uuid} {
			if pattern.Match(content) {
				t.Errorf("%s contains an unredacted email or UUID", filepath.Base(path))
			}
		}
	}
}

func TestCapturedFixturesMatchTheirSchemaContracts(t *testing.T) {
	httpSchema := compileFixtureSchema(t, "http-exchange.schema.json")
	sessionSchema := compileFixtureSchema(t, "session.schema.json")
	compileFixtureSchema(t, "shape-regression.schema.json")
	for _, path := range filesUnder(t, filepath.Join(capturedFixtureRoot, "http", "historical"), true) {
		if filepath.Ext(path) != ".json" {
			continue
		}
		if err := validateFixtureFile(path, httpSchema); err != nil {
			t.Errorf("%s: %v", filepath.Base(path), err)
		}
	}
	for _, path := range filesUnder(t, filepath.Join(capturedFixtureRoot, "signaling", "historical"), false) {
		if filepath.Ext(path) != ".json" {
			continue
		}
		if err := validateFixtureFile(path, sessionSchema); err != nil {
			t.Errorf("%s: %v", filepath.Base(path), err)
		}
	}
}

func TestShapeRegressionSchemaPinsNullArrayAndIdentityTypes(t *testing.T) {
	schema := compileFixtureSchema(t, "shape-regression.schema.json")
	var good map[string]any
	if err := json.Unmarshal([]byte(`{"nullable_value":null,"items":[],"device_id":1000,"session_id":"session-1","dialog_id":"dialog-1","command_id":"command-1"}`), &good); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(good); err != nil {
		t.Fatalf("valid shape was rejected: %v", err)
	}
	for _, test := range []struct {
		key   string
		value any
	}{
		{"nullable_value", []any{}},
		{"items", nil},
		{"device_id", "1000"},
		{"session_id", float64(1000)},
		{"dialog_id", float64(1000)},
		{"command_id", float64(1000)},
	} {
		bad := make(map[string]any, len(good))
		for key, value := range good {
			bad[key] = value
		}
		bad[test.key] = test.value
		if err := schema.Validate(bad); err == nil {
			t.Errorf("schema accepted invalid %s type %T", test.key, test.value)
		}
	}
}

func TestCapturedVariantsCoverDistinctOperations(t *testing.T) {
	variants := filesUnder(t, filepath.Join(capturedFixtureRoot, "http", "historical", "variants"), false)
	if len(variants) < 10 {
		t.Fatalf("found %d variant fixtures, want at least 10", len(variants))
	}
	names := make(map[string]bool)
	for _, path := range variants {
		names[filepath.Base(path)] = true
	}
	for _, prefix := range []string{"device-settings-patch-", "device-detail-", "device-timeline-"} {
		found := false
		for name := range names {
			found = found || strings.HasPrefix(name, prefix)
		}
		if !found {
			t.Errorf("variant set has no file beginning with %q", prefix)
		}
	}
}

func TestCapturedDeviceRPCCapabilitiesKeepProtocolNames(t *testing.T) {
	fixture := decodeFixtureFile(t, filepath.Join(capturedFixtureRoot, "http", "historical", "device-list.json"))
	response := testObject(t, testValue(t, fixture, "response"))
	var commandArrays [][]any
	collectRPCCommands(testValue(t, response, "body"), &commandArrays)
	if len(commandArrays) == 0 {
		t.Fatal("device fixture contains no supported RPC command arrays")
	}
	foundPTZ := false
	for _, commands := range commandArrays {
		for _, command := range commands {
			name, ok := command.(string)
			if !ok {
				t.Fatalf("RPC command is not a string: %T", command)
			}
			foundPTZ = foundPTZ || strings.HasPrefix(name, "PTZ.")
		}
	}
	if !foundPTZ {
		t.Fatal("device fixture contains no PTZ protocol method name")
	}
}

func TestCapturedPathsKeepVersionSegmentsAndTemplateIdentifiers(t *testing.T) {
	settings := decodeFixtureFile(t, filepath.Join(capturedFixtureRoot, "http", "historical", "device-settings-patch.json"))
	request := testObject(t, testValue(t, settings, "request"))
	if got := testValue(t, request, "path"); got != "/devices/v1/devices/{device_id}/settings" {
		t.Fatalf("versioned device settings path changed: %v", got)
	}
	for _, path := range filesUnder(t, filepath.Join(capturedFixtureRoot, "http", "historical"), true) {
		if filepath.Ext(path) != ".json" {
			continue
		}
		fixture := decodeFixtureFile(t, path)
		request := testObject(t, testValue(t, fixture, "request"))
		requestPath := testValue(t, request, "path").(string)
		if regexp.MustCompile(`/v[1-4]/\{device_id\}`).MatchString(requestPath) {
			t.Errorf("%s lost its versioned path segment", filepath.Base(path))
		}
	}
}

func TestCapturedSessionsKeepFullConversationShapes(t *testing.T) {
	for _, test := range []struct{ flowNumber, want int }{{21, 254}, {402, 243}} {
		path := filepath.Join(capturedFixtureRoot, "signaling", "historical", fmt.Sprintf("flow-%d.json", test.flowNumber))
		fixture := decodeFixtureFile(t, path)
		messages := testValue(t, fixture, "messages").([]any)
		if len(messages) != test.want {
			t.Errorf("flow %d has %d messages, want %d", test.flowNumber, len(messages), test.want)
		}
		for _, rawMessage := range messages {
			message := testObject(t, rawMessage)
			for _, key := range []string{"direction", "frame", "payload"} {
				if _, exists := message.get(key); !exists {
					t.Errorf("flow %d message is missing %s", test.flowNumber, key)
				}
			}
		}
	}
}

func compileFixtureSchema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	path := filepath.Join(capturedFixtureRoot, "schemas", name)
	// #nosec G304 -- schema name is selected from this test's fixed list.
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var schema any
	if err := json.Unmarshal(contents, &schema); err != nil {
		t.Fatalf("parse schema %s: %v", name, err)
	}
	resourceURL := "https://go-ring.invalid/capture/" + name
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	if err := compiler.AddResource(resourceURL, schema); err != nil {
		t.Fatalf("add schema %s: %v", name, err)
	}
	compiled, err := compiler.Compile(resourceURL)
	if err != nil {
		t.Fatalf("compile schema %s: %v", name, err)
	}
	return compiled
}

func validateFixtureFile(path string, schema *jsonschema.Schema) error {
	// #nosec G304 -- fixture paths are resolved beneath the checked-in fixture tree.
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var fixture any
	if err := json.Unmarshal(contents, &fixture); err != nil {
		return err
	}
	return schema.Validate(fixture)
}

func capturedJSONFiles(t *testing.T) []string {
	t.Helper()
	files := append(filesUnder(t, filepath.Join(capturedFixtureRoot, "http", "historical"), true), filesUnder(t, filepath.Join(capturedFixtureRoot, "signaling", "historical"), false)...)
	filtered := files[:0]
	for _, path := range files {
		if filepath.Ext(path) == ".json" {
			filtered = append(filtered, path)
		}
	}
	return filtered
}

func filesUnder(t *testing.T, root string, recursive bool) []string {
	t.Helper()
	files := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != root && entry.IsDir() && !recursive {
			return filepath.SkipDir
		}
		if !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func collectRPCCommands(value any, result *[][]any) {
	if value == nil {
		return
	}
	if commands, exists := objectValue(value, "supported_rpc_commands"); exists {
		if array, ok := commands.([]any); ok {
			*result = append(*result, array)
		}
	}
	for _, key := range objectKeys(value) {
		child, _ := objectValue(value, key)
		collectRPCCommands(child, result)
	}
	if array, ok := value.([]any); ok {
		for _, child := range array {
			collectRPCCommands(child, result)
		}
	}
}
