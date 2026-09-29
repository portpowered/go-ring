package routegate

import (
	"go/ast"
	"go/token"
)

const (
	fcmCheckinPath                = "/checkin"
	fcmRegisterPath               = "/c2dm/register3"
	fcmInstallationsPath          = "/v1/projects/ring-17770/installations"
	fcmCheckinValidatorName       = "validateCheckinRequest"
	fcmRegisterValidatorName      = "validateRegisterRequest"
	fcmInstallationsValidatorName = "validateInstallationsRequest"
	fcmRegistrationsValidatorName = "validateRegistrationsRequest"
	fcmRouteCheckinName           = "routeCheckin"
	fcmRouteRegisterName          = "routeRegister"
	fcmRouteInstallationsName     = "routeInstallations"
	fcmRouteRegistrationsName     = "routeRegistrations"
)

func verifyFCMTransport(
	sources map[string]*parsedGoFile,
	contract fcmOpenAPI,
) ([]*ast.CallExpr, bool) {
	file := sources["pkg/dependencies/push/http_transport.go"]
	if file == nil || len(contract.Paths) != len(expectedFCMRoutes()) {
		return nil, false
	}

	packageFiles := []*parsedGoFile{file}

	roundTrip := findMethod(packageFiles, "diagnosticTransport", "RoundTrip")
	if roundTrip == nil {
		return nil, false
	}

	names := []string{
		"matchFCMRoute",
		"validateFCMHeaders",
		"omitDefaultVAPID",
		"readAndRestoreBody",
		"validateFCMRequestBody",
	}
	calls := make([]*ast.CallExpr, 0, len(names)+1)
	lastPosition := token.Pos(0)

	for _, name := range names {
		matches := directFunctionCalls(roundTrip, name)
		if len(matches) != 1 || matches[0].Pos() <= lastPosition {
			return nil, false
		}

		calls = append(calls, matches[0])
		lastPosition = matches[0].Pos()
	}

	forward := roundTripForwardCall(roundTrip)
	if forward == nil || forward.Pos() <= lastPosition || !fcmTransportValidatorsMatch(file.file, contract) {
		return nil, false
	}

	calls = append(calls, forward)

	return calls, true
}

func directFunctionCalls(function *ast.FuncDecl, name string) []*ast.CallExpr {
	calls := make([]*ast.CallExpr, 0, 1)

	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		identifier, ok := call.Fun.(*ast.Ident)
		if ok && identifier.Name == name {
			calls = append(calls, call)
		}

		return true
	})

	return calls
}

func roundTripForwardCall(function *ast.FuncDecl) *ast.CallExpr {
	var match *ast.CallExpr

	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "RoundTrip" || len(call.Args) != 1 {
			return true
		}

		field, ok := selector.X.(*ast.SelectorExpr)
		if !ok || field.Sel.Name != "next" {
			return true
		}

		receiver, ok := field.X.(*ast.Ident)

		methodReceiver := methodReceiver(function)

		if !ok || methodReceiver == nil || receiver.Obj != methodReceiver.Obj {
			return true
		}

		if match != nil {
			match = nil

			return false
		}

		match = call

		return true
	})

	return match
}

func fcmTransportValidatorsMatch(file *ast.File, contract fcmOpenAPI) bool {
	functions := make(map[string]*ast.FuncDecl)

	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Recv == nil {
			functions[function.Name.Name] = function
		}
	}

	matcher := functions["matchFCMRoute"]
	headers := functions["validateFCMHeaders"]

	bodies := functions["validateFCMRequestBody"]

	if matcher == nil || headers == nil || bodies == nil ||
		!matchFCMRouteSemantics(matcher, contract, file) ||
		!validateFCMHeaderSemantics(headers, file) ||
		!sameHeaderSetSemantics(functions["sameHeaderSet"]) {
		return false
	}

	for path := range expectedFCMRoutes() {
		if _, exists := contract.Paths[path]; !exists {
			return false
		}

		validatorName := expectedBodyValidator(path)
		if !functionDispatchesBodyValidator(bodies, routeNameForPath(path), validatorName) ||
			!fcmBodyValidatorSemantics(functions[validatorName], validatorName, file) {
			return false
		}
	}

	return true
}

func expectedBodyValidator(path string) string {
	switch path {
	case fcmCheckinPath:
		return fcmCheckinValidatorName
	case fcmRegisterPath:
		return fcmRegisterValidatorName
	case fcmInstallationsPath:
		return fcmInstallationsValidatorName
	default:
		return fcmRegistrationsValidatorName
	}
}

func routeNameForPath(path string) string {
	switch path {
	case fcmCheckinPath:
		return fcmRouteCheckinName
	case fcmRegisterPath:
		return fcmRouteRegisterName
	case fcmInstallationsPath:
		return fcmRouteInstallationsName
	default:
		return fcmRouteRegistrationsName
	}
}

