package protocols_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/portpowered/go-ring/internal/ringerrors"
	"gopkg.in/yaml.v3"
)

type fcmInventory struct {
	Provenance fcmInventoryProvenance `yaml:"provenance"`
	MCS        mcsSocketInventory     `yaml:"mcs_socket"`
	Protobuf   fcmProtobufInventory   `yaml:"protobuf"`
}

type mcsSocketInventory struct {
	Host           string            `yaml:"host"`
	Port           int               `yaml:"port"`
	Network        string            `yaml:"network"`
	TLS            bool              `yaml:"tls"`
	Domain         string            `yaml:"domain"`
	Version        int               `yaml:"version"`
	VersionBytes   int               `yaml:"version_bytes"`
	TagBytes       int               `yaml:"tag_bytes"`
	LengthEncoding string            `yaml:"length_encoding"`
	Callsite       string            `yaml:"callsite"`
	AppDataKeys    map[string]string `yaml:"app_data_keys"`
	AppDataValues  map[string]string `yaml:"app_data_values"`
	Tags           map[string]int    `yaml:"tags"`
}

type fcmInventoryProvenance struct {
	Module       string `yaml:"module"`
	Version      string `yaml:"version"`
	TagCommit    string `yaml:"tag_commit"`
	ModuleSum    string `yaml:"module_sum"`
	LocalPackage string `yaml:"local_package"`
}

type fcmProtobufInventory struct {
	Generator        fcmProtobufGenerator `yaml:"generator"`
	SourceFiles      []fcmInventoryFile   `yaml:"source_files"`
	GeneratedGoFiles []fcmInventoryFile   `yaml:"generated_go_files"`
}

type fcmProtobufGenerator struct {
	Protoc      string `yaml:"protoc"`
	ProtocGenGo string `yaml:"protoc_gen_go"`
	Runtime     string `yaml:"runtime"`
}

type fcmInventoryFile struct {
	Path     string   `yaml:"path"`
	SHA256   string   `yaml:"sha256"`
	Source   string   `yaml:"source"`
	Messages []string `yaml:"messages"`
}

func TestExternalFCMOpenAPIAndJSONModels(t *testing.T) {
	t.Parallel()

	spec := loadOpenAPI(t, filepath.Join("external", "fcm.openapi.yaml"))
	operations := fcmOperations()
	assertFCMOperationNames(t, spec, operations)

	checkin := operationFor(t, spec, operations[0].path, "post")
	assertFCMCheckinContract(t, spec, checkin)

	legacyRegistration := operationFor(t, spec, operations[1].path, "post")
	assertFCMLegacyRegistrationContract(t, spec, legacyRegistration)

	installation := operationFor(t, spec, operations[2].path, "post")
	assertFCMInstallationContract(t, spec, installation)

	registration := operationFor(t, spec, operations[3].path, "post")
	assertFCMRegistrationContract(t, spec, registration)
	assertFCMExamples(t, spec, legacyRegistration, installation, registration)
}

type fcmOperation struct {
	path string
	name string
}

func fcmOperations() []fcmOperation {
	return []fcmOperation{
		{path: "/checkin", name: "checkInFCMClient"},
		{path: "/c2dm/register3", name: "registerFCMClient"},
		{path: "/v1/projects/ring-17770/installations", name: "createFCMInstallation"},
		{path: "/v1/projects/ring-17770/registrations", name: "registerFCMInstallation"},
	}
}

func assertFCMOperationNames(t *testing.T, spec openAPIDocument, operations []fcmOperation) {
	t.Helper()

	for _, expected := range operations {
		operation := operationFor(t, spec, expected.path, "post")
		if operation.OperationID != expected.name {
			t.Errorf("%s operationId = %q, want %q", expected.path, operation.OperationID, expected.name)
		}
	}
}

