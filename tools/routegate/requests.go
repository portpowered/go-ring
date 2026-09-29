package routegate

import (
	"go/ast"
	"go/token"
	"net/url"
	"strings"
)

const rawQueryFieldName = "RawQuery"

type generatedRequestAuditContext struct {
	root         string
	imports      map[string]string
	contracts    Contracts
	packageFiles []*parsedGoFile
	add          func(ast.Node, string, string)
}

type generatedRequestState struct {
	initializers map[*ast.Object]map[*ast.AssignStmt]bool
	routes       map[*ast.Object]HTTPRoute
	valid        map[*ast.Object]bool
}

func auditGeneratedRequests(
	root, path string,
	fileSet *token.FileSet,
	file *ast.File,
	imports map[string]string,
	contracts Contracts,
	packageFiles []*parsedGoFile,
) (map[*ast.CallExpr]bool, []Finding) {
	verified := make(map[*ast.CallExpr]bool)
	findings := []Finding{}
	add := func(node ast.Node, rule, message string) {
		findings = append(findings, Finding{
			Path:    relativePath(root, path),
			Line:    fileSet.Position(node.Pos()).Line,
			Rule:    rule,
			Message: message,
		})
	}
	context := generatedRequestAuditContext{
		root:         root,
		imports:      imports,
		contracts:    contracts,
		packageFiles: packageFiles,
		add:          add,
	}

	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}

		state := discoverGeneratedRequests(function, context)
		if len(state.routes) == 0 {
			rejectUnboundGeneratedJSONHelpers(function, add)

			continue
		}

		state.valid = make(map[*ast.Object]bool, len(state.routes))
		for object := range state.routes {
			state.valid[object] = true
		}

		wireMaps := newRequestWireMapState(function, state, context)
		wireMaps.audit()
		auditGeneratedRequestUses(function, state, wireMaps, context)
		recordVerifiedGeneratedSends(function, state.valid, packageFiles, verified)
	}

	return verified, findings
}

func discoverGeneratedRequests(function *ast.FuncDecl, context generatedRequestAuditContext) generatedRequestState {
	state := generatedRequestState{
		initializers: make(map[*ast.Object]map[*ast.AssignStmt]bool),
		routes:       make(map[*ast.Object]HTTPRoute),
		valid:        nil,
	}

	ast.Inspect(function.Body, func(node ast.Node) bool {
		if _, nestedFunction := node.(*ast.FuncLit); nestedFunction {
			return false
		}

		assignment, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}

		for index, expression := range assignment.Rhs {
			call, ok := expression.(*ast.CallExpr)
			if !ok || index >= len(assignment.Lhs) {
				continue
			}

			route, ok := generatedRequestRoute(call, context.imports, context.contracts)
			if !ok {
				continue
			}

			if !generatedRequestServerBound(call, function, context.contracts) {
				context.add(call, "unverified-generated-server",
					"generated request server must be an inventoried origin or the current client's configured base")
			}

			identifier, ok := assignment.Lhs[index].(*ast.Ident)
			if !ok || identifier.Obj == nil || identifier.Name == "_" {
				continue
			}

			if _, parameter := identifier.Obj.Decl.(*ast.Field); parameter {
				context.add(assignment, "unverified-generated-request-initialization",
					"generated request assignment to a parameter does not prove the request on every path")

				continue
			}

			state.routes[identifier.Obj] = route
			if state.initializers[identifier.Obj] == nil {
				state.initializers[identifier.Obj] = make(map[*ast.AssignStmt]bool)
			}

			state.initializers[identifier.Obj][assignment] = true
		}

		return true
	})

	return state
}

func rejectUnboundGeneratedJSONHelpers(function *ast.FuncDecl, add func(ast.Node, string, string)) {
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if _, nestedFunction := node.(*ast.FuncLit); nestedFunction {
			return false
		}

		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == generatedJSONHelperName {
			add(
				call,
				"unverified-generated-request-helper",
				"doGeneratedJSON receives a request whose generated OpenAPI operation is not proven in this function",
			)
		}

		return true
	})
}

func auditGeneratedRequestUses(
	function *ast.FuncDecl,
	state generatedRequestState,
	wireMaps *requestWireMapState,
	context generatedRequestAuditContext,
) {
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if _, nestedFunction := node.(*ast.FuncLit); nestedFunction {
			return false
		}

		switch value := node.(type) {
		case *ast.AssignStmt:
			auditGeneratedRequestAssignment(value, function, state, wireMaps, context)
		case *ast.CallExpr:
			auditGeneratedRequestUseCall(value, function, state, wireMaps, context)
		case *ast.UnaryExpr:
			auditGeneratedRequestAddress(value, state, context.add)
		case *ast.CompositeLit:
			auditGeneratedRequestAggregate(value, state, context.add)
		case *ast.ReturnStmt:
			auditGeneratedRequestReturn(value, function, state, context)
		}

		return true
	})
}

