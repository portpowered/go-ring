package routegate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

type fcmRouteContract struct {
	operationID string
	origin      string
	path        string
	constant    string
	builder     string
	pathBuilder string
	contentType string
}

type fcmOpenAPI struct {
	Paths map[string]map[string]fcmOpenAPIOperation `yaml:"paths"`
}

type fcmOpenAPIOperation struct {
	OperationID string             `yaml:"operationId"`
	Servers     []fcmOpenAPIServer `yaml:"servers"`
}

type fcmOpenAPIServer struct {
	URL string `yaml:"url"`
}

func expectedFCMRoutes() map[string]fcmRouteContract {
	return map[string]fcmRouteContract{
		"/checkin": {
			operationID: "checkInFCMClient",
			origin:      "https://android.clients.google.com",
			path:        "/checkin",
			constant:    "FCMCheckin",
			builder:     "NewCheckInFCMClientRequestWithBody",
			pathBuilder: "NewCheckInFCMClientRequestWithBody",
			contentType: "application/x-protobuf",
		},
		"/c2dm/register3": {
			operationID: "registerFCMClient",
			origin:      "https://android.clients.google.com",
			path:        "/c2dm/register3",
			constant:    "FCMRegister",
			builder:     "NewRegisterFCMClientRequestWithFormdataBody",
			pathBuilder: "NewRegisterFCMClientRequestWithBody",
			contentType: "application/x-www-form-urlencoded",
		},
		"/v1/projects/ring-17770/installations": {
			operationID: "createFCMInstallation",
			origin:      "https://firebaseinstallations.googleapis.com",
			path:        "/v1/projects/ring-17770/installations",
			constant:    "FCMInstallations",
			builder:     "NewCreateFCMInstallationRequest",
			pathBuilder: "NewCreateFCMInstallationRequestWithBody",
			contentType: "application/json",
		},
		"/v1/projects/ring-17770/registrations": {
			operationID: "registerFCMInstallation",
			origin:      "https://fcmregistrations.googleapis.com",
			path:        "/v1/projects/ring-17770/registrations",
			constant:    "FCMRegistrations",
			builder:     "NewRegisterFCMInstallationRequest",
			pathBuilder: "NewRegisterFCMInstallationRequestWithBody",
			contentType: "application/json",
		},
	}
}

func auditExternalFCM(root string, parsed []*parsedGoFile) (map[*ast.CallExpr]bool, []Finding) {
	sources := parsedSourcesByRelativePath(root, parsed)
	if sources["third_party/go-push-receiver/client.go"] == nil {
		return nil, nil
	}

	findings := make([]Finding, 0)
	verifiedCalls := make(map[*ast.CallExpr]bool)

	contract, valid := loadFCMContract(root)
	if !valid {
		findings = append(findings, Finding{
			Path:    "api/external/fcm.openapi.yaml",
			Line:    0,
			Rule:    "invalid-fcm-contract",
			Message: "external FCM schema does not enumerate the four pinned HTTP operations and origins",
		})
	}

	constantsValid := valid && verifyFCMProtocolConstants(root, contract) &&
		verifyFCMDependencyConstants(sources, contract)
	if !constantsValid {
		findings = append(findings, Finding{
			Path:    "internal/protocol/fcm.go",
			Line:    0,
			Rule:    "unbound-fcm-route-constants",
			Message: "protocol and pinned dependency route constants do not match the checked-in external FCM schema",
		})
	}

	buildersValid := valid && verifyFCMGeneratedBuilders(root, contract)
	if !buildersValid {
		findings = append(findings, Finding{
			Path:    "internal/generatedfcm/client.gen.go",
			Line:    0,
			Rule:    "unbound-generated-fcm-operation",
			Message: "generated FCM request builders do not construct the exact schema operation paths and POST methods",
		})
	}

	callersValid := constantsValid && buildersValid && verifyFCMCallSites(sources, contract)
	if !callersValid {
		findings = append(findings, Finding{
			Path: "third_party/go-push-receiver",
			Line: 0,
			Rule: "unbound-fcm-callsite",
			Message: "pinned receiver callsites must use the exact generated FCM operation builders " +
				"and configured schema origins",
		})
	}

	requestHelper, requestCallsValid := verifyFCMRequestHelper(sources)
	if !requestCallsValid {
		findings = append(findings, Finding{
			Path:    "third_party/go-push-receiver/client.go",
			Line:    0,
			Rule:    "unverified-fcm-request-helper",
			Message: "FCM request helper must preserve the generated request and send it through its injected client",
		})
	}

	transport, transportValid := verifyFCMTransport(sources, contract)
	if !transportValid {
		findings = append(findings, Finding{
			Path:    "pkg/dependencies/push/http_transport.go",
			Line:    0,
			Rule:    "unverified-fcm-transport",
			Message: "FCM transport must match the schema routes and validate route, headers, and body before forwarding",
		})
	}

	injectionValid := verifyFCMTransportInjection(sources)
	if !injectionValid {
		findings = append(findings, Finding{
			Path:    "pkg/dependencies/push/receiver.go",
			Line:    0,
			Rule:    "unverified-fcm-transport-injection",
			Message: "the pinned receiver must receive the validating RoundTripper through its HTTP client option",
		})
	}

	if valid && constantsValid && buildersValid && callersValid && requestCallsValid && transportValid && injectionValid {
		for _, call := range requestHelper {
			verifiedCalls[call] = true
		}

		for _, call := range transport {
			verifiedCalls[call] = true
		}
	}

	return verifiedCalls, findings
}

