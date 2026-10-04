package routegate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fcmContractFixture = `openapi: 3.1.0
paths:
  /checkin:
    post: {operationId: checkInFCMClient, servers: [{url: https://android.clients.google.com}]}
  /c2dm/register3:
    post: {operationId: registerFCMClient, servers: [{url: https://android.clients.google.com}]}
  /v1/projects/ring-17770/installations:
    post: {operationId: createFCMInstallation, servers: [{url: https://firebaseinstallations.googleapis.com}]}
  /v1/projects/ring-17770/registrations:
    post: {operationId: registerFCMInstallation, servers: [{url: https://fcmregistrations.googleapis.com}]}
`

const fcmTransportFixture = `package push

import (
	"net/url"
	"net/http"
	"strings"
	protocol "github.com/portpowered/go-ring/internal/protocol"
)

type diagnosticTransport struct { next http.RoundTripper }

func (t *diagnosticTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	route, err := matchFCMRoute(request)
	if err != nil { return nil, err }
	err = validateFCMHeaders(route, request)
	if err != nil { return nil, err }
	if route == routeRegistrations { err = omitDefaultVAPID(request) }
	body, err := readAndRestoreBody(request)
	if err != nil { return nil, err }
	err = validateFCMRequestBody(route, body)
	if err != nil { return nil, err }
	response, err := t.next.RoundTrip(request)
	return response, err
}

func matchFCMRoute(request *http.Request) (string, error) {
	if request.URL.Scheme != "https" { return "", NewNetworkError() }
	if request.Method != http.MethodPost { return "", NewNetworkError() }
	switch {
	case request.URL.Host == protocol.FCMCheckinHost && request.URL.EscapedPath() == protocol.FCMCheckinPath:
		return routeCheckin, nil
	case request.URL.Host == protocol.FCMRegisterHost && request.URL.EscapedPath() == protocol.FCMRegisterPath:
		return routeRegister, nil
	case request.URL.Host == protocol.FCMInstallationsHost && request.URL.EscapedPath() == protocol.FCMInstallationsPath:
		return routeInstallations, nil
	case request.URL.Host == protocol.FCMRegistrationsHost && request.URL.EscapedPath() == protocol.FCMRegistrationsPath:
		return routeRegistrations, nil
	default:
		return "", NewNetworkError()
	}
}

func validateFCMHeaders(route string, request *http.Request) error {
	var expected http.Header
	switch route {
	case routeCheckin:
		expected = http.Header{protocol.FCMContentTypeHeader: {protocol.FCMContentTypeProtobuf}}
	case routeRegister:
		if !decimal(request.Header.Get("Authorization")) {
			return invalidFCMHeaderError()
		}
		expected = http.Header{
			protocol.FCMContentTypeHeader: {protocol.FCMContentTypeForm},
			"Authorization":               {request.Header.Get("Authorization")},
		}
	case routeInstallations:
		expected = http.Header{
			protocol.FCMContentTypeHeader:         {protocol.FCMContentTypeJSON},
			protocol.FCMAcceptHeader:              {protocol.FCMContentTypeJSON},
			protocol.FCMInstallationsAPIKeyHeader: {apiKey},
		}
	case routeRegistrations:
		if strings.TrimSpace(request.Header.Get(protocol.FCMInstallationsAuthHeader)) == "" {
			return invalidFCMHeaderError()
		}
		expected = http.Header{
			protocol.FCMContentTypeHeader:         {protocol.FCMContentTypeJSON},
			protocol.FCMInstallationsAPIKeyHeader: {apiKey},
			protocol.FCMInstallationsAuthHeader: {
				request.Header.Get(protocol.FCMInstallationsAuthHeader),
			},
		}
	default:
		return invalidFCMHeaderError()
	}
	if !sameHeaderSet(expected, request.Header) { return invalidFCMHeaderError() }
	return nil
}

func validateFCMRequestBody(route string, body []byte) error {
	switch route {
	case routeCheckin: return validateCheckinRequest(body)
	case routeRegister: return validateRegisterRequest(body)
	case routeInstallations: return validateInstallationsRequest(body)
	case routeRegistrations: return validateRegistrationsRequest(body)
	default: return NewNetworkError()
	}
}

func omitDefaultVAPID(request *http.Request) error { return nil }
func readAndRestoreBody(request *http.Request) ([]byte, error) { return nil, nil }
func NewNetworkError() error { return nil }
func invalidFCMHeaderError() error { return nil }
func decimal(string) bool { return false }
func sameHeaderSet(expected, actual http.Header) bool {
	if len(expected) != len(actual) { return false }
	for name, values := range expected {
		actualValues := actual.Values(name)
		if len(values) != len(actualValues) { return false }
		for index, value := range values {
			if actualValues[index] != value { return false }
		}
	}
	return true
}
func validateCheckinRequest(body []byte) error {
	if !exactProtoFields(body) || body.GetVersion() != 3 { return NewBadRequestError() }
	if body.GetUnknown() { return NewBadRequestError() }
	return nil
}
func validateRegisterRequest(body []byte) error {
	values, err := url.ParseQuery(string(body))
	if err != nil { return NewBadRequestError() }
	if !oneValueIs(values) || !decimal(values.Get("device")) { return NewBadRequestError() }
	return nil
}
func validateInstallationsRequest(body []byte) error {
	fields, err := jsonObject(body)
	if err != nil { return NewBadRequestError() }
	if !exactJSONKeys(fields) || !jsonStringIs(fields) || !validFID(fields) { return NewBadRequestError() }
	return nil
}
func validateRegistrationsRequest(body []byte) error {
	fields, err := jsonObject(body)
	if err != nil { return NewBadRequestError() }
		if !exactJSONKeys(fields) || !validWebPushKey(fields) ||
			!strings.HasPrefix("endpoint", protocol.FCMRegistrationEndpointPrefix) {
			return NewBadRequestError()
		}
	return nil
}
func exactProtoFields(any) bool { return false }
func oneValueIs(any) bool { return false }
func jsonObject(any) (any, error) { return nil, nil }
func exactJSONKeys(any) bool { return false }
func jsonStringIs(any) bool { return false }
func validFID(any) bool { return false }
func validWebPushKey(any) bool { return false }
func NewBadRequestError() error { return nil }

const (
	routeCheckin = "checkin"
	routeRegister = "register"
	routeInstallations = "installations"
	routeRegistrations = "registrations"
)
`