func assertFCMCheckinContract(t *testing.T, spec openAPIDocument, operation *openapi3.Operation) {
	t.Helper()
	assertFCMOperationServer(t, operation, "https://android.clients.google.com")
	assertFCMHeaderParameters(t, spec, operation, map[string]string{"Content-Type": "application/x-protobuf"})
	assertFCMOperationMedia(t, operation, "application/x-protobuf", "application/x-protobuf", 200)
	assertBinaryFCMSchema(t, fcmRequestSchema(operation, "application/x-protobuf"), "check-in request")
	assertBinaryFCMSchema(t, fcmResponseSchema(operation, 200, "application/x-protobuf"), "check-in response")
}

func assertBinaryFCMSchema(t *testing.T, schema *openapi3.SchemaRef, name string) {
	t.Helper()

	if schema == nil || schema.Value == nil || schema.Value.Format != "binary" {
		t.Errorf("%s schema must describe protobuf wire bytes", name)
	}
}

func assertFCMLegacyRegistrationContract(t *testing.T, spec openAPIDocument, operation *openapi3.Operation) {
	t.Helper()
	assertFCMOperationServer(t, operation, "https://android.clients.google.com")
	assertFCMHeaderParameters(t, spec, operation, map[string]string{
		"Content-Type":  "application/x-www-form-urlencoded",
		"Authorization": "AidLogin 12345:67890",
		"User-Agent":    "",
	})
	assertFCMOperationMedia(t, operation, "application/x-www-form-urlencoded", "text/plain", 200)
}

func assertFCMInstallationContract(t *testing.T, spec openAPIDocument, operation *openapi3.Operation) {
	t.Helper()
	assertFCMOperationServer(t, operation, "https://firebaseinstallations.googleapis.com")
	assertFCMHeaderParameters(t, spec, operation, map[string]string{
		"Accept": "application/json", "Content-Type": "application/json",
		"X-Goog-Api-Key": "AIzaSyCv-hdFBmmdBBJadNy-TFwB-xN_H5m3Bk8",
	})
	assertFCMOperationMedia(t, operation, "application/json", "application/json", 200, 201)
}

func assertFCMRegistrationContract(t *testing.T, spec openAPIDocument, operation *openapi3.Operation) {
	t.Helper()
	assertFCMOperationServer(t, operation, "https://fcmregistrations.googleapis.com")
	assertFCMHeaderParameters(t, spec, operation, map[string]string{
		"Content-Type": "application/json", "X-Goog-Api-Key": "AIzaSyCv-hdFBmmdBBJadNy-TFwB-xN_H5m3Bk8",
		"X-Goog-Firebase-Installations-Auth": "synthetic-install-token",
	})
	assertFCMOperationMedia(t, operation, "application/json", "application/json", 200, 201)
}

func assertFCMExamples(
	t *testing.T,
	spec openAPIDocument,
	legacyRegistration, installation, registration *openapi3.Operation,
) {
	t.Helper()

	assertFCMJSONExample(t, spec, installation, "application/json", 200,
		map[string]any{
			"appId": "1:876313859327:android:e10ec6ddb3c81f39", "authVersion": "FIS_v2",
			"fid": "cAAAAAAAAAAAAAAAAAAAAAA=", "sdkVersion": "w:0.6.17",
		},
		map[string]any{
			"name": "projects/ring-17770/installations/synthetic", "fid": "cAAAAAAAAAAAAAAAAAAAAAA=",
			"refreshToken": "synthetic-refresh-token",
			"authToken":    map[string]any{"token": "synthetic-install-token", "expiresIn": "604800s"},
		})
	assertFCMJSONExample(t, spec, registration, "application/json", 200,
		map[string]any{"web": map[string]any{
			"endpoint": "https://fcm.googleapis.com/fcm/send/synthetic-token",
			"p256dh":   "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=",
			"auth":     "AgICAgICAgICAgICAgICAg==",
		}},
		map[string]any{"token": "synthetic-fcm-token", "pushSet": "synthetic-push-set"})
	assertFCMLegacyExamples(t, spec, legacyRegistration)
}

func assertFCMJSONExample(
	t *testing.T,
	spec openAPIDocument,
	operation *openapi3.Operation,
	contentType string,
	status int,
	request, response any,
) {
	t.Helper()
	assertFCMRequestExample(t, spec, operation, contentType, request)
	assertFCMResponseExample(t, spec, operation, status, contentType, response)
}

