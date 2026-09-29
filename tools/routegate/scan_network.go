package routegate

import (
	"fmt"
	"go/ast"
	"path/filepath"
	"strings"
)

const (
	httpImportPath                          = "net/http"
	websocketImportPath                     = "github.com/gorilla/websocket"
	httpRoundTripMethodName                 = "RoundTrip"
	generatedJSONHelperName                 = "doGeneratedJSON"
	doRequestHelperName                     = "doRequest"
	sendAuthorizedHelperName                = "sendAuthorizedRequest"
	sendWithRetryHelperName                 = "sendWithRetry"
	sendTypedHelperName                     = "sendTyped"
	dialSignalingHelperName                 = "DialSignaling"
	openEventsHelperName                    = "OpenEvents"
	openEventsWithDialerHelperName          = "OpenEventsWithDialer"
	writeSignalingHelperName                = "WriteSignaling"
	validateWebSocketURLName                = "ValidateWebSocketURL"
	validateWebSocketOverrideName           = "ValidateWebSocketOverride"
	generatedClientConstructorName          = "NewClient"
	generatedClientResponsesConstructorName = "NewClientWithResponses"
	websocketDialContextMethodName          = "DialContext"
	websocketWriteMessageName               = "WriteMessage"
)

// auditNetworkCall checks direct network primitives without classifying
// sync.Once.Do(func()) as an HTTP request send.
func auditNetworkCall(
	call *ast.CallExpr,
	selector *ast.SelectorExpr,
	imports map[string]string,
	verifiedDoCalls map[*ast.CallExpr]bool,
	verifiedFCMCalls map[*ast.CallExpr]bool,
	parents map[ast.Node]ast.Node,
	packageFiles []*parsedGoFile,
	verifiedSignalWriter bool,
	add func(ast.Node, string, string),
) {
	name := selector.Sel.Name
	packagePath, imported, shadowed := selectorImport(selector, imports)

	auditHTTPConstructor(call, selector, name, packagePath, imported, shadowed, verifiedFCMCalls, add)
	auditHTTPRequestHelper(call, selector, name, packagePath, imported, shadowed, add)
	auditHTTPClientSend(call, name, imports, verifiedDoCalls, verifiedFCMCalls, add)
	auditHTTPTransportSend(call, name, imports, verifiedFCMCalls, add)
	auditWebSocketNetworkCall(
		call,
		selector,
		name,
		packagePath,
		imported,
		shadowed,
		imports,
		parents,
		packageFiles,
		verifiedSignalWriter,
		add,
	)
	auditRawNetworkDial(call, name, packagePath, imported, shadowed, imports, verifiedFCMCalls, add)
}

func auditHTTPConstructor(
	call *ast.CallExpr,
	selector *ast.SelectorExpr,
	name, packagePath string,
	imported, shadowed bool,
	verifiedFCMCalls map[*ast.CallExpr]bool,
	add func(ast.Node, string, string),
) {
	if !isHTTPConstructor(name) || !imported {
		return
	}

	if verifiedFCMCalls[call] {
		return
	}

	switch {
	case shadowed:
		add(
			selector,
			"shadowed-http-qualifier",
			"HTTP request constructor qualifier is shadowed; resolve it to the net/http import",
		)
	case packagePath != httpImportPath:
		message := fmt.Sprintf("%s.%s comes from %q, not net/http", selectorName(selector.X), name, packagePath)
		add(selector, "counterfeit-http-qualifier", message)
	default:
		message := "constructs an HTTP request outside a generated OpenAPI operation; " +
			"bind method, path, query, and headers to the schema"
		add(
			call,
			"handwritten-http-route",
			message,
		)
	}
}

func auditHTTPRequestHelper(
	call *ast.CallExpr,
	selector *ast.SelectorExpr,
	name, packagePath string,
	imported, shadowed bool,
	add func(ast.Node, string, string),
) {
	if !isHTTPRequestHelper(name) || !imported {
		return
	}

	if packagePath == httpImportPath && !shadowed {
		add(
			call,
			"handwritten-http-route",
			"uses a package-level net/http request helper outside a generated OpenAPI operation",
		)

		return
	}

	if shadowed || packagePath != httpImportPath {
		message := fmt.Sprintf("%s.%s does not resolve to the net/http import", selectorName(selector.X), name)
		add(call, "counterfeit-http-qualifier", message)
	}
}

