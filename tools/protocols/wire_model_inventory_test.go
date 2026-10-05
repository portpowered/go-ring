package protocols_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	signalingmodels "github.com/portpowered/go-ring/pkg/dependencymodels/signaling"
	"github.com/portpowered/go-ring/pkg/ring"
)

type wireModelInventory struct {
	name              string
	schemaPath        string
	modelDirectory    string
	generator         string
	transportFiles    []string
	componentTypeName func(string) string
	omittedComponents map[string]string
	extraTypes        map[string]string
}

func TestGeneratedWireModelInventoryCoversEveryNamedComponent(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)

	for _, inventory := range wireModelInventories() {
		t.Run(inventory.name, func(t *testing.T) {
			t.Parallel()

			assertGeneratedWireInventory(t, root, inventory)
		})
	}

	assertAnonymousSignalingOrigins(t)
}

func wireModelInventories() []wireModelInventory {
	asyncExtras := map[string]string{
		"AnonymousSchema_1":    "SignalingInboundDiscriminator.method enum",
		"AnonymousSchema_82":   "ServerCloseReason.code union",
		"AnonymousSchema_91":   "ServerRPCBody.command union",
		"AnonymousSchema_199":  "PTZContinuousWireCommand.method enum",
		"PTzCommand":           "components.messages.PTZCommand frame union",
		"SessionStreamOptions": "channels.sessionStreamOptions message union",
	}
	inventories := []wireModelInventory{
		{
			name:           "provider REST",
			schemaPath:     "api/openapi.yaml",
			modelDirectory: "pkg/dependencymodels/rest",
			generator:      "oapi-codegen v2.8.0, then tools/restmodelsplit split via make generate-api",
			transportFiles: []string{
				"pkg/dependencies/rest/client.go",
				"pkg/ring/client_captured_http.go",
			},
			componentTypeName: identityModelName,
			omittedComponents: map[string]string{},
			extraTypes:        map[string]string{},
		},
		{
			name:           "FCM HTTP and JSON-in-string payloads",
			schemaPath:     "api/external/fcm.openapi.yaml",
			modelDirectory: "pkg/dependencymodels/fcm",
			generator:      "oapi-codegen v2.8.0 pkg/dependencymodels/fcm/config.yaml via make generate-api",
			transportFiles: []string{
				"pkg/dependencies/push/http_transport.go",
				"third_party/go-push-receiver/fcm.go",
				"pkg/ring/client_push.go",
			},
			componentTypeName: identityModelName,
			omittedComponents: map[string]string{},
			extraTypes:        map[string]string{},
		},
		{
			name:           "public read projections",
			schemaPath:     "api/client-models.openapi.yaml",
			modelDirectory: "pkg/ringapimodels",
			generator:      "oapi-codegen v2.8.0 pkg/ringapimodels/config.yaml via make generate-api",
			transportFiles: []string{
				"pkg/ring/client_captured_http.go",
				"pkg/ring/client_read_models.go",
			},
			componentTypeName: identityModelName,
			omittedComponents: map[string]string{},
			extraTypes:        map[string]string{},
		},
		{
			name:           "signaling and push",
			schemaPath:     "api/asyncapi.yaml",
			modelDirectory: "pkg/dependencymodels/signaling",
			generator:      "Modelina tools/protocols/generate_signaling.mjs via make generate-api",
			transportFiles: []string{
				"pkg/dependencies/websocket/wire.go",
				"internal/signaling/session.go",
				"pkg/ring/device_session.go",
				"pkg/ring/signaling_extras.go",
			},
			componentTypeName: normalizeModelinaAcronyms,
			omittedComponents: map[string]string{
				"ClientEnvelope": "the channel's conditional envelope is represented " +
					"by its generated operation-specific frame models",
				"PTZRPC":    "validation constraints are applied to generated PTZ wire command and parameter models",
				"RPCResult": "the server RPC command and generated result value model carry the decoded result",
				"RPCError":  "the server RPC command and generated error value model carry the decoded error",
				"Heartbeat": "the active ping, pong, and push heartbeat messages each have generated frame models",
			},
			extraTypes: asyncExtras,
		},
	}

	return inventories
}

