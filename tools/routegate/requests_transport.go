package routegate

import (
	"go/ast"
	"go/token"
)

func verifiedHTTPTransportCalls(packageFiles []*parsedGoFile) map[*ast.CallExpr]bool {
	verified := make(map[*ast.CallExpr]bool)
	seen := make(map[string]bool)

	for _, source := range packageFiles {
		for _, declaration := range source.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil || function.Name.Name != sendWithRetryHelperName {
				continue
			}

			typeName := receiverTypeName(function)
			if typeName == "" || seen[typeName] {
				continue
			}

			seen[typeName] = true
			authorized := findMethod(packageFiles, typeName, "sendAuthorizedRequest")

			jsonHelper := findMethod(packageFiles, typeName, generatedJSONHelperName)

			if authorized != nil && jsonHelper != nil && authorizedRequestHelperBody(authorized, function) &&
				generatedJSONHelperBody(jsonHelper, authorized) &&
				retryHelperBody(function) {
				if call := retryDoCall(function); call != nil {
					verified[call] = true
				}
			}
		}
	}

	return verified
}

func findMethod(packageFiles []*parsedGoFile, typeName, methodName string) *ast.FuncDecl {
	var match *ast.FuncDecl

	for _, source := range packageFiles {
		for _, declaration := range source.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Name.Name != methodName || receiverTypeName(function) != typeName {
				continue
			}

			if match != nil {
				return nil
			}

			match = function
		}
	}

	return match
}

func requestParameter(function *ast.FuncDecl) *ast.Ident {
	if function.Type.Params == nil {
		return nil
	}

	for _, field := range function.Type.Params.List {
		for _, name := range field.Names {
			if name.Name == "req" {
				return name
			}
		}
	}

	return nil
}

func generatedJSONHelperBody(function, authorized *ast.FuncDecl) bool {
	request := requestParameter(function)
	if request == nil ||
		!functionHasOnlyRequestCalls(function, request.Obj, map[string]bool{sendAuthorizedHelperName: true}) {
		return false
	}

	return countMethodCallsWithRequest(function, sendAuthorizedHelperName, request.Obj, 1) == 1 &&
		callReceiverIsCurrentMethod(function, sendAuthorizedHelperName) &&
		sameMethodReceiverType(function, authorized)
}

func authorizedRequestHelperBody(function, retry *ast.FuncDecl) bool {
	request := requestParameter(function)
	if request == nil ||
		!functionHasOnlyRequestCalls(function, request.Obj, map[string]bool{sendWithRetryHelperName: true}) {
		return false
	}

	return countMethodCallsWithRequest(function, sendWithRetryHelperName, request.Obj, 1) == 1 &&
		callReceiverIsCurrentMethod(function, sendWithRetryHelperName) &&
		sameMethodReceiverType(function, retry)
}

func retryHelperBody(function *ast.FuncDecl) bool {
	request := requestParameter(function)
	if request == nil || !functionHasOnlyRequestCalls(function, request.Obj, map[string]bool{"Do": true}) {
		return false
	}

	call := retryDoCall(function)
	if call == nil {
		return false
	}

	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !currentMethodReceiverIsClientField(selector.X, function, "httpClient") || len(call.Args) != 1 {
		return false
	}

	attempt, ok := retryAttemptIdentifier(call)
	if !ok {
		return false
	}

	return retryAttemptAssignmentsAreSafe(function, request.Obj, attempt.Obj)
}

func retryAttemptIdentifier(call *ast.CallExpr) (*ast.Ident, bool) {
	if call == nil || len(call.Args) != 1 {
		return nil, false
	}

	attempt, ok := call.Args[0].(*ast.Ident)

	return attempt, ok && attempt.Obj != nil
}