func isHTTPRequestHelper(name string) bool {
	switch name {
	case "Get", "Head", "Post", "PostForm":
		return true
	default:
		return false
	}
}

func auditHTTPClientSend(
	call *ast.CallExpr,
	name string,
	imports map[string]string,
	verifiedDoCalls map[*ast.CallExpr]bool,
	verifiedFCMCalls map[*ast.CallExpr]bool,
	add func(ast.Node, string, string),
) {
	if name != "Do" || !takesRequestValue(call.Args) || !hasImportPath(imports, httpImportPath) ||
		verifiedDoCalls[call] || verifiedFCMCalls[call] {
		return
	}

	add(
		call,
		"handwritten-http-send",
		"sends an HTTP request through Do; route the request through a generated OpenAPI operation",
	)
}

func auditHTTPTransportSend(
	call *ast.CallExpr,
	name string,
	imports map[string]string,
	verifiedFCMCalls map[*ast.CallExpr]bool,
	add func(ast.Node, string, string),
) {
	if name != httpRoundTripMethodName || !takesRequestValue(call.Args) || !hasImportPath(imports, httpImportPath) {
		return
	}

	if verifiedFCMCalls[call] {
		return
	}

	add(
		call,
		"handwritten-http-send",
		"sends or forwards an HTTP request through RoundTrip; inventory and schema-bind this transport edge",
	)
}

func auditWebSocketNetworkCall(
	call *ast.CallExpr,
	selector *ast.SelectorExpr,
	name, packagePath string,
	imported, shadowed bool,
	imports map[string]string,
	parents map[ast.Node]ast.Node,
	packageFiles []*parsedGoFile,
	verifiedSignalWriter bool,
	add func(ast.Node, string, string),
) {
	if isWebSocketDialMethod(name) {
		auditWebSocketDialCall(
			call,
			selector,
			name,
			packagePath,
			imported,
			shadowed,
			imports,
			parents,
			packageFiles,
			add,
		)
	}

	if isWebSocketWriteMethod(name) {
		auditWebSocketWriteCall(
			call,
			selector,
			name,
			packagePath,
			imported,
			shadowed,
			parents,
			verifiedSignalWriter,
			add,
		)
	}
}

func auditWebSocketDialCall(
	call *ast.CallExpr,
	selector *ast.SelectorExpr,
	name, packagePath string,
	imported, shadowed bool,
	imports map[string]string,
	parents map[ast.Node]ast.Node,
	packageFiles []*parsedGoFile,
	add func(ast.Node, string, string),
) {
	if imported && (shadowed || packagePath != websocketImportPath) {
		message := fmt.Sprintf("%s.%s does not resolve to %s", selectorName(selector.X), name, websocketImportPath)
		add(call, "counterfeit-websocket-qualifier", message)

		return
	}

	websocketImported := imported && packagePath == websocketImportPath || hasImportPath(imports, websocketImportPath)
	if websocketImported && !verifiedWebSocketDial(call, parents, packageFiles, imports) {
		add(
			call,
			"unbound-websocket-dial",
			"opens a WebSocket transport without a statically checked AsyncAPI channel and configured server",
		)
	}
}

func auditWebSocketWriteCall(
	call *ast.CallExpr,
	selector *ast.SelectorExpr,
	name, packagePath string,
	imported, shadowed bool,
	parents map[ast.Node]ast.Node,
	verifiedSignalWriter bool,
	add func(ast.Node, string, string),
) {
	if imported && (shadowed || packagePath != websocketImportPath) {
		message := fmt.Sprintf("%s.%s does not resolve to %s", selectorName(selector.X), name, websocketImportPath)
		add(call, "counterfeit-websocket-qualifier", message)

		return
	}

	if verifiedSignalWriteCall(call, parents, verifiedSignalWriter) {
		return
	}

	add(call, "unbound-websocket-write", "writes a raw WebSocket frame outside a schema-checked signaling operation")
}