func assertFCMRequestExample(
	t *testing.T,
	spec openAPIDocument,
	operation *openapi3.Operation,
	contentType string,
	request any,
) {
	t.Helper()

	requestSchema := fcmRequestSchema(operation, contentType)
	if requestSchema == nil {
		t.Errorf("%s has no %s request schema", operation.OperationID, contentType)

		return
	}

	err := spec.validate(t, requestSchema, request)
	if err != nil {
		t.Errorf("%s rejected a valid %s request", operation.OperationID, contentType)
	}
}

func assertFCMResponseExample(
	t *testing.T,
	spec openAPIDocument,
	operation *openapi3.Operation,
	status int,
	contentType string,
	response any,
) {
	t.Helper()

	responseSchema := fcmResponseSchema(operation, status, contentType)
	if responseSchema == nil {
		t.Errorf("%s has no %d response", operation.OperationID, status)

		return
	}

	err := spec.validate(t, responseSchema, response)
	if err != nil {
		t.Errorf("%s rejected a valid %s response", operation.OperationID, contentType)
	}
}

func assertFCMLegacyExamples(t *testing.T, spec openAPIDocument, operation *openapi3.Operation) {
	t.Helper()

	requestSchema := fcmRequestSchema(operation, "application/x-www-form-urlencoded")
	if requestSchema == nil {
		t.Fatal("legacy registration request schema is missing")
	}

	legacyRequest := map[string]any{
		"app":       "org.chromium.linux",
		"X-subtype": "1:876313859327:android:e10ec6ddb3c81f39",
		"device":    "12345",
		"sender": "BDOU99-h67HcA6JeFXHbSNMu7e2yNNu3RzoMj8TM4W88jITfq7ZmPvIM1Iv-" +
			"4_l2LxQcYwhqby2xGpWwzjfAnG4",
	}

	err := spec.validate(t, requestSchema, legacyRequest)
	if err != nil {
		t.Errorf("legacy registration request schema rejected a valid synthetic request: %v", err)
	}

	responseSchema := fcmResponseSchema(operation, 200, "text/plain")
	if responseSchema == nil {
		t.Fatal("legacy registration response schema is missing")
	}

	err = spec.validate(t, responseSchema, "token=synthetic-legacy-token")
	if err != nil {
		t.Errorf("legacy registration response schema rejected a valid synthetic response: %v", err)
	}

	legacyRequest["unexpected"] = "rejected"
	if spec.validate(t, requestSchema, legacyRequest) == nil {
		t.Error("legacy registration request schema accepted an unlisted form field")
	}
}

func TestFCMProtocolInventoryPinsSourcesAndGeneratedGo(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	inventory := loadFCMInventory(t, root)
	assertFCMReceiverProvenance(t, inventory)
	assertFCMProtobufGenerator(t, inventory)
	assertFCMInventoryFiles(t, root, inventory.Protobuf.SourceFiles,
		filepath.Join("third_party", "go-push-receiver", "proto", "*.proto"), false, inventory.Protobuf.Generator)
	assertFCMInventoryFiles(t, root, inventory.Protobuf.GeneratedGoFiles,
		filepath.Join("third_party", "go-push-receiver", "pb", "*", "*.pb.go"), true, inventory.Protobuf.Generator)
	assertFCMModulePackaging(t, root)
}

