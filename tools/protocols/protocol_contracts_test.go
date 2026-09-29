package protocols_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

func TestRecordedHTTPBodiesMatchOpenAPISchemas(t *testing.T) {
	t.Parallel()

	spec := loadOpenAPI(t, "openapi.yaml")

	files := capturedHTTPFiles(t)
	if len(files) == 0 {
		t.Fatal("HTTP capture fixtures are missing")
	}

	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			assertRecordedHTTPExchangeMatchesSchema(t, spec, path)
		})
	}
}

func assertRecordedHTTPExchangeMatchesSchema(t *testing.T, spec openAPIDocument, path string) {
	t.Helper()

	row := readHTTPExchange(t, path)
	operation := operationFor(t, spec, row.Request.Path, row.Request.Method)
	assertRequestOriginIsDeclared(t, spec, operation, row.Request)
	assertRequestBodyMatchesSchema(t, spec, operation, row.Request)
	assertResponseBodyMatchesSchema(t, spec, operation, row.Response)
}

func assertRequestOriginIsDeclared(
	t *testing.T,
	spec openAPIDocument,
	operation *openapi3.Operation,
	request capturedHTTPRequest,
) {
	t.Helper()

	servers := spec.doc.Servers
	if operation.Servers != nil {
		servers = *operation.Servers
	}

	for _, server := range servers {
		if server.URL == request.Origin {
			return
		}
	}

	t.Fatalf(
		"request origin %q is not declared for %s %s",
		request.Origin,
		request.Method,
		request.Path,
	)
}

func assertRequestBodyMatchesSchema(
	t *testing.T,
	spec openAPIDocument,
	operation *openapi3.Operation,
	request capturedHTTPRequest,
) {
	t.Helper()

	requestBody, requestSchema := httpRequestSchema(operation)
	if requestBody != nil && requestBody.Required && !request.JSON {
		t.Fatal("required request body is absent")
	}

	if !request.JSON {
		return
	}

	if requestSchema == nil {
		t.Fatal("JSON request has no application/json schema")
	}

	err := spec.validate(t, requestSchema, request.Body)
	if err != nil {
		t.Fatalf("request body violates OpenAPI schema: %v", err)
	}
}

func assertResponseBodyMatchesSchema(
	t *testing.T,
	spec openAPIDocument,
	operation *openapi3.Operation,
	responseBody capturedHTTPResponse,
) {
	t.Helper()

	response, responseSchema := httpResponseSchema(operation, responseBody.Status)
	if response == nil {
		t.Fatalf("response status %d has no declared contract", responseBody.Status)
	}

	if !responseBody.JSON {
		assertNonJSONResponseHasNoSchema(t, response, responseBody)

		return
	}

	if responseSchema == nil {
		t.Fatal("JSON response has no application/json schema")
	}

	err := spec.validate(t, responseSchema, responseBody.Body)
	if err != nil {
		t.Fatalf("response body violates OpenAPI schema: %v", err)
	}
}

func assertNonJSONResponseHasNoSchema(
	t *testing.T,
	response *openapi3.Response,
	responseBody capturedHTTPResponse,
) {
	t.Helper()

	if responseBody.Body != nil {
		t.Fatalf("non-JSON response body is %v, expected null", responseBody.Body)
	}

	if len(response.Content) != 0 {
		t.Fatal("non-JSON response contract unexpectedly declares content")
	}
}

func TestInvalidHTTPPayloadsAreRejected(t *testing.T) {
	t.Parallel()

	spec := loadOpenAPI(t, "openapi.yaml")

	cases := []struct {
		path   string
		method string
		body   any
	}{
		{"/commands/v1/devices/{device_id}", "patch", map[string]any{"command_name": "unknown"}},
		{
			"/duos/v1/devices/{device_id}/update",
			"put",
			map[string]any{"entity": map[string]any{"live_view_enabled": "false"}},
		},
	}

	for _, testCase := range cases {
		operation := operationFor(t, spec, testCase.path, testCase.method)

		_, schema := httpRequestSchema(operation)

		err := spec.validate(t, schema, testCase.body)
		if err == nil {
			t.Errorf("%s %s accepted invalid payload %#v", testCase.method, testCase.path, testCase.body)
		}
	}

	deviceList := schemaNamed(t, spec, "DeviceList")
	for _, value := range []any{map[string]any{"devices": map[string]any{}}, map[string]any{}} {
		err := spec.validate(t, deviceList, value)
		if err == nil {
			t.Errorf("DeviceList accepted invalid payload %#v", value)
		}
	}
}

