package protocols_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestGeneratedSignalingFieldConstantsComeFromAsyncAPISchema(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	async := readYAMLObject(t, filepath.Join(root, "api", "asyncapi.yaml"))
	properties := make(map[string]bool)
	collectSchemaPropertyNames(nestedMap(async, "components", "schemas"), properties)

	path := filepath.Join(root, "internal", "protocol", "signaling.go")

	fieldConstants := generatedStringConstants(t, path, "Field")
	if len(fieldConstants) == 0 {
		t.Fatal("generated signaling field constants are missing")
	}

	for name, value := range fieldConstants {
		if !properties[value] {
			t.Errorf("%s = %q is not an AsyncAPI schema property", name, value)
		}
	}
}

func TestGeneratedFCMPushKeyConstantsComeFromExternalSchema(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	schema := readYAMLObject(t, filepath.Join(root, "api", "external", "fcm.openapi.yaml"))
	components := nestedMap(schema, "components", "schemas")
	wanted := map[string]struct {
		component string
		property  string
	}{
		"FCMPushAndroidConfigKey":  {component: "RingPushNotificationEnvelope", property: "android_config"},
		"FCMPushDataKey":           {component: "RingPushNotificationEnvelope", property: "data"},
		"FCMPushDoorbotIDKey":      {component: "RingPushNotificationEnvelope", property: "doorbot_id"},
		"FCMPushCategoryKey":       {component: "RingPushNotificationConfig", property: "category"},
		"FCMPushPayloadDeviceKey":  {component: "RingPushNotificationPayload", property: "device"},
		"FCMPushPayloadGCMDataKey": {component: "RingPushNotificationPayload", property: "gcmData"},
		"FCMPushDeviceIDKey":       {component: "RingPushNotificationDevice", property: "id"},
		"FCMPushGCMActionKey":      {component: "RingPushNotificationGCMData", property: "action"},
	}

	path := filepath.Join(root, "internal", "protocol", "fcm.go")
	actual := generatedStringConstants(t, path, "FCMPush")

	if len(actual) != len(wanted) {
		t.Errorf("generated FCM push-key constants = %v; want %d entries", actual, len(wanted))
	}

	for name, mapping := range wanted {
		if actual[name] != mapping.property {
			t.Errorf("%s = %q; want %q from %s.%s", name, actual[name], mapping.property, mapping.component, mapping.property)
		}

		properties := mapValue(mapValue(components[mapping.component])["properties"])
		if _, ok := properties[mapping.property]; !ok {
			t.Errorf("%s.%s is not declared by the FCM schema", mapping.component, mapping.property)
		}
	}

	clientPath := filepath.Join(root, "pkg", "ring", "client_push.go")
	client := parseGoFile(t, clientPath)

	if literals := signalingWireKeyLiterals(client); len(literals) > 0 {
		t.Errorf("%s contains literal FCM wire keys %v; use generated protocol constants", clientPath, literals)
	}
}

