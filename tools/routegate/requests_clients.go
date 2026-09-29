package routegate

import (
	"go/ast"
	"go/token"
)

func hasSingleReceiver(function *ast.FuncDecl) bool {
	return function != nil && function.Recv != nil && len(function.Recv.List) == 1 &&
		len(function.Recv.List[0].Names) == 1
}

func httpClientReceiverIsOwner(expression ast.Expr, function *ast.FuncDecl, packageFiles []*parsedGoFile) bool {
	if function == nil || function.Body == nil {
		return false
	}

	if currentMethodReceiverIsClientField(expression, function, "httpClient") {
		return true
	}

	if parameter, ok := expression.(*ast.Ident); ok && parameter.Obj != nil {
		if authClientCloneIsFromReceiver(function, parameter) {
			return true
		}

		return trustedHTTPClientParameter(function, parameter, packageFiles)
	}

	if pendingClientSelectorFromReceiver(expression, function) {
		return true
	}

	return false
}

func currentMethodReceiverIsClientField(expression ast.Expr, function *ast.FuncDecl, field string) bool {
	if function.Recv == nil || len(function.Recv.List) != 1 || len(function.Recv.List[0].Names) != 1 {
		return false
	}

	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != field {
		return false
	}

	owner, ok := selector.X.(*ast.Ident)

	return ok && owner.Obj != nil && owner.Obj == function.Recv.List[0].Names[0].Obj && field == selector.Sel.Name
}

func authClientCloneIsFromReceiver(function *ast.FuncDecl, client *ast.Ident) bool {
	if !hasSingleReceiver(function) || client.Obj == nil {
		return false
	}

	copyObject, clientAssignments := receiverClientCopyObject(function, client.Obj)
	if clientAssignments != 1 || copyObject == nil {
		return false
	}

	copyAssignments, validCopy := copiedHTTPClientAssignments(function, copyObject)

	return copyAssignments == 1 && validCopy
}

func receiverClientCopyObject(function *ast.FuncDecl, client *ast.Object) (*ast.Object, int) {
	var copyObject *ast.Object

	assignments := 0

	ast.Inspect(function.Body, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}

		assignment, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}

		for index, left := range assignment.Lhs {
			identifier, ok := left.(*ast.Ident)
			if !ok || index >= len(assignment.Rhs) {
				continue
			}

			if identifier.Obj == client {
				assignments++

				address, ok := assignment.Rhs[index].(*ast.UnaryExpr)
				if !ok || address.Op != token.AND {
					continue
				}

				copyIdentifier, ok := address.X.(*ast.Ident)
				if !ok || copyIdentifier.Obj == nil {
					continue
				}

				copyObject = copyIdentifier.Obj
			}
		}

		return true
	})

	return copyObject, assignments
}
func copiedHTTPClientAssignments(function *ast.FuncDecl, copyObject *ast.Object) (int, bool) {
	assignments := 0
	validCopy := false

	for _, declaration := range function.Body.List {
		ast.Inspect(declaration, func(node ast.Node) bool {
			if _, nested := node.(*ast.FuncLit); nested {
				return false
			}

			assignment, ok := node.(*ast.AssignStmt)
			if !ok {
				return true
			}

			for index, left := range assignment.Lhs {
				identifier, ok := left.(*ast.Ident)
				if !ok || identifier.Obj != copyObject || index >= len(assignment.Rhs) {
					continue
				}

				assignments++

				value, ok := assignment.Rhs[index].(*ast.StarExpr)
				if !ok {
					continue
				}

				validCopy = currentMethodReceiverIsClientField(value.X, function, "httpClient")
			}

			return true
		})
	}

	return assignments, validCopy
}

func pendingClientSelectorFromReceiver(expression ast.Expr, function *ast.FuncDecl) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "client" {
		return false
	}

	pending, ok := selector.X.(*ast.Ident)
	if !ok || pending.Obj == nil || function.Recv == nil || len(function.Recv.List) != 1 ||
		len(function.Recv.List[0].Names) != 1 {
		return false
	}

	assignments := 0
	valid := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}

		assignment, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}

		for index, left := range assignment.Lhs {
			identifier, ok := left.(*ast.Ident)
			if !ok || identifier.Obj != pending.Obj || index >= len(assignment.Rhs) {
				continue
			}

			assignments++
			valid = currentMethodReceiverIsClientField(assignment.Rhs[index], function, "pendingPKCE")
		}

		return true
	})

	return assignments == 1 && valid
}