func TestMCSInventoryPinsSocketAndActiveMessageMappings(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	inventory := loadFCMInventory(t, root)

	socket := inventory.MCS
	if socket.Host != "mtalk.google.com" || socket.Port != 5228 || socket.Network != "tcp" || !socket.TLS ||
		socket.Domain != "mcs.android.com" || socket.Version != 41 || socket.VersionBytes != 1 ||
		socket.TagBytes != 1 || socket.LengthEncoding != "protobuf-varint" ||
		socket.Callsite != "third_party/go-push-receiver/fcm.go:tryToConnect" {
		t.Fatal("MCS socket inventory does not match the pinned protocol contract")
	}

	wantedTags := map[string]int{
		"HeartbeatPing": 0, "HeartbeatAck": 1, "LoginRequest": 2, "LoginResponse": 3,
		"Close": 4, "MessageStanza": 5, "PresenceStanza": 6, "IqStanza": 7,
		"DataMessageStanza": 8, "BatchPresenceStanza": 9, "StreamErrorStanza": 10,
		"HTTPRequest": 11, "HTTPResponse": 12, "BindAccountRequest": 13,
		"BindAccountResponse": 14, "TalkMetadata": 15, "NumProtoTypes": 16, "Unknown": 255,
	}
	if !reflect.DeepEqual(socket.Tags, wantedTags) {
		t.Errorf("MCS tag inventory = %v, want %v", socket.Tags, wantedTags)
	}

	wantedMessages := []string{
		"HeartbeatPing", "HeartbeatAck", "LoginRequest", "LoginResponse",
		"Close", "IqStanza", "DataMessageStanza", "StreamErrorStanza",
	}

	var listedMessages []string

	for _, file := range inventory.Protobuf.SourceFiles {
		if file.Path == "third_party/go-push-receiver/proto/mcs.proto" {
			listedMessages = file.Messages

			break
		}
	}

	sort.Strings(listedMessages)
	sort.Strings(wantedMessages)

	if !reflect.DeepEqual(listedMessages, wantedMessages) {
		t.Fatalf("active MCS messages = %v, want %v", listedMessages, wantedMessages)
	}

	tagSource, err := readRepositoryFile(t, root, filepath.Join("third_party", "go-push-receiver", "tag.go"))
	if err != nil {
		t.Fatal(err)
	}

	activePattern := regexp.MustCompile(`return new\(pb\.([A-Za-z_][A-Za-z0-9_]*)\)`)
	matches := activePattern.FindAllStringSubmatch(string(tagSource), -1)

	activeMessages := make([]string, 0, len(matches))

	for _, match := range matches {
		activeMessages = append(activeMessages, match[1])
	}

	sort.Strings(activeMessages)

	if !reflect.DeepEqual(activeMessages, wantedMessages) {
		t.Fatalf("GenerateMessage active types = %v, inventory lists %v", activeMessages, wantedMessages)
	}

	protoSource, err := readRepositoryFile(t, root, filepath.Join("third_party", "go-push-receiver", "proto", "mcs.proto"))
	if err != nil {
		t.Fatal(err)
	}

	taggedMessages := taggedMCSProtoMessages(string(protoSource))
	wantedTaggedMessages := []string{
		"HeartbeatPing", "HeartbeatAck", "LoginRequest", "LoginResponse",
		"Close", "IqStanza", "DataMessageStanza",
	}

	sort.Strings(taggedMessages)
	sort.Strings(wantedTaggedMessages)

	if !reflect.DeepEqual(taggedMessages, wantedTaggedMessages) {
		t.Fatalf("tagged MCS proto messages = %v, want %v", taggedMessages, wantedTaggedMessages)
	}
}

