package routegate

import (
	"go/ast"
	"go/token"
	"strconv"
)

func matchFCMRouteSemantics(function *ast.FuncDecl, contract fcmOpenAPI, file *ast.File) bool {
	if function == nil || !functionConditionHasSelectorAndString(function, "Scheme", "https") ||
		!functionConditionHasImportedSelector(function, file, "MethodPost", httpImportPath) ||
		!functionHasIdentifierCall(function, "NewNetworkError") {
		return false
	}

	for routePath, route := range expectedFCMRoutes() {
		if _, exists := contract.Paths[routePath]; !exists ||
			!routeSwitchCaseMatches(function, route, routeNameForPath(routePath)) {
			return false
		}
	}

	return hasDefaultCaseReturningCall(function, "NewNetworkError")
}

func routeSwitchCaseMatches(function *ast.FuncDecl, route fcmRouteContract, resultName string) bool {
	found := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		switchStatement, ok := node.(*ast.SwitchStmt)
		if !ok || switchStatement.Tag != nil {
			return true
		}

		for _, statement := range switchStatement.Body.List {
			clause, ok := statement.(*ast.CaseClause)
			if !ok || len(clause.List) != 1 || !caseReturnsIdentifier(clause, resultName) {
				continue
			}

			if nodeHasSelectorName(clause.List[0], "Host") &&
				nodeHasSelectorName(clause.List[0], route.constant+"Host") &&
				nodeHasSelectorName(clause.List[0], "EscapedPath") &&
				nodeHasSelectorName(clause.List[0], route.constant+"Path") {
				found = true

				return false
			}
		}

		return !found
	})

	return found
}

func caseReturnsIdentifier(clause *ast.CaseClause, expected string) bool {
	found := false

	for _, bodyStatement := range clause.Body {
		ast.Inspect(bodyStatement, func(node ast.Node) bool {
			statement, ok := node.(*ast.ReturnStmt)
			if !ok || len(statement.Results) == 0 {
				return true
			}

			identifier, ok := unparen(statement.Results[0]).(*ast.Ident)
			if ok && identifier.Name == expected {
				found = true

				return false
			}

			return true
		})
	}

	return found
}

func functionConditionHasSelectorAndString(function *ast.FuncDecl, selectorName, value string) bool {
	found := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		conditional, ok := node.(*ast.IfStmt)
		if !ok || !nodeHasSelectorName(conditional.Cond, selectorName) {
			return true
		}

		if nodeHasString(conditional.Cond, value) {
			found = true

			return false
		}

		return true
	})

	return found
}

func functionConditionHasImportedSelector(function *ast.FuncDecl, file *ast.File, name, path string) bool {
	imports := importAliases(file)
	found := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		conditional, ok := node.(*ast.IfStmt)
		if !ok {
			return true
		}

		ast.Inspect(conditional.Cond, func(conditionNode ast.Node) bool {
			selector, ok := conditionNode.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == name && isImportedSelector(selector, imports, path) {
				found = true

				return false
			}

			return !found
		})

		return !found
	})

	return found
}

func nodeHasSelectorName(node ast.Node, name string) bool {
	found := false

	ast.Inspect(node, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == name {
			found = true

			return false
		}

		return !found
	})

	return found
}

func nodeHasIdentifierName(node ast.Node, name string) bool {
	found := false

	ast.Inspect(node, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok && identifier.Name == name {
			found = true

			return false
		}

		return !found
	})

	return found
}

func nodeHasString(node ast.Node, expected string) bool {
	found := false

	ast.Inspect(node, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if ok && literal.Kind == token.STRING && stringLiteralEquals(literal, expected) {
			found = true

			return false
		}

		return !found
	})

	return found
}

func hasDefaultCaseReturningCall(function *ast.FuncDecl, name string) bool {
	found := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		clause, ok := node.(*ast.CaseClause)
		if !ok || clause.List != nil {
			return true
		}

		if blockHasCallName(clause.Body, name) {
			found = true

			return false
		}

		return true
	})

	return found
}

func blockHasCallName(block []ast.Stmt, name string) bool {
	found := false

	for _, statement := range block {
		ast.Inspect(statement, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}

			calledName := callName(call.Fun)
			if calledName == name {
				found = true

				return false
			}

			return !found
		})
	}

	return found
}