func assertGeneratedWireInventory(t *testing.T, root string, inventory wireModelInventory) {
	t.Helper()

	if inventory.generator == "" || len(inventory.transportFiles) == 0 {
		t.Fatalf("%s inventory has no generator or transport mapping", inventory.name)
	}

	assertInventoryTransportFiles(t, root, inventory)
	schema := readYAMLObject(t, filepath.Join(root, inventory.schemaPath))
	components := nestedMap(schema, "components", "schemas")
	declarations := generatedTypeDeclarations(t, filepath.Join(root, inventory.modelDirectory))
	expected := generatedComponentTypes(t, inventory, components, declarations)
	assertExtraGeneratedTypes(t, inventory, declarations, expected)
}

func assertInventoryTransportFiles(t *testing.T, root string, inventory wireModelInventory) {
	t.Helper()

	for _, relative := range inventory.transportFiles {
		path := filepath.Join(root, relative)

		_, statErr := os.Stat(path)
		if statErr != nil {
			t.Errorf("%s transport use %s is missing: %v", inventory.name, relative, statErr)
		}
	}
}

func generatedComponentTypes(
	t *testing.T,
	inventory wireModelInventory,
	components map[string]any,
	declarations map[string]bool,
) map[string]bool {
	t.Helper()

	expected := make(map[string]bool, len(components)+len(inventory.extraTypes))

	for componentName := range components {
		goName := inventory.componentTypeName(componentName)

		if reason, omitted := inventory.omittedComponents[componentName]; omitted {
			if reason == "" {
				t.Errorf("%s omission %s has no transport mapping", inventory.name, componentName)
			}

			continue
		}

		expected[goName] = true

		if !declarations[goName] {
			t.Errorf("%s schema component %s has no generated Go definition %s", inventory.name, componentName, goName)
		}
	}

	return expected
}

func assertExtraGeneratedTypes(
	t *testing.T,
	inventory wireModelInventory,
	declarations map[string]bool,
	expected map[string]bool,
) {
	t.Helper()

	for goName, schemaOrigin := range inventory.extraTypes {
		expected[goName] = true

		if schemaOrigin == "" {
			t.Errorf("generated type %s has no schema or message origin", goName)
		}

		if !declarations[goName] {
			t.Errorf("%s schema origin %s has no generated Go definition %s", inventory.name, schemaOrigin, goName)
		}
	}

	if len(inventory.extraTypes) == 0 {
		return
	}

	for goName := range declarations {
		if !expected[goName] {
			t.Errorf("%s generated type %s has no component or explicit anonymous/message origin", inventory.name, goName)
		}
	}
}

func TestHandwrittenPublicSignalingAdaptersMatchGeneratedSchemaFields(t *testing.T) {
	t.Parallel()

	schema := readYAMLObject(t, filepath.Join(repositoryRoot(t), "api", "asyncapi.yaml"))
	components := nestedMap(schema, "components", "schemas")
	checks := map[string]reflect.Type{
		"PushFilter":     reflect.TypeFor[ring.PushFilter](),
		"PushFilters":    reflect.TypeFor[ring.PushFilters](),
		"PushEventBody":  reflect.TypeFor[ring.PushEvent](),
		"RPCResultValue": reflect.TypeFor[ring.PTZResult](),
		"LiveViewBody":   reflect.TypeFor[ring.SessionDescription](),
	}

	for name, model := range checks {
		properties := mapValue(components[name])["properties"]

		schemaFields := make([]string, 0, len(mapValue(properties)))

		for property := range mapValue(properties) {
			schemaFields = append(schemaFields, property)
		}

		slices.Sort(schemaFields)

		actualFields, _ := projectionJSONFields(model)

		if name == "LiveViewBody" {
			schemaFields = []string{"sdp", "type"}
		}

		if !slices.Equal(actualFields, schemaFields) {
			t.Errorf("public signaling adapter %s JSON fields = %v; schema = %v", name, actualFields, schemaFields)
		}
	}
}

func identityModelName(name string) string { return name }