func taggedMCSProtoMessages(source string) []string {
	tagPattern := regexp.MustCompile(`TAG:\s*\d+`)
	messagePattern := regexp.MustCompile(`^\s*message\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{`)
	lines := strings.Split(source, "\n")

	var messages []string

	for index, line := range lines {
		if !tagPattern.MatchString(line) {
			continue
		}

		for _, candidate := range lines[index+1:] {
			if tagPattern.MatchString(candidate) {
				break
			}

			match := messagePattern.FindStringSubmatch(candidate)
			if len(match) == 2 {
				messages = append(messages, match[1])

				break
			}
		}
	}

	return messages
}
func loadFCMInventory(t *testing.T, root string) fcmInventory {
	t.Helper()

	data, err := readRepositoryFile(t, root, filepath.Join("api", "external", "push-protocol-inventory.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	var inventory fcmInventory

	err = yaml.Unmarshal(data, &inventory)
	if err != nil {
		t.Fatal(err)
	}

	return inventory
}

func assertFCMReceiverProvenance(t *testing.T, inventory fcmInventory) {
	t.Helper()

	provenance := inventory.Provenance
	if provenance.Module != "github.com/crow-misia/go-push-receiver" ||
		provenance.Version != "v0.3.0" ||
		provenance.TagCommit != "6d572da2d7ef29efcfebc06227e1cfeb177f41f5" ||
		provenance.ModuleSum != "h1:1AsABaBqvOdOrke/S2kULGiJ7fiqOfbdupC+lNgAU4U=" ||
		provenance.LocalPackage != "third_party/go-push-receiver" {
		t.Fatal("FCM receiver provenance does not match the pinned upstream module")
	}
}

func assertFCMProtobufGenerator(t *testing.T, inventory fcmInventory) {
	t.Helper()

	generator := inventory.Protobuf.Generator
	if generator.Protoc != "6.31.1" || generator.ProtocGenGo != "v1.36.6" ||
		generator.Runtime != "google.golang.org/protobuf v1.36.8" {
		t.Fatal("FCM protobuf generator inventory is not pinned")
	}
}

func assertFCMInventoryFiles(
	t *testing.T,
	root string,
	files []fcmInventoryFile,
	subtree string,
	generated bool,
	generator fcmProtobufGenerator,
) {
	t.Helper()

	listed := make([]string, 0, len(files))

	for _, file := range files {
		listed = append(listed, file.Path)

		contents, err := readRepositoryFile(t, root, filepath.FromSlash(file.Path))
		if err != nil {
			t.Fatal(err)
		}

		assertFCMFileHash(t, file, contents)

		if generated {
			assertFCMGeneratedMarkers(t, file, contents, generator)
		}
	}

	glob, err := filepath.Glob(filepath.Join(root, subtree))
	if err != nil {
		t.Fatal(err)
	}

	actual := make([]string, 0, len(glob))

	for _, path := range glob {
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			t.Fatal(relErr)
		}

		actual = append(actual, filepath.ToSlash(relative))
	}

	sort.Strings(actual)
	sort.Strings(listed)

	if !reflect.DeepEqual(actual, listed) {
		t.Errorf("%s inventory = %v, listed %v", subtree, actual, listed)
	}
}

func assertFCMFileHash(t *testing.T, file fcmInventoryFile, contents []byte) {
	t.Helper()

	digest := sha256.Sum256(contents)
	if hex.EncodeToString(digest[:]) != file.SHA256 {
		t.Errorf("pinned file %s hash changed", file.Path)
	}
}

func assertFCMGeneratedMarkers(
	t *testing.T,
	file fcmInventoryFile,
	contents []byte,
	generator fcmProtobufGenerator,
) {
	t.Helper()

	markers := []string{
		"// Code generated by protoc-gen-go. DO NOT EDIT.",
		"// \tprotoc-gen-go " + generator.ProtocGenGo,
		"// \tprotoc        v" + generator.Protoc,
		"// source: " + file.Source,
	}

	for _, marker := range markers {
		if !strings.Contains(string(contents), marker) {
			t.Errorf("generated file %s is missing %q", file.Path, marker)
		}
	}
}

func readRepositoryFile(t *testing.T, root, relative string) ([]byte, error) {
	t.Helper()

	path := filepath.Join(root, relative)

	resolved, err := filepath.Rel(root, path)
	if err != nil {
		return nil, ringerrors.NewBadRequestError("resolve repository file path", err)
	}

	if resolved == ".." || strings.HasPrefix(resolved, ".."+string(filepath.Separator)) {
		return nil, fs.ErrInvalid
	}

	// #nosec G304 -- paths are fixed inventory files or validated as repository-relative above.
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		return nil, ringerrors.NewInternalServerError("read repository file "+relative, readErr)
	}

	return data, nil
}