func TestCapturedQueryParametersAreDeclaredAndOptional(t *testing.T) {
	t.Parallel()

	spec := loadOpenAPI(t, "openapi.yaml")

	for _, path := range capturedHTTPFiles(t) {
		row := readHTTPExchange(t, path)
		if len(row.Request.Query) == 0 {
			continue
		}

		operation := operationFor(t, spec, row.Request.Path, row.Request.Method)
		declared := map[string]*openapi3.Parameter{}

		for _, ref := range operation.Parameters {
			if ref != nil && ref.Value != nil {
				declared[ref.Value.In+"\x00"+ref.Value.Name] = ref.Value
			}
		}

		for _, query := range row.Request.Query {
			parameter := declared["query\x00"+query.Name]
			if parameter == nil {
				t.Errorf("%s: query parameter %q is not declared", filepath.Base(path), query.Name)

				continue
			}

			if parameter.Required {
				t.Errorf("%s: captured query parameter %q is marked required", filepath.Base(path), query.Name)
			}
		}
	}
}

func TestDeviceSettingsWireSchemaAndUnknownExtensions(t *testing.T) {
	t.Parallel()

	spec := loadOpenAPI(t, "openapi.yaml")
	responseSchema := schemaNamed(t, spec, "DeviceSettings")

	var captured any

	for _, path := range capturedHTTPFiles(t) {
		if filepath.Base(path) != "device-settings-get.json" {
			continue
		}

		captured = readHTTPExchange(t, path).Response.Body

		break
	}

	if captured == nil {
		t.Fatal("device-settings-get.json capture is missing")
	}

	err := spec.validate(t, responseSchema, captured)
	if err != nil {
		t.Fatalf("captured DeviceSettings body is invalid: %v", err)
	}

	extended := cloneJSON(t, captured)
	motionSettings := nestedMap(extended, "motion_settings")
	motionSettings["future_vendor_field"] = map[string]any{"opaque": []any{1, "x"}}

	err = spec.validate(t, responseSchema, extended)
	if err != nil {
		t.Fatalf("unknown extension field was rejected: %v", err)
	}

	invalid := cloneJSON(t, captured)

	nestedMap(invalid, "motion_settings")["motion_detection_enabled"] = "yes"

	err = spec.validate(t, responseSchema, invalid)

	var validationErr *jsonschema.ValidationError
	if err == nil || !errors.As(err, &validationErr) {
		t.Fatalf("invalid motion_detection_enabled lost its validation cause: %v", err)
	}

	for name, value := range map[string]any{
		"missing motion_settings":       map[string]any{},
		"null motion_detection_enabled": map[string]any{"motion_settings": map[string]any{"motion_detection_enabled": nil}},
		"null motion_settings":          map[string]any{"motion_settings": nil},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := spec.validate(t, responseSchema, value)
			if err != nil {
				t.Fatalf("optional/null settings value was rejected: %v", err)
			}
		})
	}
}

func TestDeviceSpeedBoundsAndExtensibleKinds(t *testing.T) {
	t.Parallel()

	spec := loadOpenAPI(t, "openapi.yaml")

	movement := schemaNamed(t, spec, "PTZMovement")

	for _, testCase := range []struct {
		speed    float64
		accepted bool
	}{
		{-0.01, false}, {0, true}, {0.63, true}, {1, true}, {1.01, false},
	} {
		err := spec.validate(t, movement, map[string]any{"max_speed": testCase.speed})
		if (err == nil) != testCase.accepted {
			t.Errorf(
				"PTZMovement max_speed=%v: accepted=%t, want %t (error %v)",
				testCase.speed,
				err == nil,
				testCase.accepted,
				err,
			)
		}
	}

	device := schemaNamed(t, spec, "Device")

	futureDevice := map[string]any{"id": 1001, "kind": "future_camera", "description": "Future"}

	err := spec.validate(t, device, futureDevice)
	if err != nil {
		t.Fatalf("extensible future device kind was rejected: %v", err)
	}

	kind := nestedMap(spec.raw, "components", "schemas", "Device", "properties", "kind")
	if !containsString(sliceValue(kind["x-extensible-enum"]), "stickup_cam_mini_ptz_v1") {
		t.Fatal("known device kind is absent from x-extensible-enum")
	}

	if _, exists := kind["enum"]; exists {
		t.Fatal("extensible device kind must not use a closed enum")
	}
}