func normalizeModelinaAcronyms(name string) string {
	for _, acronym := range []string{"ICE", "PTZ", "RPC"} {
		name = strings.ReplaceAll(name, acronym, strings.ToUpper(acronym[:1])+strings.ToLower(acronym[1:]))
	}

	return name
}

func generatedTypeDeclarations(t *testing.T, directory string) map[string]bool {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join(directory, "*.go"))
	if err != nil {
		t.Fatal(err)
	}

	declarations := make(map[string]bool)

	for _, path := range paths {
		data, err := os.ReadFile(path) // #nosec G304 -- path is a checked-in model package.
		if err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(string(data), "Code generated by") {
			continue
		}

		file, err := parser.ParseFile(token.NewFileSet(), path, data, 0)
		if err != nil {
			t.Fatalf("parse generated file %s: %v", path, err)
		}

		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}

			for _, spec := range general.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}

				declarations[typeSpec.Name.Name] = true
			}
		}
	}

	if len(declarations) == 0 {
		t.Errorf("model package %s has no generated type declarations", directory)
	}

	return declarations
}

type anonymousSignalingOrigin struct {
	goType    string
	owner     reflect.Type
	property  string
	ownerName string
}

func assertAnonymousSignalingOrigins(t *testing.T) {
	t.Helper()

	for _, origin := range anonymousSignalingOrigins() {
		field, found := generatedFieldByJSONName(origin.owner, origin.property)
		if !found {
			t.Errorf("%s has no generated field", origin.ownerName)

			continue
		}

		fieldType := field.Type
		for fieldType.Kind() == reflect.Pointer || fieldType.Kind() == reflect.Slice {
			fieldType = fieldType.Elem()
		}

		if fieldType.Name() != origin.goType {
			t.Errorf(
				"%s resolves to %s, want its generated anonymous schema type %s",
				origin.ownerName,
				fieldType.Name(),
				origin.goType,
			)
		}
	}

	for name, expected := range map[string][]string{
		"PTzCommand":     {"PtzCommandFrame", "PtzContinuousCommandFrame"},
		"ServerEnvelope": {"SignalingInboundDiscriminator", "ServerRpcFrame", "PushEventFrame"},
	} {
		model := generatedSignalingType(name)

		fields := make([]string, model.NumField())

		for index := range model.NumField() {
			fields[index] = model.Field(index).Type.Name()
		}

		for _, want := range expected {
			if !slices.Contains(fields, want) {
				t.Errorf("generated union %s fields = %v, missing schema message member %s", name, fields, want)
			}
		}
	}
}

func anonymousSignalingOrigins() []anonymousSignalingOrigin {
	return []anonymousSignalingOrigin{
		{
			goType: "AnonymousSchema_1", owner: reflect.TypeFor[signalingmodels.SignalingInboundDiscriminator](),
			property: "method", ownerName: "SignalingInboundDiscriminator.method",
		},
		{
			goType: "AnonymousSchema_82", owner: reflect.TypeFor[signalingmodels.ServerCloseReason](),
			property: "code", ownerName: "ServerCloseReason.code",
		},
		{
			goType: "AnonymousSchema_91", owner: reflect.TypeFor[signalingmodels.ServerRpcBody](),
			property: "command", ownerName: "ServerRPCBody.command",
		},
		{
			goType: "AnonymousSchema_199", owner: reflect.TypeFor[signalingmodels.PtzContinuousWireCommand](),
			property: "method", ownerName: "PTZContinuousWireCommand.method",
		},
	}
}

func generatedSignalingType(name string) reflect.Type {
	switch name {
	case "PTzCommand":
		return reflect.TypeFor[signalingmodels.PTzCommand]()
	case "ServerEnvelope":
		return reflect.TypeFor[signalingmodels.ServerEnvelope]()
	default:
		panic("unknown generated signaling union " + name)
	}
}

func generatedFieldByJSONName(model reflect.Type, name string) (reflect.StructField, bool) {
	for index := range model.NumField() {
		field := model.Field(index)

		jsonName, _, _ := strings.Cut(field.Tag.Get("json"), ",")

		if jsonName == name {
			return field, true
		}
	}

	var zero reflect.StructField

	return zero, false
}