func TestGeneratedPublicProjectionValuesComeFromSchema(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	schema := readYAMLObject(t, filepath.Join(root, "api", "client-models.openapi.yaml"))
	components := nestedMap(schema, "components", "schemas")
	wanted := map[string]struct {
		component string
		value     string
	}{
		"DeviceKindStickUpMiniPTZ":  {component: "DeviceKind", value: "stickup_cam_mini_ptz_v1"},
		"ConnectionOnline":          {component: "ConnectionState", value: "online"},
		"ConnectionOffline":         {component: "ConnectionState", value: "offline"},
		"PowerModeWired":            {component: "PowerMode", value: "wired"},
		"LocationResourceLocations": {component: "LocationResourceType", value: "locations"},
		"TimelineEventOnDemand":     {component: "TimelineEventType", value: "on_demand"},
		"TimelineEventDing":         {component: "TimelineEventType", value: "ding"},
		"TimelineEventMotion":       {component: "TimelineEventType", value: "motion"},
		"RecordingStatusReady":      {component: "RecordingStatus", value: "ready"},
		"TimelineStateCompleted":    {component: "TimelineState", value: "completed"},
		"HistoryFeedEvent":          {component: "HistoryFeedType", value: "EVENT"},
	}

	path := filepath.Join(root, "internal", "protocol", "public_models.go")
	actual := generatedStringConstants(t, path, "")

	if !reflect.DeepEqual(actual, publicModelExpectedValues(wanted)) {
		t.Errorf("generated public projection values = %v; want %v", actual, publicModelExpectedValues(wanted))
	}

	knownValues := make(map[string]bool, len(wanted))

	for _, mapping := range wanted {
		component := mapValue(components[mapping.component])
		schemaValues := sliceValue(component["x-extensible-enum"])
		found := false

		for _, value := range schemaValues {
			if value == mapping.value {
				found = true

				break
			}
		}

		if !found {
			t.Errorf("%s is missing from the %s schema values", mapping.value, mapping.component)
		}

		knownValues[mapping.value] = true
	}

	clientPath := filepath.Join(root, "pkg", "ring", "client_read_models.go")
	client := parseGoFile(t, clientPath)

	if literals := schemaValueLiterals(client, knownValues); len(literals) > 0 {
		t.Errorf("%s hard-codes schema-defined public values %v; use generated protocol constants", clientPath, literals)
	}
}

func TestPublicProjectionValueGateRejectsInlineKnownValue(t *testing.T) {
	t.Parallel()

	negative := `package sample
const value = "stickup_cam_mini_ptz_v1"
`

	file, err := parser.ParseFile(token.NewFileSet(), "negative_public_value.go", negative, 0)
	if err != nil {
		t.Fatal(err)
	}

	got := schemaValueLiterals(file, map[string]bool{"stickup_cam_mini_ptz_v1": true})

	want := []string{"stickup_cam_mini_ptz_v1"}

	if !slices.Equal(got, want) {
		t.Fatalf("public schema value gate findings = %v, want %v", got, want)
	}
}

func publicModelExpectedValues(wanted map[string]struct {
	component string
	value     string
}) map[string]string {
	expected := make(map[string]string, len(wanted))
	for name, mapping := range wanted {
		expected[name] = mapping.value
	}

	return expected
}

func TestMCSAppDataCryptoKeysComeFromProtocolInventory(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	inventory := loadFCMInventory(t, root)
	wanted := map[string]string{
		"ContentEncoding": "content-encoding",
		"CryptoKey":       "crypto-key",
		"Encryption":      "encryption",
	}
	wantedValues := map[string]string{
		"ContentEncodingAES128GCM": "aes128gcm",
		"CryptoKeyDHPrefix":        "dh=",
		"EncryptionSaltPrefix":     "salt=",
	}

	if !reflect.DeepEqual(inventory.MCS.AppDataKeys, wanted) {
		t.Fatalf("MCS AppData key inventory = %v, want %v", inventory.MCS.AppDataKeys, wanted)
	}

	if !reflect.DeepEqual(inventory.MCS.AppDataValues, wantedValues) {
		t.Fatalf("MCS AppData value inventory = %v, want %v", inventory.MCS.AppDataValues, wantedValues)
	}

	path := filepath.Join(root, "internal", "protocol", "mcs.gen.go")
	generated := map[string]string{
		"MCSAppDataContentEncodingKey":       wanted["ContentEncoding"],
		"MCSAppDataCryptoKeyKey":             wanted["CryptoKey"],
		"MCSAppDataEncryptionKey":            wanted["Encryption"],
		"MCSAppDataContentEncodingAES128GCM": wantedValues["ContentEncodingAES128GCM"],
		"MCSAppDataCryptoKeyDHPrefix":        wantedValues["CryptoKeyDHPrefix"],
		"MCSAppDataEncryptionSaltPrefix":     wantedValues["EncryptionSaltPrefix"],
	}
	actual := generatedStringConstants(t, path, "MCSAppData")

	if !reflect.DeepEqual(actual, generated) {
		t.Errorf("generated MCS AppData keys = %v, want %v", actual, generated)
	}

	cryptoPath := filepath.Join(root, "third_party", "go-push-receiver", "crypto.go")

	crypto := parseGoFile(t, cryptoPath)

	if literals := findByKeyStringArguments(crypto); len(literals) > 0 {
		t.Errorf("%s hard-codes AppData keys %v; use generated protocol constants", cryptoPath, literals)
	}

	knownValues := make(map[string]bool, len(wantedValues))
	for _, value := range wantedValues {
		knownValues[value] = true
	}

	if literals := schemaValueLiterals(crypto, knownValues); len(literals) > 0 {
		t.Errorf("%s hard-codes MCS AppData values %v; use generated protocol constants", cryptoPath, literals)
	}
}