func auditGeneratedRequestAssignment(
	assignment *ast.AssignStmt,
	function *ast.FuncDecl,
	state generatedRequestState,
	wireMaps *requestWireMapState,
	context generatedRequestAuditContext,
) {
	for _, left := range assignment.Lhs {
		identifier, isIdentifier := left.(*ast.Ident)
		if isIdentifier && state.routes[identifier.Obj].OperationID != "" {
			if state.initializers[identifier.Obj][assignment] || safeRequestCopy(assignment, identifier.Obj) {
				continue
			}

			state.valid[identifier.Obj] = false

			context.add(
				assignment,
				"generated-request-reassignment",
				"schema-generated request is reassigned outside a WithContext or Clone copy",
			)

			continue
		}

		request := requestRoot(left)
		if request != nil && state.routes[request].OperationID != "" &&
			isRequestHeaderMap(left, request) {
			if wireMaps.auditHeaderAssignment(assignment, request) {
				continue
			}

			state.valid[request] = false

			continue
		}

		object := requestMutationObject(left, state.routes)
		if object != nil {
			if isRequestRawQuery(left, object) && wireMaps.auditRawQueryAssignment(assignment, object) {
				continue
			}

			state.valid[object] = false

			context.add(
				assignment,
				"generated-request-route-mutation",
				"schema-generated request method, URL path, authority, or query is changed after route generation",
			)
		}
	}

	for _, right := range assignment.Rhs {
		for object := range state.routes {
			if !containsObject(right, object) || state.initializers[object][assignment] ||
				safeRequestCopy(assignment, object) ||
				headerConversionExpression(right, object, context.imports, state.routes) ||
				wireMaps.safeRequestHeaderAlias(assignment, object) ||
				verifiedRequestSendExpression(right, object, function, context.packageFiles) {
				continue
			}

			state.valid[object] = false

			context.add(
				assignment,
				"generated-request-escape",
				"schema-generated request escapes to an alias, aggregate, field, global, or unverified assignment",
			)
		}
	}
}

func auditGeneratedRequestUseCall(
	call *ast.CallExpr,
	function *ast.FuncDecl,
	state generatedRequestState,
	wireMaps *requestWireMapState,
	context generatedRequestAuditContext,
) {
	_ = wireMaps

	selector, isSelector := call.Fun.(*ast.SelectorExpr)

	for _, argument := range call.Args {
		for object := range state.routes {
			auditGeneratedRequestCallArgument(call, selector, isSelector, argument, object, function, state, context)
		}
	}
}

func auditGeneratedRequestCallArgument(
	call *ast.CallExpr,
	selector *ast.SelectorExpr,
	isSelector bool,
	argument ast.Expr,
	object *ast.Object,
	function *ast.FuncDecl,
	state generatedRequestState,
	context generatedRequestAuditContext,
) {
	if !containsObject(argument, object) {
		return
	}

	if isSelector && selector.Sel.Name == "Do" && exactObject(argument, object) {
		if httpClientReceiverIsOwner(selector.X, function, context.packageFiles) {
			return
		}

		state.valid[object] = false

		context.add(
			call,
			"unverified-http-client-receiver",
			"generated request is sent through a client other than the current client's injected httpClient field",
		)

		return
	}

	if isSelector && selector.Sel.Name == generatedJSONHelperName && exactObject(argument, object) &&
		verifiedRequestHelperCall(call, selector, function, context.packageFiles) {
		return
	}

	if isSelector && headerMapConversion(call, context.imports, state.routes) &&
		isRequestHeaderMap(argument, object) {
		return
	}

	state.valid[object] = false

	context.add(
		call,
		"generated-request-escape",
		"schema-generated request or one of its maps escapes to an unverified helper argument",
	)
}

func auditGeneratedRequestAddress(
	expression *ast.UnaryExpr,
	state generatedRequestState,
	add func(ast.Node, string, string),
) {
	if expression.Op != token.AND {
		return
	}

	for object := range state.routes {
		if !containsObject(expression.X, object) {
			continue
		}

		state.valid[object] = false

		add(
			expression,
			"generated-request-escape",
			"address of a schema-generated request is retained through an unverified pointer",
		)
	}
}

func auditGeneratedRequestAggregate(
	literal *ast.CompositeLit,
	state generatedRequestState,
	add func(ast.Node, string, string),
) {
	for object := range state.routes {
		for _, element := range literal.Elts {
			if !containsObject(element, object) {
				continue
			}

			state.valid[object] = false

			add(literal, "generated-request-escape", "schema-generated request is stored in an aggregate")
		}
	}
}