const fcmInstanceCallsiteFixture = `package pushreceiver

import (
	"context"
	generatedfcm "github.com/portpowered/go-ring/internal/generatedfcm"
	"github.com/portpowered/go-ring/internal/protocol"
)

type Client struct{}
func (c *Client) checkIn(ctx context.Context) {
	request, err := generatedfcm.NewCheckInFCMClientRequestWithBody("https://"+protocol.FCMCheckinHost,
		&generatedfcm.CheckInFCMClientParams{ContentType: generatedfcm.CheckInFCMClientParamsContentTypeApplicationxProtobuf},
		string(generatedfcm.CheckInFCMClientParamsContentTypeApplicationxProtobuf), nil)
	if err != nil { return }
	_, err = c.post(ctx, request)
	_ = err
}
func (c *Client) doRegister(ctx context.Context) {
	request, err := generatedfcm.NewRegisterFCMClientRequestWithFormdataBody("https://"+protocol.FCMRegisterHost,
		nil, generatedfcm.LegacyRegistrationRequest{})
	if err != nil { return }
	_, err = c.post(ctx, request)
	_ = err
}
`

const fcmCallsiteFixture = `package pushreceiver

import (
	"context"
	generatedfcm "github.com/portpowered/go-ring/internal/generatedfcm"
	"github.com/portpowered/go-ring/internal/protocol"
)

type Client struct{}
func (c *Client) installFCM(ctx context.Context) {
	request, err := generatedfcm.NewCreateFCMInstallationRequest("https://"+protocol.FCMInstallationsHost,
		nil, generatedfcm.InstallationRequest{})
	if err != nil { return }
	_, err = c.post(ctx, request)
	_ = err
}
func (c *Client) registerFCM(ctx context.Context) {
	request, err := generatedfcm.NewRegisterFCMInstallationRequest("https://"+protocol.FCMRegistrationsHost,
		nil, generatedfcm.FCMRegistrationRequest{})
	if err != nil { return }
	_, err = c.post(ctx, request)
	_ = err
}
`