func retryAttemptAssignmentsAreSafe(function *ast.FuncDecl, request, attempt *ast.Object) bool {
	assignments := 0
	validAssignments := true
	attemptRequests := map[*ast.Object]HTTPRoute{
		request: {OperationID: "transport", Method: "", Path: "", HasBody: false},
		attempt: {OperationID: "transport", Method: "", Path: "", HasBody: false},
	}

	ast.Inspect(function.Body, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}

		switch value := node.(type) {
		case *ast.AssignStmt:
			for index, left := range value.Lhs {
				if index < len(value.Rhs) && safeRetryBodyRestoration(left, value.Rhs[index], request, attempt) {
					continue
				}

				identifier, ok := left.(*ast.Ident)
				if !ok || identifier.Obj != attempt || index >= len(value.Rhs) {
					if requestMutationObject(left, attemptRequests) != nil {
						validAssignments = false
					}

					continue
				}

				assignments++

				if !safeAttemptRequestAssignment(value.Rhs[index], request) {
					validAssignments = false
				}
			}
		case *ast.CallExpr:
			if requestURLQueryMutation(value, attemptRequests) {
				validAssignments = false
			}
		}

		return true
	})

	return assignments > 0 && validAssignments
}

func safeRetryBodyRestoration(left, right ast.Expr, request, attempt *ast.Object) bool {
	selector, ok := left.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Body" {
		return false
	}

	receiver, ok := selector.X.(*ast.Ident)

	return ok && receiver.Obj == attempt && receiver.Obj != request && safeRequestBodyCall(right, request)
}

func safeAttemptRequestAssignment(expression ast.Expr, request *ast.Object) bool {
	if identifier, ok := expression.(*ast.Ident); ok {
		return identifier.Obj == request
	}

	call, ok := expression.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}

	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Clone" {
		return false
	}

	identifier, ok := selector.X.(*ast.Ident)

	return ok && identifier.Obj == request
}

func safeRequestBodyCall(expression ast.Expr, request *ast.Object) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}

	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "GetBody" {
		return false
	}

	identifier, ok := selector.X.(*ast.Ident)

	return ok && identifier.Obj == request
}

func retryDoCall(function *ast.FuncDecl) *ast.CallExpr {
	var found *ast.CallExpr

	count := 0

	ast.Inspect(function.Body, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}

		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "Do" {
			found = call
			count++
		}

		return true
	})

	if count != 1 {
		return nil
	}

	return found
}

func functionHasOnlyRequestCalls(function *ast.FuncDecl, request *ast.Object, allowedMethods map[string]bool) bool {
	valid := true
	testRoutes := map[*ast.Object]HTTPRoute{
		request: {OperationID: "transport", Method: "", Path: "", HasBody: false},
	}

	ast.Inspect(function.Body, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}

		if !requestNodeUsesOnlyAllowedCalls(node, function, request, allowedMethods, testRoutes) {
			valid = false
		}

		return true
	})

	return valid
}

func requestNodeUsesOnlyAllowedCalls(
	node ast.Node,
	function *ast.FuncDecl,
	request *ast.Object,
	allowedMethods map[string]bool,
	testRoutes map[*ast.Object]HTTPRoute,
) bool {
	switch value := node.(type) {
	case *ast.AssignStmt:
		return requestAssignmentUsesOnlyAllowedCalls(value, function, request, allowedMethods, testRoutes)
	case *ast.CallExpr:
		return requestCallUsesOnlyAllowedCalls(value, request, allowedMethods, testRoutes)
	case *ast.UnaryExpr:
		return value.Op != token.AND || !containsObject(value.X, request)
	case *ast.CompositeLit:
		return aggregateHasNoRequest(value, request)
	case *ast.ReturnStmt:
		return returnedRequestIsAbsent(value, request)
	default:
		return true
	}
}