func verifyFCMTransportInjection(sources map[string]*parsedGoFile) bool {
	receiver := sources["pkg/dependencies/push/receiver.go"]
	option := sources["third_party/go-push-receiver/option.go"]

	client := sources["third_party/go-push-receiver/client.go"]

	if receiver == nil || option == nil || client == nil {
		return false
	}

	function := findFunction(receiver.file, "StartWithTransports")
	optionFunction := findFunction(option.file, "WithHTTPClient")

	defaults := findFunction(client.file, "setDefaultOptions")

	if function == nil || optionFunction == nil || defaults == nil {
		return false
	}

	if len(directFunctionCalls(function, "newDiagnosticTransport")) != 1 ||
		!functionCallsInjectedHTTPClient(function, receiver.file) || !optionAssignsHTTPClient(optionFunction) ||
		!defaultsOnlySetHTTPClientWhenNil(defaults) {
		return false
	}

	return true
}

func findFunction(file *ast.File, name string) *ast.FuncDecl {
	var match *ast.FuncDecl

	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != name {
			continue
		}

		if match != nil {
			return nil
		}

		match = function
	}

	return match
}

func functionCallsInjectedHTTPClient(function *ast.FuncDecl, file *ast.File) bool {
	imports := importAliases(file)
	withClientCalls := selectorCalls(function, "WithHTTPClient")

	newCalls := selectorCalls(function, "New")

	if len(withClientCalls) != 1 || len(newCalls) != 1 {
		return false
	}

	withClient, ok := withClientCalls[0].Fun.(*ast.SelectorExpr)
	if !ok || !isImportedSelector(withClient, imports, "github.com/portpowered/go-ring/third_party/go-push-receiver") ||
		len(withClientCalls[0].Args) != 1 {
		return false
	}

	address, ok := withClientCalls[0].Args[0].(*ast.UnaryExpr)
	if !ok || address.Op != token.AND {
		return false
	}

	client, ok := address.X.(*ast.CompositeLit)
	if !ok {
		return false
	}

	typeSelector, ok := client.Type.(*ast.SelectorExpr)
	if !ok || typeSelector.Sel.Name != "Client" || !isImportedSelector(typeSelector, imports, httpImportPath) {
		return false
	}

	diagnosticObject := assignedCallObject(function, "newDiagnosticTransport")
	diagnostic := keyedValue(client, "Transport")

	transportIdentifier, ok := diagnostic.(*ast.Ident)
	if !ok || diagnosticObject == nil || transportIdentifier.Obj != diagnosticObject {
		return false
	}

	newSelector, ok := newCalls[0].Fun.(*ast.SelectorExpr)
	if !ok || !isImportedSelector(newSelector, imports, "github.com/portpowered/go-ring/third_party/go-push-receiver") {
		return false
	}

	return callHasIdentifierArgument(newCalls[0], "options")
}

func assignedCallObject(function *ast.FuncDecl, callName string) *ast.Object {
	var object *ast.Object

	for _, call := range directFunctionCalls(function, callName) {
		for _, declaration := range function.Body.List {
			ast.Inspect(declaration, func(node ast.Node) bool {
				assignment, ok := node.(*ast.AssignStmt)
				if !ok || len(assignment.Rhs) != 1 || assignment.Rhs[0] != call || len(assignment.Lhs) != 1 {
					return true
				}

				identifier, ok := assignment.Lhs[0].(*ast.Ident)
				if ok {
					object = identifier.Obj
				}

				return false
			})
		}
	}

	return object
}

func callHasIdentifierArgument(call *ast.CallExpr, name string) bool {
	for _, argument := range call.Args {
		identifier, ok := argument.(*ast.Ident)
		if ok && identifier.Name == name {
			return true
		}
	}

	return false
}

func keyedValue(composite *ast.CompositeLit, name string) ast.Expr {
	for _, element := range composite.Elts {
		keyValue, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}

		key, ok := keyValue.Key.(*ast.Ident)
		if ok && key.Name == name {
			return keyValue.Value
		}
	}

	return nil
}

func optionAssignsHTTPClient(function *ast.FuncDecl) bool {
	assigned := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}

		for index, left := range assignment.Lhs {
			selector, ok := left.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "httpClient" || index >= len(assignment.Rhs) {
				continue
			}

			value, ok := assignment.Rhs[index].(*ast.Ident)
			if ok && value.Name == "c" {
				assigned = true
			}
		}

		return true
	})

	return assigned
}

func defaultsOnlySetHTTPClientWhenNil(function *ast.FuncDecl) bool {
	guarded := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		conditional, ok := node.(*ast.IfStmt)
		if !ok {
			return true
		}

		if !isNilComparison(conditional.Cond, "httpClient") {
			return true
		}

		guarded = blockHasSelector(conditional.Body, "httpClient")

		return false
	})

	return guarded
}

func blockHasSelector(block *ast.BlockStmt, name string) bool {
	found := false

	ast.Inspect(block, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == name {
			found = true

			return false
		}

		return !found
	})

	return found
}

func isNilComparison(expression ast.Expr, field string) bool {
	binary, ok := expression.(*ast.BinaryExpr)
	if !ok || binary.Op != token.EQL {
		return false
	}

	selector, ok := binary.X.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != field {
		return false
	}

	identifier, ok := binary.Y.(*ast.Ident)

	return ok && identifier.Name == "nil"
}