func TestExternalFCMContractRejectsUnmodeledRoute(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	path := filepath.Join(root, "api", "external", "fcm.openapi.yaml")

	err := os.MkdirAll(filepath.Dir(path), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(path, []byte(fcmContractFixture), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := loadFCMContract(root); !ok {
		t.Fatal("valid external FCM contract was rejected")
	}

	mutated := strings.Replace(fcmContractFixture, "/checkin:", "/unmodeled:", 1)

	err = os.WriteFile(path, []byte(mutated), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := loadFCMContract(root); ok {
		t.Fatal("unmodeled external route was accepted")
	}
}

func TestFCMTransportRejectsMissingValidationAndEarlyForward(t *testing.T) {
	t.Parallel()

	contract := fcmOpenAPI{Paths: map[string]map[string]fcmOpenAPIOperation{
		"/checkin": {}, "/c2dm/register3": {},
		"/v1/projects/ring-17770/installations": {},
		"/v1/projects/ring-17770/registrations": {},
	}}
	if _, ok := verifyFCMTransport(transportFixture(t, fcmTransportFixture), contract); !ok {
		t.Fatal("schema-checked FCM transport fixture was rejected")
	}

	missingValidation := strings.Replace(fcmTransportFixture,
		"err = validateFCMRequestBody(route, body)\n\tif err != nil { return nil, err }\n", "", 1)
	if _, ok := verifyFCMTransport(transportFixture(t, missingValidation), contract); ok {
		t.Fatal("FCM transport without body validation was accepted")
	}

	noOpHeaderComparison := strings.Replace(fcmTransportFixture,
		"if len(expected) != len(actual) { return false }",
		"if false { return false }", 1)
	if _, ok := verifyFCMTransport(transportFixture(t, noOpHeaderComparison), contract); ok {
		t.Fatal("FCM transport with a no-op header comparison was accepted")
	}

	noOpCheckinValidator := strings.Replace(fcmTransportFixture,
		"if !exactProtoFields(body) || body.GetVersion() != 3 { return NewBadRequestError() }",
		"if false { return NewBadRequestError() }", 1)
	if _, ok := verifyFCMTransport(transportFixture(t, noOpCheckinValidator), contract); ok {
		t.Fatal("FCM transport with a no-op schema validator was accepted")
	}

	earlyForward := strings.Replace(fcmTransportFixture,
		"\tbody, err := readAndRestoreBody(request)",
		"\tresponse, err := t.next.RoundTrip(request)\n\tbody, err := readAndRestoreBody(request)", 1)

	earlyForward = strings.Replace(earlyForward, "\tresponse, err := t.next.RoundTrip(request)\n\treturn response, err",
		"\treturn response, err", 1)

	if _, ok := verifyFCMTransport(transportFixture(t, earlyForward), contract); ok {
		t.Fatal("FCM transport that forwards before validation was accepted")
	}
}

func TestFCMCallsitesRejectDynamicRouteReplacement(t *testing.T) {
	t.Parallel()

	sources := callsiteFixture(t, fcmInstanceCallsiteFixture, fcmCallsiteFixture)

	contract := fcmOpenAPI{Paths: map[string]map[string]fcmOpenAPIOperation{
		"/checkin": {}, "/c2dm/register3": {},
		"/v1/projects/ring-17770/installations": {},
		"/v1/projects/ring-17770/registrations": {},
	}}

	if !verifyFCMCallSites(sources, contract) {
		t.Fatal("schema-bound FCM callsite fixture was rejected")
	}

	mutated := strings.Replace(fcmInstanceCallsiteFixture,
		"\"https://\"+protocol.FCMCheckinHost", "dynamicServer", 1)
	if verifyFCMCallSites(callsiteFixture(t, mutated, fcmCallsiteFixture), contract) {
		t.Fatal("dynamic FCM builder authority was accepted")
	}
}

func transportFixture(t *testing.T, source string) map[string]*parsedGoFile {
	t.Helper()

	return map[string]*parsedGoFile{
		"pkg/dependencies/push/http_transport.go": parseFCMSource(t, "http_transport.go", source),
	}
}

func callsiteFixture(t *testing.T, instanceID, fcm string) map[string]*parsedGoFile {
	t.Helper()

	return map[string]*parsedGoFile{
		"third_party/go-push-receiver/instanceid.go": parseFCMSource(t, "instanceid.go", instanceID),
		"third_party/go-push-receiver/fcm.go":        parseFCMSource(t, "fcm.go", fcm),
	}
}

func parseFCMSource(t *testing.T, name, source string) *parsedGoFile {
	t.Helper()

	fileSet := token.NewFileSet()

	file, err := parser.ParseFile(fileSet, name, source, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	return &parsedGoFile{path: name, fileSet: fileSet, file: file}
}

type mcsMutationCase struct {
	name string
	edit func(t *testing.T, root string)
	rule string
}

func TestMCSSourceGateRejectsContractMutations(t *testing.T) {
	t.Parallel()

	tests := []mcsMutationCase{
		{
			name: "inventory host",
			rule: "invalid-mcs-socket-contract",
			edit: func(t *testing.T, root string) {
				t.Helper()
				mutateMCSFile(
					t,
					root,
					"api/external/push-protocol-inventory.yaml",
					"host: mtalk.google.com",
					"host: other.example.test",
				)
			},
		},
		{
			name: "inventory TLS change",
			rule: "invalid-mcs-socket-contract",
			edit: func(t *testing.T, root string) {
				t.Helper()
				mutateMCSFile(t, root, "api/external/push-protocol-inventory.yaml", "tls: true", "tls: false")
			},
		},
		{
			name: "inventory callsite change",
			rule: "invalid-mcs-socket-contract",
			edit: func(t *testing.T, root string) {
				t.Helper()
				mutateMCSFile(
					t,
					root,
					"api/external/push-protocol-inventory.yaml",
					"fcm.go:tryToConnect",
					"fcm.go:otherFunction",
				)
			},
		},
		{
			name: "generated network constant",
			rule: "unbound-mcs-protocol-constants",
			edit: func(t *testing.T, root string) {
				t.Helper()
				mutateMCSFile(
					t,
					root,
					"internal/protocol/mcs.gen.go",
					`MCSNetwork                         = "tcp"`,
					`MCSNetwork                         = "udp"`,
				)
			},
		},
		{
			name: "injected dial uses unschematized address",
			rule: "unbound-mcs-dial-callsite",
			edit: func(t *testing.T, root string) {
				t.Helper()
				mutateMCSFile(
					t,
					root,
					"third_party/go-push-receiver/fcm.go",
					"protocol.MCSNetwork, protocol.MCSAddress",
					`"tcp", "mtalk.google.com:5228"`,
				)
			},
		},
		{
			name: "TLS dial uses unschematized address",
			rule: "unbound-mcs-dial-callsite",
			edit: func(t *testing.T, root string) {
				t.Helper()
				mutateMCSFile(
					t,
					root,
					"third_party/go-push-receiver/fcm.go",
					"dialer.DialContext(ctx, protocol.MCSNetwork, protocol.MCSAddress)",
					`dialer.DialContext(ctx, "tcp", "mtalk.google.com:5228")`,
				)
			},
		},
		{
			name: "tag alias mutation",
			rule: "unbound-mcs-tag-mapping",
			edit: func(t *testing.T, root string) {
				t.Helper()
				mutateMCSFile(t, root, "third_party/go-push-receiver/tag.go",
					"tagLoginRequest        tagType = protocol.MCSLoginRequestTag",
					"tagLoginRequest        tagType = protocol.MCSHeartbeatAckTag")
			},
		},
		{
			name: "active message mapping mutation",
			rule: "unbound-mcs-tag-mapping",
			edit: func(t *testing.T, root string) {
				t.Helper()
				mutateMCSFile(t, root, "third_party/go-push-receiver/tag.go",
					"return new(pb.LoginRequest)", "return new(pb.LoginResponse)")
			},
		},
		{
			name: "unlisted active proto message",
			rule: "invalid-mcs-socket-contract",
			edit: func(t *testing.T, root string) {
				t.Helper()
				mutateMCSFile(t, root, "api/external/push-protocol-inventory.yaml",
					"        - StreamErrorStanza", "        - StreamErrorStanza\n        - UnlistedActiveMessage")
			},
		},
	}

	assertMCSContractMutations(t, tests)
}

func assertMCSContractMutations(t *testing.T, tests []mcsMutationCase) {
	t.Helper()

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := mcsFixture(t)
			test.edit(t, root)

			findings, err := Audit(root)
			if err != nil {
				t.Fatal(err)
			}

			if !hasFindingRule(findings, test.rule) {
				t.Fatalf("expected %s finding, got: %+v", test.rule, findings)
			}
		})
	}
}

func TestMCSSourceGateRejectsExtraReceiverDial(t *testing.T) {
	t.Parallel()

	root := mcsFixture(t)
	writeMCSFile(t, root, "third_party/go-push-receiver/extra_dial.go", `package pushreceiver

import "crypto/tls"

func extraDial(dialer *tls.Dialer) {
	_, _ = dialer.DialContext(nil, "tcp", "other.example.test:5228")
}
`)

	findings, err := Audit(root)
	if err != nil {
		t.Fatal(err)
	}

	if !hasFindingRule(findings, "unlisted-mcs-network-dial") {
		t.Fatalf("expected extra dial finding, got: %+v", findings)
	}
}

func TestRawTLSDialMustBeContractBound(t *testing.T) {
	t.Parallel()

	parsed := parseFCMSource(t, "tls_dial.go", `package sample
import "crypto/tls"
func connect(dialer *tls.Dialer) {
	_, _ = dialer.DialContext(nil, "tcp", "other.example.test:5228")
}
`)

	var dial *ast.CallExpr

	ast.Inspect(parsed.file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == websocketDialContextMethodName {
			dial = call
		}

		return true
	})

	if dial == nil {
		t.Fatal("fixture has no TLS DialContext call")
	}

	var findings []string

	add := func(_ ast.Node, rule, _ string) { findings = append(findings, rule) }
	auditRawNetworkDial(dial, websocketDialContextMethodName, "", false, false, importAliases(parsed.file), nil, add)

	if !containsRule(findings, "unbound-network-dial") {
		t.Fatalf("expected raw TLS dial finding, got %v", findings)
	}

	verified := map[*ast.CallExpr]bool{dial: true}
	findings = nil

	auditRawNetworkDial(dial, websocketDialContextMethodName, "", false, false, importAliases(parsed.file), verified, add)

	if len(findings) != 0 {
		t.Fatalf("contract-bound TLS dial was rejected: %v", findings)
	}
}