func validateFCMHeaderSemantics(function *ast.FuncDecl, file *ast.File) bool {
	if function == nil || !functionHasIdentifierCall(function, "sameHeaderSet") ||
		!functionHasIdentifierCall(function, "invalidFCMHeaderError") ||
		!functionHasRouteCase(function, "routeCheckin", "FCMContentTypeHeader", "FCMContentTypeProtobuf") ||
		!functionHasRouteCase(function, "routeRegister", "FCMContentTypeHeader", "FCMContentTypeForm") ||
		!functionHasRouteCase(function, "routeInstallations", "FCMInstallationsAPIKeyHeader", "FCMContentTypeJSON", "FCMAcceptHeader") ||
		!functionHasRouteCase(function, "routeRegistrations", "FCMInstallationsAPIKeyHeader", "FCMContentTypeJSON", "FCMInstallationsAuthHeader") {
		return false
	}

	if !functionConditionHasCall(function, "decimal") ||
		!functionConditionHasSelector(function, "TrimSpace") {
		return false
	}

	return functionConditionHasCall(function, "sameHeaderSet") &&
		functionHasImportedSelector(function, importAliases(file), "Header", httpImportPath)
}

func functionHasRouteCase(function *ast.FuncDecl, route string, requiredSelectors ...string) bool {
	found := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		switchStatement, ok := node.(*ast.SwitchStmt)
		if !ok || !nodeHasIdentifierName(switchStatement.Tag, "route") {
			return true
		}

		for _, statement := range switchStatement.Body.List {
			clause, ok := statement.(*ast.CaseClause)
			if !ok || len(clause.List) != 1 || !nodeHasIdentifierName(clause.List[0], route) {
				continue
			}

			found = true

			for _, required := range requiredSelectors {
				if !nodeHasSelectorName(clause, required) {
					found = false

					break
				}
			}

			return false
		}

		return true
	})

	return found
}

func functionConditionHasCall(function *ast.FuncDecl, name string) bool {
	found := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		conditional, ok := node.(*ast.IfStmt)
		if ok && nodeHasCallName(conditional.Cond, name) {
			found = true

			return false
		}

		return !found
	})

	return found
}

func functionConditionHasSelector(function *ast.FuncDecl, name string) bool {
	found := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		conditional, ok := node.(*ast.IfStmt)
		if ok && nodeHasSelectorName(conditional.Cond, name) {
			found = true

			return false
		}

		return !found
	})

	return found
}

func nodeHasCallName(node ast.Node, name string) bool {
	found := false

	ast.Inspect(node, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		if callName(call.Fun) == name {
			found = true

			return false
		}

		return !found
	})

	return found
}

func sameHeaderSetSemantics(function *ast.FuncDecl) bool {
	if function == nil || !functionHasSelector(function, "Values") ||
		!functionHasRangeOver(function, "expected") || !functionHasRangeOver(function, "values") ||
		!functionConditionHasLengthMismatch(function, "expected", "actual") ||
		!functionConditionHasLengthMismatch(function, "values", "actualValues") ||
		!functionConditionHasIndexMismatch(function, "actualValues", "index", "value") ||
		!functionReturnsBoolean(function, true) || countBooleanReturnsInCondition(function, false) < 3 {
		return false
	}

	return functionHasIdentifier(function, "name") && functionHasIdentifier(function, "actualValues")
}

func functionHasRangeOver(function *ast.FuncDecl, name string) bool {
	found := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		statement, ok := node.(*ast.RangeStmt)
		if ok && nodeHasIdentifierName(statement.X, name) {
			found = true

			return false
		}

		return !found
	})

	return found
}

func functionConditionHasLengthMismatch(function *ast.FuncDecl, leftName, rightName string) bool {
	found := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		conditional, ok := node.(*ast.IfStmt)
		if !ok {
			return true
		}

		ast.Inspect(conditional.Cond, func(conditionNode ast.Node) bool {
			binary, ok := conditionNode.(*ast.BinaryExpr)
			if !ok || binary.Op != token.NEQ {
				return true
			}

			left, right := binary.X, binary.Y
			if nodeHasIdentifierName(left, leftName) && nodeHasIdentifierName(right, rightName) ||
				nodeHasIdentifierName(left, rightName) && nodeHasIdentifierName(right, leftName) {
				found = true

				return false
			}

			return true
		})

		return !found
	})

	return found
}

func functionConditionHasIndexMismatch(function *ast.FuncDecl, mapName, indexName, valueName string) bool {
	found := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		conditional, ok := node.(*ast.IfStmt)
		if !ok {
			return true
		}

		ast.Inspect(conditional.Cond, func(conditionNode ast.Node) bool {
			binary, ok := conditionNode.(*ast.BinaryExpr)
			if !ok || binary.Op != token.NEQ {
				return true
			}

			index, ok := unparen(binary.X).(*ast.IndexExpr)
			if ok && nodeHasIdentifierName(index.X, mapName) && nodeHasIdentifierName(index.Index, indexName) &&
				nodeHasIdentifierName(binary.Y, valueName) {
				found = true

				return false
			}

			return true
		})

		return !found
	})

	return found
}

func functionReturnsBoolean(function *ast.FuncDecl, expected bool) bool {
	found := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		statement, ok := node.(*ast.ReturnStmt)
		if !ok || len(statement.Results) != 1 {
			return true
		}

		identifier, ok := unparen(statement.Results[0]).(*ast.Ident)
		if ok && identifier.Name == strconv.FormatBool(expected) {
			found = true

			return false
		}

		return true
	})

	return found
}