func requestAssignmentUsesOnlyAllowedCalls(
	assignment *ast.AssignStmt,
	function *ast.FuncDecl,
	request *ast.Object,
	allowedMethods map[string]bool,
	testRoutes map[*ast.Object]HTTPRoute,
) bool {
	for _, left := range assignment.Lhs {
		if requestMutationObject(left, testRoutes) != nil {
			return false
		}
	}

	for index, right := range assignment.Rhs {
		if !containsObject(right, request) || index >= len(assignment.Lhs) {
			continue
		}

		if safeRequestCopy(assignment, request) || allowedRequestCall(right, request, allowedMethods) {
			continue
		}

		left, isIdentifier := assignment.Lhs[index].(*ast.Ident)

		if function.Name.Name == sendWithRetryHelperName && safeRequestBodyCall(right, request) {
			continue
		}

		if function.Name.Name == sendWithRetryHelperName && isIdentifier && left.Obj != request &&
			safeAttemptRequestAssignment(right, request) {
			continue
		}

		return false
	}

	return true
}

func allowedRequestCall(expression ast.Expr, request *ast.Object, allowedMethods map[string]bool) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}

	selector, ok := call.Fun.(*ast.SelectorExpr)

	return ok && allowedMethods[selector.Sel.Name] && hasExactArgument(call, request)
}

func requestCallUsesOnlyAllowedCalls(
	call *ast.CallExpr,
	request *ast.Object,
	allowedMethods map[string]bool,
	testRoutes map[*ast.Object]HTTPRoute,
) bool {
	if requestURLQueryMutation(call, testRoutes) {
		return false
	}

	selector, ok := call.Fun.(*ast.SelectorExpr)

	for _, argument := range call.Args {
		if !containsObject(argument, request) {
			continue
		}

		if !ok || !allowedMethods[selector.Sel.Name] || !exactObject(argument, request) {
			return false
		}
	}

	return true
}

func aggregateHasNoRequest(literal *ast.CompositeLit, request *ast.Object) bool {
	for _, element := range literal.Elts {
		if containsObject(element, request) {
			return false
		}
	}

	return true
}

func returnedRequestIsAbsent(statement *ast.ReturnStmt, request *ast.Object) bool {
	for _, result := range statement.Results {
		if exactObject(result, request) {
			return false
		}
	}

	return true
}

func countMethodCallsWithRequest(function *ast.FuncDecl, method string, request *ast.Object, requestArg int) int {
	count := 0

	ast.Inspect(function.Body, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}

		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == method && len(call.Args) > requestArg &&
			exactObject(call.Args[requestArg], request) {
			count++
		}

		return true
	})

	return count
}

func hasExactArgument(call *ast.CallExpr, request *ast.Object) bool {
	for _, argument := range call.Args {
		if exactObject(argument, request) {
			return true
		}
	}

	return false
}

func callReceiverIsCurrentMethod(function *ast.FuncDecl, method string) bool {
	valid := false
	count := 0

	ast.Inspect(function.Body, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}

		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != method {
			return true
		}

		count++
		valid = callUsesCurrentReceiver(selector.X, function)

		return true
	})

	return count == 1 && valid
}

func parameterPosition(function *ast.FuncDecl, object *ast.Object) int {
	if function.Type.Params == nil {
		return -1
	}

	position := 0

	for _, field := range function.Type.Params.List {
		if len(field.Names) == 0 {
			position++

			continue
		}

		for _, name := range field.Names {
			if name.Obj == object {
				return position
			}

			position++
		}
	}

	return -1
}

func sameMethodReceiverType(left, right *ast.FuncDecl) bool {
	return receiverTypeName(left) != "" && receiverTypeName(left) == receiverTypeName(right)
}

func receiverTypeName(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) != 1 {
		return ""
	}

	expression := function.Recv.List[0].Type
	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
	}

	identifier := rootIdentifierFromExpression(expression)
	if identifier == nil {
		return ""
	}

	return identifier.Name
}

func callUsesCurrentReceiver(expression ast.Expr, function *ast.FuncDecl) bool {
	if function.Recv == nil || len(function.Recv.List) != 1 || len(function.Recv.List[0].Names) != 1 {
		return false
	}

	identifier, ok := expression.(*ast.Ident)

	return ok && identifier.Obj != nil && identifier.Obj == function.Recv.List[0].Names[0].Obj
}