func assertFCMModulePackaging(t *testing.T, root string) {
	t.Helper()

	moduleFiles := []string{"go.mod", filepath.Join("cmd", "go-ring", "go.mod")}

	for _, moduleFile := range moduleFiles {
		moduleData, err := readRepositoryFile(t, root, moduleFile)
		if err != nil {
			t.Fatal(err)
		}

		if strings.Contains(string(moduleData), "github.com/crow-misia/go-push-receiver") {
			t.Errorf("%s still depends on the upstream push receiver module", moduleFile)
		}
	}

	_, err := os.Stat(filepath.Join(root, "third_party", "go-push-receiver", "go.mod"))
	if err == nil {
		t.Error("checked-in push receiver is a nested module; consumers cannot inherit its replacement")
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
}

func jsonStatus(status int) string {
	return strconv.Itoa(status)
}

func assertFCMHeaderParameters(
	t *testing.T,
	spec openAPIDocument,
	operation *openapi3.Operation,
	wanted map[string]string,
) {
	t.Helper()

	actualNames := make([]string, 0, len(operation.Parameters))

	for _, parameterRef := range operation.Parameters {
		if parameterRef == nil || parameterRef.Value == nil {
			t.Errorf("%s has an unresolved header parameter", operation.OperationID)

			continue
		}

		parameter := parameterRef.Value
		if parameter.In != "header" {
			t.Errorf("%s parameter %q is in %q, want header", operation.OperationID, parameter.Name, parameter.In)

			continue
		}

		actualNames = append(actualNames, parameter.Name)

		expectedValue, exists := wanted[parameter.Name]
		if !exists {
			t.Errorf("%s has unexpected header parameter %q", operation.OperationID, parameter.Name)

			continue
		}

		if !parameter.Required {
			t.Errorf("%s header %q is not required", operation.OperationID, parameter.Name)
		}

		validationErr := spec.validate(t, parameter.Schema, expectedValue)
		if validationErr != nil {
			t.Errorf("%s header %q schema rejected %q: %v", operation.OperationID, parameter.Name, expectedValue, validationErr)
		}
	}

	wantedNames := make([]string, 0, len(wanted))
	for name := range wanted {
		wantedNames = append(wantedNames, name)
	}

	sort.Strings(actualNames)
	sort.Strings(wantedNames)

	if !reflect.DeepEqual(actualNames, wantedNames) {
		t.Errorf("%s header parameters = %v, want exactly %v", operation.OperationID, actualNames, wantedNames)
	}
}

func assertFCMOperationServer(t *testing.T, operation *openapi3.Operation, expected string) {
	t.Helper()

	if operation.Servers == nil {
		t.Errorf("%s has no operation-level server", operation.OperationID)

		return
	}

	actual := make([]string, 0, len(*operation.Servers))

	for _, server := range *operation.Servers {
		if server != nil {
			actual = append(actual, server.URL)
		}
	}

	sort.Strings(actual)

	if !reflect.DeepEqual(actual, []string{expected}) {
		t.Errorf("%s servers = %v, want exactly %q", operation.OperationID, actual, expected)
	}
}

func assertFCMOperationMedia(
	t *testing.T,
	operation *openapi3.Operation,
	requestContentType, responseContentType string,
	responseStatuses ...int,
) {
	t.Helper()

	if fcmRequestSchema(operation, requestContentType) == nil {
		t.Errorf("%s has no resolved request body", operation.OperationID)
	} else if fcmRequestSchema(operation, requestContentType).Value == nil {
		t.Errorf("%s has no modeled %s request body", operation.OperationID, requestContentType)
	}

	for _, status := range responseStatuses {
		if fcmResponseSchema(operation, status, responseContentType) == nil {
			t.Errorf("%s has no %d response", operation.OperationID, status)
		} else if fcmResponseSchema(operation, status, responseContentType).Value == nil {
			t.Errorf("%s has no modeled %s response for %d", operation.OperationID, responseContentType, status)
		}
	}
}

func fcmRequestSchema(operation *openapi3.Operation, contentType string) *openapi3.SchemaRef {
	if operation == nil || operation.RequestBody == nil || operation.RequestBody.Value == nil {
		return nil
	}

	media := operation.RequestBody.Value.Content[contentType]
	if media == nil {
		return nil
	}

	return media.Schema
}

func fcmResponseSchema(operation *openapi3.Operation, status int, contentType string) *openapi3.SchemaRef {
	if operation == nil || operation.Responses == nil {
		return nil
	}

	responseRef := operation.Responses.Value(jsonStatus(status))
	if responseRef == nil || responseRef.Value == nil {
		return nil
	}

	media := responseRef.Value.Content[contentType]
	if media == nil {
		return nil
	}

	return media.Schema
}