func auditGenericHTTPHelper(
	call *ast.CallExpr,
	selector *ast.SelectorExpr,
	parents map[ast.Node]ast.Node,
	add func(ast.Node, string, string),
) {
	switch selector.Sel.Name {
	case doRequestHelperName, "doJSONRequest":
		add(
			call,
			"unbound-generic-http-helper",
			"generic HTTP helper call has dynamic method/path and is not bound to one generated OpenAPI operation",
		)
	case sendAuthorizedHelperName:
		caller := enclosingFunction(call, parents)
		if caller == nil || (caller.Name.Name != doRequestHelperName && caller.Name.Name != generatedJSONHelperName) {
			add(
				call,
				"unverified-http-helper-edge",
				"request enters the shared HTTP transport outside the inventoried generated or generic request helper",
			)
		}
	case sendWithRetryHelperName:
		caller := enclosingFunction(call, parents)
		if caller == nil || caller.Name.Name != sendAuthorizedHelperName {
			add(
				call,
				"unverified-http-helper-edge",
				"request enters the retry transport outside the inventoried authorization helper",
			)
		}
	default:
		return
	}
}

func enclosingFunction(node ast.Node, parents map[ast.Node]ast.Node) *ast.FuncDecl {
	for parent := parents[node]; parent != nil; parent = parents[parent] {
		if function, ok := parent.(*ast.FuncDecl); ok {
			return function
		}
	}

	return nil
}

func auditPrimitiveMethodValue(
	selector *ast.SelectorExpr,
	imports map[string]string,
	add func(ast.Node, string, string),
) {
	name := selector.Sel.Name
	packagePath, imported, shadowed := selectorImport(selector, imports)

	if isHTTPConstructor(name) && imported {
		switch {
		case shadowed:
			add(selector, "shadowed-http-qualifier", "HTTP request constructor is aliased through a shadowing local")
		case packagePath != httpImportPath:
			message := fmt.Sprintf(
				"%s.%s is imported from %q, not net/http",
				selectorName(selector.X),
				name,
				packagePath,
			)
			add(selector, "counterfeit-http-qualifier", message)
		default:
			add(
				selector,
				"network-primitive-alias",
				"net/http request constructor is stored as a value; generated operations must own request construction",
			)
		}
	}

	if isWebSocketDialMethod(name) || isWebSocketWriteMethod(name) {
		switch {
		case imported && (shadowed || packagePath != websocketImportPath):
			message := fmt.Sprintf("%s.%s does not resolve to %s", selectorName(selector.X), name, websocketImportPath)
			add(selector, "counterfeit-websocket-qualifier", message)
		case imported && packagePath == websocketImportPath ||
			hasImportPath(imports, websocketImportPath) ||
			isWebSocketWriteMethod(name):
			add(
				selector,
				"network-primitive-alias",
				fmt.Sprintf("WebSocket method %s is stored as a value outside a checked channel boundary", name),
			)
		}
	}

	auditHTTPMethodValue(selector, imports, name, packagePath, imported, add)
}

func auditHTTPMethodValue(
	selector *ast.SelectorExpr,
	imports map[string]string,
	name, packagePath string,
	imported bool,
	add func(ast.Node, string, string),
) {
	if name == "Do" || name == httpRoundTripMethodName || name == "Dial" || name == websocketDialContextMethodName {
		if imported && (packagePath == httpImportPath || packagePath == "net") ||
			!imported && (name == "Do" || name == httpRoundTripMethodName) &&
				hasImportPath(imports, httpImportPath) {
			add(
				selector,
				"network-primitive-alias",
				fmt.Sprintf("network method %s is stored as a value and bypasses callsite route checks", name),
			)
		}
	}

	if name == sendWithRetryHelperName {
		add(selector, "network-primitive-alias",
			"request transport helper is stored as a value and bypasses generated request callsite checks")
	}
}