func mcsFixture(t *testing.T) string {
	t.Helper()

	return mcsAuditFixtureRoot(t)
}

func mcsAuditFixtureRoot(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	writeMCSFile(t, root, "go.mod", "module example.com/routegatefixture\n\ngo 1.24.0\n")
	writeMCSFile(t, root, "api/openapi.yaml", `openapi: 3.1.0
info: {title: routegate fixture, version: 0.1.0}
servers: [{url: https://api.example.test}]
paths:
  /widgets/{widget_id}:
    get:
      operationId: getWidget
      parameters:
        - {name: widget_id, in: path, required: true, schema: {type: string}}
      responses: {'200': {description: ok}}
`)
	writeMCSFile(t, root, "api/asyncapi.yaml", `asyncapi: 2.6.0
info: {title: routegate fixture, version: 0.1.0}
servers: {}
channels: {}
operations: {}
components:
  messages: {}
  schemas: {}
`)
	writeMCSFile(t, root, "internal/protocol/endpoints.go", "package protocol\n")
	writeMCSFile(t, root, "generatedhttp/client.gen.go", `// Code generated by fixture. DO NOT EDIT.
package generatedhttp

import "net/http"

func NewGetWidgetRequest(server string, widgetID string) (*http.Request, error) {
	return http.NewRequest(http.MethodGet, server+"/widgets/"+widgetID, nil)
}

// Corresponds with GET /widgets/{widget_id} (the `+"`getWidget`"+` operationId)
func (c *Client) GetWidget() {}

type Client struct{}
`)
	writeMCSFile(t, root, "client.go", "package client\n")

	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	repositoryRoot := filepath.Clean(filepath.Join(workingDirectory, "..", ".."))

	for _, relative := range []string{
		"api/external/push-protocol-inventory.yaml",
		"internal/protocol/mcs.gen.go",
		"third_party/go-push-receiver/fcm.go",
		"third_party/go-push-receiver/constants.go",
		"third_party/go-push-receiver/tag.go",
	} {
		// #nosec G304 -- source paths are fixed below and remain inside this repository.
		contents, readErr := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(relative)))
		if readErr != nil {
			t.Fatal(readErr)
		}

		writeMCSFile(t, root, relative, string(contents))
	}

	return root
}

func mutateMCSFile(t *testing.T, root, relative, old, replacement string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(relative))
	// #nosec G304 -- callers pass fixed fixture paths beneath t.TempDir.
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	updated := strings.Replace(string(contents), old, replacement, 1)
	if updated == string(contents) {
		t.Fatalf("fixture mutation target %q was not found in %s", old, relative)
	}

	writeErr := os.WriteFile(path, []byte(updated), 0o600)
	if writeErr != nil {
		t.Fatal(writeErr)
	}
}

func writeMCSFile(t *testing.T, root, relative, contents string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(relative))

	mkdirErr := os.MkdirAll(filepath.Dir(path), 0o700)
	if mkdirErr != nil {
		t.Fatal(mkdirErr)
	}

	writeErr := os.WriteFile(path, []byte(contents), 0o600)
	if writeErr != nil {
		t.Fatal(writeErr)
	}
}

func hasFindingRule(findings []Finding, rule string) bool {
	for _, finding := range findings {
		if finding.Rule == rule {
			return true
		}
	}

	return false
}

func containsRule(rules []string, want string) bool {
	for _, rule := range rules {
		if rule == want {
			return true
		}
	}

	return false
}