func loadFCMContract(root string) (fcmOpenAPI, bool) {
	path := filepath.ToSlash(filepath.Join("api", "external", "fcm.openapi.yaml"))

	raw, err := fs.ReadFile(os.DirFS(root), path)
	if err != nil {
		return fcmOpenAPI{Paths: nil}, false
	}

	var document fcmOpenAPI
	if yaml.Unmarshal(raw, &document) != nil || len(document.Paths) != len(expectedFCMRoutes()) {
		return fcmOpenAPI{Paths: nil}, false
	}

	for routePath, expected := range expectedFCMRoutes() {
		methods, exists := document.Paths[routePath]
		if !exists || len(methods) != 1 {
			return fcmOpenAPI{Paths: nil}, false
		}

		operation, exists := methods["post"]
		if !exists || operation.OperationID != expected.operationID || len(operation.Servers) != 1 ||
			operation.Servers[0].URL != expected.origin {
			return fcmOpenAPI{Paths: nil}, false
		}

		parsedOrigin, err := url.Parse(operation.Servers[0].URL)
		if err != nil || parsedOrigin.Scheme != "https" || parsedOrigin.Host == "" ||
			parsedOrigin.Path != "" || parsedOrigin.RawQuery != "" || parsedOrigin.Fragment != "" {
			return fcmOpenAPI{Paths: nil}, false
		}
	}

	return document, true
}

func verifyFCMProtocolConstants(root string, contract fcmOpenAPI) bool {
	path := filepath.Join(root, "internal", "protocol", "fcm.go")

	values, ok := stringConstants(path)
	if !ok {
		return false
	}

	expected := map[string]string{
		"FCMCheckinHost":       "android.clients.google.com",
		"FCMCheckinPath":       "/checkin",
		"FCMRegisterHost":      "android.clients.google.com",
		"FCMRegisterPath":      "/c2dm/register3",
		"FCMInstallationsHost": "firebaseinstallations.googleapis.com",
		"FCMInstallationsPath": "/v1/projects/ring-17770/installations",
		"FCMRegistrationsHost": "fcmregistrations.googleapis.com",
		"FCMRegistrationsPath": "/v1/projects/ring-17770/registrations",
		"FCMProjectID":         "ring-17770",
	}

	for name, value := range expected {
		if values[name] != value {
			return false
		}
	}

	return len(contract.Paths) == len(expectedFCMRoutes())
}

func verifyFCMDependencyConstants(sources map[string]*parsedGoFile, contract fcmOpenAPI) bool {
	file := sources["third_party/go-push-receiver/constants.go"]
	if file == nil {
		return false
	}

	values := stringConstantsFromFile(file.file)

	for _, obsolete := range []string{
		"checkinURL",
		"registerURL",
		"firebaseInstallationURL",
		"firebaseRegistrationURL",
	} {
		if _, exists := values[obsolete]; exists {
			return false
		}
	}

	return len(contract.Paths) == len(expectedFCMRoutes())
}

func stringConstants(path string) (map[string]string, bool) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, false
	}

	return stringConstantsFromFile(file), true
}

func stringConstantsFromFile(file *ast.File) map[string]string {
	values := make(map[string]string)

	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}

		for _, specification := range general.Specs {
			value, ok := specification.(*ast.ValueSpec)
			if !ok || len(value.Values) != 1 || len(value.Names) == 0 {
				continue
			}

			literal, ok := value.Values[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}

			unquoted, err := strconv.Unquote(literal.Value)
			if err != nil {
				continue
			}

			for _, name := range value.Names {
				values[name.Name] = unquoted
			}
		}
	}

	return values
}

func parsedSourcesByRelativePath(root string, parsed []*parsedGoFile) map[string]*parsedGoFile {
	sources := make(map[string]*parsedGoFile, len(parsed))
	for _, file := range parsed {
		sources[relativePath(root, file.path)] = file
	}

	return sources
}