func auditGeneratedOperationCall(
	call *ast.CallExpr,
	selector *ast.SelectorExpr,
	operationNames map[string]map[string]HTTPRoute,
	generatedClients map[*ast.Object]string,
	generatedCandidates map[*ast.Object]string,
	imports map[string]string,
	add func(ast.Node, string, string),
) {
	paths := generatedOperationPaths(selector.Sel.Name, operationNames)
	if len(paths) == 0 {
		return
	}

	identifier, ok := selector.X.(*ast.Ident)
	if !ok {
		return
	}

	if identifier.Obj == nil {
		packagePath, imported := imports[identifier.Name]
		if imported {
			add(
				call,
				"counterfeit-generated-operation",
				fmt.Sprintf(
					"%s.%s is not a method call on a verified generated HTTP client (import path %q)",
					identifier.Name,
					selector.Sel.Name,
					packagePath,
				),
			)
		}

		return
	}

	candidatePath, candidate := generatedCandidates[identifier.Obj]
	if !candidate {
		return
	}

	if generatedClients[identifier.Obj] != candidatePath || !contains(paths, candidatePath) {
		add(
			call,
			"unverified-generated-client",
			fmt.Sprintf(
				"receiver for generated operation %s is reassigned, shadowed, or constructed from another package",
				selector.Sel.Name,
			),
		)
	}
}

func auditGeneratedOperationValue(
	selector *ast.SelectorExpr,
	operationNames map[string]map[string]HTTPRoute,
	generatedClients map[*ast.Object]string,
	generatedCandidates map[*ast.Object]string,
	imports map[string]string,
	add func(ast.Node, string, string),
) {
	paths := generatedOperationPaths(selector.Sel.Name, operationNames)
	if len(paths) == 0 {
		return
	}

	identifier, ok := selector.X.(*ast.Ident)
	if !ok {
		return
	}

	if identifier.Obj == nil {
		packagePath, imported := imports[identifier.Name]
		if imported {
			add(
				selector,
				"counterfeit-generated-operation",
				fmt.Sprintf(
					"%s.%s method value comes from import %q, not a generated client receiver",
					identifier.Name,
					selector.Sel.Name,
					packagePath,
				),
			)
		}

		return
	}

	if candidatePath, candidate := generatedCandidates[identifier.Obj]; candidate &&
		(generatedClients[identifier.Obj] != candidatePath || !contains(paths, candidatePath)) {
		add(
			selector,
			"unverified-generated-client",
			fmt.Sprintf(
				"generated OpenAPI method value %s is not bound to a proven generated client object",
				selector.Sel.Name,
			),
		)
	}
}

func auditGeneratedConstructorCall(
	call *ast.CallExpr,
	selector *ast.SelectorExpr,
	contracts Contracts,
	imports map[string]string,
	add func(ast.Node, string, string),
) {
	if selector.Sel.Name != generatedClientConstructorName &&
		selector.Sel.Name != generatedClientResponsesConstructorName {
		return
	}

	identifier, ok := selector.X.(*ast.Ident)
	if !ok {
		return
	}

	packagePath, imported := imports[identifier.Name]
	if !imported {
		return
	}

	if identifier.Obj != nil {
		add(
			call,
			"shadowed-generated-http-qualifier",
			identifier.Name+" constructor qualifier is shadowed; resolve it to the discovered generated HTTP package",
		)

		return
	}

	if hasGeneratedHTTPPath(contracts, packagePath) {
		return
	}

	for generatedPath := range contracts.GeneratedHTTPPaths {
		if filepath.Base(generatedPath) == identifier.Name {
			add(
				call,
				"counterfeit-generated-http-import",
				fmt.Sprintf(
					"%s is imported from %q instead of discovered generated package %q",
					identifier.Name,
					packagePath,
					generatedPath,
				),
			)

			return
		}
	}
}