func auditGeneratedRequestReturn(
	statement *ast.ReturnStmt,
	function *ast.FuncDecl,
	state generatedRequestState,
	context generatedRequestAuditContext,
) {
	for object := range state.routes {
		for _, result := range statement.Results {
			if !containsObject(result, object) ||
				verifiedRequestSendExpression(result, object, function, context.packageFiles) {
				continue
			}

			state.valid[object] = false

			context.add(
				statement,
				"generated-request-escape",
				"schema-generated request escapes through a function return",
			)
		}
	}
}

func recordVerifiedGeneratedSends(
	function *ast.FuncDecl,
	valid map[*ast.Object]bool,
	packageFiles []*parsedGoFile,
	verified map[*ast.CallExpr]bool,
) {
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if _, nestedFunction := node.(*ast.FuncLit); nestedFunction {
			return false
		}

		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}

		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Do" {
			return true
		}

		argument, ok := call.Args[0].(*ast.Ident)
		if ok && argument.Obj != nil && valid[argument.Obj] &&
			httpClientReceiverIsOwner(selector.X, function, packageFiles) {
			verified[call] = true
		}

		return true
	})
}
func generatedRequestRoute(call *ast.CallExpr, imports map[string]string, contracts Contracts) (HTTPRoute, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return emptyHTTPRoute(), false
	}

	packagePath, imported, shadowed := selectorImport(selector, imports)
	if !imported || shadowed {
		return emptyHTTPRoute(), false
	}

	route, exists := contracts.GeneratedHTTPRequests[packagePath][selector.Sel.Name]

	return route, exists
}

func generatedRequestServerBound(call *ast.CallExpr, function *ast.FuncDecl, contracts Contracts) bool {
	if len(call.Args) == 0 {
		return false
	}

	if target, constant := stringConstant(call.Args[0]); constant {
		parsed, err := url.Parse(target)
		if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return false
		}

		for server := range contracts.HTTPServers {
			known, parseErr := url.Parse(server)
			if parseErr == nil && canonicalOrigin(parsed) == canonicalOrigin(known) &&
				strings.TrimRight(parsed.EscapedPath(), "/") == strings.TrimRight(known.EscapedPath(), "/") {
				return true
			}
		}

		return false
	}

	base, ok := unparen(call.Args[0]).(*ast.CallExpr)
	if !ok || len(base.Args) != 1 {
		return false
	}

	helper, ok := unparen(base.Fun).(*ast.Ident)
	if !ok || helper.Name != "generatedServerBase" ||
		(helper.Obj != nil && helper.Obj.Kind != ast.Fun) {
		return false
	}

	return currentMethodReceiverIsClientField(unparen(base.Args[0]), function, "baseURI") ||
		currentMethodReceiverIsClientField(unparen(base.Args[0]), function, "oauthBaseURI")
}

func emptyHTTPRoute() HTTPRoute {
	return HTTPRoute{OperationID: "", Method: "", Path: ""}
}

func safeRequestCopy(assignment *ast.AssignStmt, object *ast.Object) bool {
	if assignment.Tok != token.ASSIGN || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
		return false
	}

	left, ok := assignment.Lhs[0].(*ast.Ident)
	if !ok || left.Obj != object {
		return false
	}

	call, ok := assignment.Rhs[0].(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}

	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (selector.Sel.Name != "WithContext" && selector.Sel.Name != "Clone") {
		return false
	}

	receiver, ok := selector.X.(*ast.Ident)

	return ok && receiver.Obj == object
}

func requestMutationObject(expression ast.Expr, requests map[*ast.Object]HTTPRoute) *ast.Object {
	object := requestRoot(expression)
	if object == nil || requests[object].OperationID == "" {
		return nil
	}

	selectors := selectorChain(expression)
	if len(selectors) == 0 {
		return nil
	}

	if selectors[0] == "Method" || selectors[0] == "URL" && (len(selectors) == 1 || pathSelector(selectors[1])) {
		return object
	}

	return nil
}

func pathSelector(name string) bool {
	switch name {
	case "Path", "RawPath", rawQueryFieldName, "ForceQuery", "Host", "Scheme", "Opaque":
		return true
	default:
		return false
	}
}

func requestURLQueryMutation(call *ast.CallExpr, requests map[*ast.Object]HTTPRoute) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	switch selector.Sel.Name {
	case "Set", "Add", "Del", "Encode":
	default:
		return false
	}

	queryCall, ok := selector.X.(*ast.CallExpr)
	if !ok || len(queryCall.Args) != 0 {
		return false
	}

	querySelector, ok := queryCall.Fun.(*ast.SelectorExpr)

	return ok && querySelector.Sel.Name == "Query" && requestURLExpression(querySelector.X, requests)
}

func requestURLExpression(expression ast.Expr, requests map[*ast.Object]HTTPRoute) bool {
	selectors := selectorChain(expression)
	object := requestRoot(expression)

	return object != nil && requests[object].OperationID != "" && len(selectors) > 0 && selectors[0] == "URL"
}