func countBooleanReturnsInCondition(function *ast.FuncDecl, expected bool) int {
	wanted := strconv.FormatBool(expected)
	count := 0

	ast.Inspect(function.Body, func(node ast.Node) bool {
		conditional, ok := node.(*ast.IfStmt)
		if !ok {
			return true
		}

		returned := false

		ast.Inspect(conditional.Body, func(bodyNode ast.Node) bool {
			statement, ok := bodyNode.(*ast.ReturnStmt)
			if !ok || len(statement.Results) != 1 {
				return true
			}

			identifier, ok := unparen(statement.Results[0]).(*ast.Ident)
			if ok && identifier.Name == wanted {
				returned = true

				return false
			}

			return true
		})

		if returned {
			count++
		}

		return true
	})

	return count
}

func functionDispatchesBodyValidator(function *ast.FuncDecl, route, validator string) bool {
	found := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		switchStatement, ok := node.(*ast.SwitchStmt)
		if !ok || !nodeHasIdentifierName(switchStatement.Tag, "route") {
			return true
		}

		for _, statement := range switchStatement.Body.List {
			clause, ok := statement.(*ast.CaseClause)
			if !ok || len(clause.List) != 1 || !nodeHasIdentifierName(clause.List[0], route) {
				continue
			}

			found = blockHasCallName(clause.Body, validator)

			return false
		}

		return true
	})

	return found && hasDefaultCaseReturningCall(function, "NewNetworkError")
}

func fcmBodyValidatorSemantics(function *ast.FuncDecl, name string, file *ast.File) bool {
	if function == nil || !functionHasIdentifierCall(function, "NewBadRequestError") {
		return false
	}

	switch name {
	case "validateCheckinRequest":
		return functionRejectingConditionHasCall(function, "exactProtoFields") &&
			functionRejectingConditionHasSelector(function, "GetVersion") &&
			functionRejectingConditionHasSelector(function, "GetUnknown")
	case "validateRegisterRequest":
		return len(importedSelectorCalls(function, "ParseQuery", importAliases(file), "net/url")) == 1 &&
			functionRejectingConditionHasCall(function, "oneValueIs") &&
			functionRejectingConditionHasCall(function, "decimal")
	case "validateInstallationsRequest":
		return functionRejectingConditionHasCall(function, "exactJSONKeys") &&
			functionRejectingConditionHasCall(function, "jsonStringIs") &&
			functionRejectingConditionHasCall(function, "validFID")
	case "validateRegistrationsRequest":
		return functionRejectingConditionHasCall(function, "exactJSONKeys") &&
			functionRejectingConditionHasCall(function, "validWebPushKey") &&
			functionRejectingConditionHasSelector(function, "HasPrefix") &&
			functionRejectingConditionHasSelector(function, "FCMRegistrationEndpointPrefix")
	default:
		return false
	}
}

func functionRejectingConditionHasCall(function *ast.FuncDecl, name string) bool {
	found := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		conditional, ok := node.(*ast.IfStmt)
		if !ok || !blockHasCallName(conditional.Body.List, "NewBadRequestError") ||
			!nodeHasCallName(conditional.Cond, name) {
			return true
		}

		found = true

		return false
	})

	return found
}

func functionRejectingConditionHasSelector(function *ast.FuncDecl, name string) bool {
	found := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		conditional, ok := node.(*ast.IfStmt)
		if !ok || !blockHasCallName(conditional.Body.List, "NewBadRequestError") ||
			!nodeHasSelectorName(conditional.Cond, name) {
			return true
		}

		found = true

		return false
	})

	return found
}

func functionHasIdentifierCall(function *ast.FuncDecl, name string) bool {
	return len(selectorOrIdentifierCalls(function, name)) > 0
}

func callName(expression ast.Expr) string {
	switch function := unparen(expression).(type) {
	case *ast.Ident:
		return function.Name
	case *ast.SelectorExpr:
		return function.Sel.Name
	default:
		return ""
	}
}

func functionHasIdentifier(function *ast.FuncDecl, name string) bool {
	return functionHasNode(function, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)

		return ok && identifier.Name == name
	})
}

func functionHasSelector(function *ast.FuncDecl, name string) bool {
	return functionHasNode(function, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)

		return ok && selector.Sel.Name == name
	})
}

func functionHasImportedSelector(function *ast.FuncDecl, imports map[string]string, name, path string) bool {
	found := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == name && isImportedSelector(selector, imports, path) {
			found = true

			return false
		}

		return !found
	})

	return found
}

func functionHasNode(function *ast.FuncDecl, predicate func(ast.Node) bool) bool {
	found := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		if predicate(node) {
			found = true

			return false
		}

		return !found
	})

	return found
}