func TestMCSAppDataGateRejectsUnregisteredKeysAndValues(t *testing.T) {
	t.Parallel()

	keyFixture := `package sample
func f(items []Item) { findByKey(items, "unregistered-app-data-key") }
`

	keyFile, err := parser.ParseFile(token.NewFileSet(), "negative_app_data_key.go", keyFixture, 0)
	if err != nil {
		t.Fatal(err)
	}

	gotKeys := findByKeyStringArguments(keyFile)

	wantKeys := []string{"unregistered-app-data-key"}

	if !slices.Equal(gotKeys, wantKeys) {
		t.Fatalf("AppData key gate findings = %v, want %v", gotKeys, wantKeys)
	}

	valueFixture := `package sample
func f(value string) bool { return value == "aes128gcm" }
`

	valueFile, err := parser.ParseFile(token.NewFileSet(), "negative_app_data_value.go", valueFixture, 0)
	if err != nil {
		t.Fatal(err)
	}

	gotValues := schemaValueLiterals(valueFile, map[string]bool{"aes128gcm": true})

	wantValues := []string{"aes128gcm"}

	if !slices.Equal(gotValues, wantValues) {
		t.Fatalf("AppData value gate findings = %v, want %v", gotValues, wantValues)
	}
}

func TestKnownSignalingValuesUseGeneratedProtocolConstants(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	async := readYAMLObject(t, filepath.Join(root, "api", "asyncapi.yaml"))
	knownValues := make(map[string]bool)
	collectSchemaKnownValues(nestedMap(async, "components", "schemas"), knownValues)

	for _, relative := range []string{
		"pkg/dependencies/websocket/wire.go",
		"pkg/dependencies/websocket/wire_close_union.go",
		"pkg/dependencies/websocket/writer.go",
		"pkg/dependencies/websocket/live_negotiation.go",
		"internal/signaling/session.go",
		"pkg/ring/device_session.go",
		"pkg/ring/signaling_extras.go",
	} {
		path := filepath.Join(root, relative)

		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}

		if literals := schemaValueLiterals(file, knownValues); len(literals) > 0 {
			t.Errorf("%s hard-codes schema-defined signaling values %v", relative, literals)
		}
	}

	negative := `package sample
func f() {
	switch "close" { case "live_view": }
}`

	file, err := parser.ParseFile(token.NewFileSet(), "negative_values.go", negative, 0)
	if err != nil {
		t.Fatal(err)
	}

	got := schemaValueLiterals(file, map[string]bool{"close": true, "live_view": true})

	want := []string{"close", "live_view"}

	if !slices.Equal(got, want) {
		t.Fatalf("schema-value literal gate findings = %v, want both hard-coded values %v", got, want)
	}
}

func generatedStringConstants(t *testing.T, path, prefix string) map[string]string {
	t.Helper()

	file := parseGoFile(t, path)
	constants := make(map[string]string)

	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}

		for _, spec := range general.Specs {
			values, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}

			for index, name := range values.Names {
				if index >= len(values.Values) || (prefix != "" && !strings.HasPrefix(name.Name, prefix)) {
					continue
				}

				value, ok := stringBasicLiteral(values.Values[index])
				if !ok {
					t.Errorf("%s is not a generated string constant", name.Name)

					continue
				}

				constants[name.Name] = value
			}
		}
	}

	return constants
}

func parseGoFile(t *testing.T, path string) *ast.File {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	return file
}