func TestQuerySemanticsAreDescribed(t *testing.T) {
	t.Parallel()

	spec := loadOpenAPI(t, "openapi.yaml")
	parameters := nestedMap(spec.raw, "components", "parameters")
	assertStrings := func(name string, expected []string) {
		t.Helper()

		parameter := mapValue(parameters[name])

		actual := sliceValue(mapValue(parameter["schema"])["x-extensible-enum"])
		if !reflect.DeepEqual(actual, stringsToAny(expected)) {
			t.Errorf("%s x-extensible-enum = %v, want %v", name, actual, expected)
		}
	}
	assertStrings("order", []string{"desc"})
	assertStrings("requestedTransport", []string{"ws"})

	capabilities := sliceValue(mapValue(mapValue(parameters["capabilities"])["schema"])["x-known-tokens"])
	if !reflect.DeepEqual(capabilities, stringsToAny([]string{"offline_event", "vehicle", "ringtercom"})) {
		t.Errorf("capabilities x-known-tokens = %v", capabilities)
	}

	for _, name := range []string{"allowUserOnly", "enableExtendedEmergencyCellUsage", "confirm_delete_favorite"} {
		if got := mapValue(mapValue(parameters[name])["schema"])["type"]; got != "boolean" {
			t.Errorf("%s schema type = %v, want boolean", name, got)
		}
	}

	if got := mapValue(mapValue(parameters["limit"])["schema"])["minimum"]; got != 1 {
		t.Errorf("limit minimum = %v, want 1", got)
	}
}

func stringsToAny(values []string) []any {
	result := make([]any, len(values))
	for i, value := range values {
		result[i] = value
	}

	return result
}

func containsString(values []any, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}

	return false
}

func TestOpenAPIDocumentsValidateAndRejectMissingInfo(t *testing.T) {
	t.Parallel()

	loadOpenAPI(t, "openapi.yaml")
	loadOpenAPI(t, "client-models.openapi.yaml")

	path := filepath.Join(repositoryRoot(t), "api", "openapi.yaml")
	doc := readYAMLObject(t, path)
	delete(doc, "info")
	normalizeOpenAPISchemaForKin(doc)

	data, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false

	invalidDoc, loadErr := loader.LoadFromData(data)
	if loadErr == nil {
		loadErr = invalidDoc.Validate(context.Background())
	}

	if loadErr == nil {
		t.Fatal("OpenAPI validator accepted a document without required info")
	}
}

func TestAsyncAPIDocumentHasRequiredShapeAndOnlyLocalRefs(t *testing.T) {
	t.Parallel()

	path := filepath.Join(repositoryRoot(t), "api", "asyncapi.yaml")
	doc := readYAMLObject(t, path)
	rejectRemoteReferences(t, doc, "asyncapi.yaml")

	if doc["asyncapi"] != "3.0.0" {
		t.Fatalf("asyncapi version = %v, want 3.0.0", doc["asyncapi"])
	}

	info := mapValue(doc["info"])
	if stringValue(info["title"]) == "" || stringValue(info["version"]) == "" {
		t.Fatal("AsyncAPI info.title and info.version are required")
	}

	components := mapValue(doc["components"])
	if len(mapValue(components["schemas"])) == 0 || len(mapValue(components["messages"])) == 0 {
		t.Fatal("AsyncAPI schemas and messages are required")
	}

	if !asyncAPIParserInstalled(t) {
		t.Log("skipping AsyncAPI parser checks; run npm ci in tools/protocols to enable them")

		return
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)

	defer cancel()
	// #nosec G204 -- path is the fixed checked-in AsyncAPI document.
	command := exec.CommandContext(ctx, "node", filepath.Join("tools", "protocols", "validate_asyncapi.mjs"), path)

	command.Dir = repositoryRoot(t)

	{
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("AsyncAPI parser rejected the document: %v\n%s", err, output)
		}
	}
}

func TestAsyncAPIParserRejectsInvalidVersionWhenInstalled(t *testing.T) {
	t.Parallel()

	if !asyncAPIParserInstalled(t) {
		t.Skip("AsyncAPI parser checks require npm ci in tools/protocols")
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)

	defer cancel()

	path := filepath.Join(repositoryRoot(t), "api", "asyncapi.yaml")
	doc := readYAMLObject(t, path)
	doc["asyncapi"] = "not-an-asyncapi-version"

	data, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	invalidPath := filepath.Join(t.TempDir(), "invalid.yaml")
	{
		err := os.WriteFile(invalidPath, data, 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}
	// #nosec G204 -- invalidPath is created inside t.TempDir for this test.
	command := exec.CommandContext(ctx, "node", filepath.Join("tools", "protocols", "validate_asyncapi.mjs"), invalidPath)

	command.Dir = repositoryRoot(t)

	{
		output, err := command.CombinedOutput()
		if err == nil {
			t.Fatalf("AsyncAPI parser accepted invalid version; output: %s", output)
		}
	}
}

func asyncAPIParserInstalled(t *testing.T) bool {
	t.Helper()
	path := filepath.Join(repositoryRoot(t), "tools", "protocols", "node_modules", "@asyncapi", "parser")
	info, err := os.Stat(path)

	return err == nil && info.IsDir()
}