func auditGeneratedRequestCall(
	call *ast.CallExpr,
	selector *ast.SelectorExpr,
	contracts Contracts,
	imports map[string]string,
	add func(ast.Node, string, string),
) {
	paths := generatedOperationPaths(selector.Sel.Name, contracts.GeneratedHTTPRequests)
	if len(paths) == 0 {
		return
	}

	identifier, ok := selector.X.(*ast.Ident)
	if !ok {
		add(
			call,
			"unverified-generated-request-builder",
			fmt.Sprintf(
				"request builder %s is not package-qualified from the discovered generated package",
				selector.Sel.Name,
			),
		)

		return
	}

	packagePath, imported := imports[identifier.Name]
	if identifier.Obj != nil || !imported || !contains(paths, packagePath) {
		add(
			call,
			"unverified-generated-request-builder",
			fmt.Sprintf("request builder %s does not resolve to its discovered generated package", selector.Sel.Name),
		)
	}
}

func auditGeneratedRequestValue(
	selector *ast.SelectorExpr,
	contracts Contracts,
	imports map[string]string,
	parents map[ast.Node]ast.Node,
	add func(ast.Node, string, string),
) {
	paths := generatedOperationPaths(selector.Sel.Name, contracts.GeneratedHTTPRequests)
	if len(paths) == 0 {
		return
	}

	if _, directCall := parents[selector].(*ast.CallExpr); directCall {
		return
	}

	add(
		selector,
		"generated-request-builder-alias",
		fmt.Sprintf(
			"generated request builder %s is stored as a function value and bypasses direct operation checks",
			selector.Sel.Name,
		),
	)

	_ = imports
}

func auditSignalingMethodValue(
	selector *ast.SelectorExpr,
	imports map[string]string,
	parents map[ast.Node]ast.Node,
	add func(ast.Node, string, string),
) {
	if _, directCall := parents[selector].(*ast.CallExpr); directCall {
		return
	}

	switch selector.Sel.Name {
	case sendTypedHelperName:
		add(
			selector,
			"signaling-helper-alias",
			"generic signaling helper is stored as a method value outside schema-pair checks",
		)
	case "Send", "send":
		if hasImportPath(imports, protocolImportPath()) ||
			hasImportPath(imports, "github.com/portpowered/go-ring/internal/signaling") {
			add(
				selector,
				"signaling-helper-alias",
				fmt.Sprintf(
					"generic signaling method %s is stored as a value outside method/body checks",
					selector.Sel.Name,
				),
			)
		}
	}
}

func auditWebSocketHelperCall(
	call *ast.CallExpr,
	selector *ast.SelectorExpr,
	imports map[string]string,
	contracts Contracts,
	parents map[ast.Node]ast.Node,
	adapterVerified bool,
	add func(ast.Node, string, string),
) {
	packagePath, imported, shadowed := selectorImport(selector, imports)
	if !imported || !strings.HasSuffix(packagePath, "/pkg/dependencies/websocket") {
		return
	}

	if shadowed {
		add(
			call,
			"shadowed-websocket-helper",
			selectorName(selector.X)+" resolves to a shadowing local, not the inventoried WebSocket package",
		)

		return
	}

	switch selector.Sel.Name {
	case dialSignalingHelperName:
		if channelAddress(contracts, "/ws") == "" {
			add(call, "missing-async-channel", "signaling WebSocket dial uses /ws, which is absent from AsyncAPI")

			return
		}

		if !verifiedWebSocketCallsite(call, selector.Sel.Name, "serverEnvelope", imports, parents) {
			add(
				call,
				"unverified-websocket-url",
				"signaling dial URL is not checked against the AsyncAPI default or the explicit override URL policy",
			)
		}
	case openEventsWithDialerHelperName, openEventsHelperName:
		if channelAddress(contracts, "/clients_api/ws") == "" {
			add(
				call,
				"missing-async-channel",
				"event WebSocket dial uses /clients_api/ws, which is absent from AsyncAPI",
			)

			return
		}

		if !verifiedWebSocketCallsite(call, selector.Sel.Name, "accountEvent", imports, parents) {
			add(
				call,
				"unverified-websocket-url",
				"event dial URL is not checked against the AsyncAPI default or the explicit override URL policy",
			)
		}
	case writeSignalingHelperName:
		if !adapterVerified {
			add(
				call,
				"handwritten-signaling-envelope",
				"WebSocket boundary serializes internal/signaling.Message without a verified generated AsyncAPI adapter",
			)
		}
	}
}