func trustedHTTPClientParameter(function *ast.FuncDecl, parameter *ast.Ident, packageFiles []*parsedGoFile) bool {
	if !hasSingleReceiver(function) || parameter.Obj == nil || !isTrustedHTTPClientParameter(function, parameter) {
		return false
	}

	parameterIndex := parameterPosition(function, parameter.Obj)
	if parameterIndex < 0 {
		return false
	}

	return trustedHTTPClientCallSites(function, parameterIndex, packageFiles)
}

func isTrustedHTTPClientParameter(function *ast.FuncDecl, parameter *ast.Ident) bool {
	return (function.Name.Name == "authFormRequest" && parameter.Name == "client") ||
		(function.Name.Name == "sendOAuthAuthorize" && parameter.Name == "authClient")
}

func trustedHTTPClientCallSites(function *ast.FuncDecl, parameterIndex int, packageFiles []*parsedGoFile) bool {
	foundCall := false
	validCalls := true

	for _, source := range packageFiles {
		for _, declaration := range source.file.Decls {
			caller, ok := declaration.(*ast.FuncDecl)
			if !ok || caller.Body == nil {
				continue
			}

			callFound, callerValid := trustedHTTPClientCaller(function, caller, parameterIndex, packageFiles)
			foundCall = foundCall || callFound
			validCalls = validCalls && callerValid
		}
	}

	return foundCall && validCalls
}

func trustedHTTPClientCaller(
	function, caller *ast.FuncDecl,
	parameterIndex int,
	packageFiles []*parsedGoFile,
) (bool, bool) {
	foundCall := false
	validCalls := true
	parents := parentNodes(caller.Body)

	ast.Inspect(caller.Body, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}

		switch value := node.(type) {
		case *ast.SelectorExpr:
			if value.Sel.Name == function.Name.Name && !isDirectClientMethodCall(value, parents) {
				validCalls = false
			}
		case *ast.CallExpr:
			selector, ok := value.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != function.Name.Name {
				return true
			}

			foundCall = true

			if !trustedHTTPClientInvocation(function, caller, value, selector, parameterIndex, packageFiles) {
				validCalls = false
			}
		}

		return true
	})

	return foundCall, validCalls
}

func isDirectClientMethodCall(expression *ast.SelectorExpr, parents map[ast.Node]ast.Node) bool {
	call, ok := parents[expression].(*ast.CallExpr)

	return ok && call.Fun == expression
}

func trustedHTTPClientInvocation(
	function, caller *ast.FuncDecl,
	call *ast.CallExpr,
	selector *ast.SelectorExpr,
	parameterIndex int,
	packageFiles []*parsedGoFile,
) bool {
	return sameMethodReceiverType(function, caller) && callUsesCurrentReceiver(selector.X, caller) &&
		len(call.Args) > parameterIndex && httpClientReceiverIsOwner(call.Args[parameterIndex], caller, packageFiles)
}

func verifiedRequestSendExpression(
	expression ast.Expr,
	request *ast.Object,
	function *ast.FuncDecl,
	packageFiles []*parsedGoFile,
) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}

	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	if selector.Sel.Name == "Do" && len(call.Args) == 1 && exactObject(call.Args[0], request) {
		return httpClientReceiverIsOwner(selector.X, function, packageFiles)
	}

	return selector.Sel.Name == generatedJSONHelperName &&
		verifiedRequestHelperCall(call, selector, function, packageFiles)
}

func verifiedRequestHelperCall(
	call *ast.CallExpr,
	selector *ast.SelectorExpr,
	caller *ast.FuncDecl,
	packageFiles []*parsedGoFile,
) bool {
	if selector.Sel.Name != generatedJSONHelperName || len(call.Args) < 2 ||
		!callUsesCurrentReceiver(selector.X, caller) {
		return false
	}

	helper := findMethod(packageFiles, receiverTypeName(caller), generatedJSONHelperName)
	authorized := findMethod(packageFiles, receiverTypeName(caller), "sendAuthorizedRequest")

	retry := findMethod(packageFiles, receiverTypeName(caller), sendWithRetryHelperName)

	if helper == nil || authorized == nil || retry == nil {
		return false
	}

	if !generatedJSONHelperBody(helper, authorized) || !authorizedRequestHelperBody(authorized, retry) {
		return false
	}

	return retryHelperBody(retry)
}
