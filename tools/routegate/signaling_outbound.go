package routegate

import (
	"go/ast"
	"go/token"
	"strings"
)

const generatedFrameMarshallerName = "marshalGeneratedFrame"

func protocolMethodCases(
	function *ast.FuncDecl,
	imports map[string]string,
	protocolValues map[string]string,
) (map[string]*ast.CaseClause, *ast.CaseClause) {
	cases := make(map[string]*ast.CaseClause)

	var defaultClause *ast.CaseClause

	ast.Inspect(function.Body, func(node ast.Node) bool {
		switchStatement, ok := node.(*ast.SwitchStmt)
		if !ok || !isMethodSwitch(switchStatement.Tag) {
			return true
		}

		for _, statement := range switchStatement.Body.List {
			clause, ok := statement.(*ast.CaseClause)
			if !ok {
				continue
			}

			if len(clause.List) == 0 {
				defaultClause = clause

				continue
			}

			for _, expression := range clause.List {
				method, valid := signalingMethod(expression, imports, protocolValues)
				if valid {
					cases[method] = clause
				}
			}
		}

		return false
	})

	return cases, defaultClause
}

func singleTopLevelMethodSwitch(function *ast.FuncDecl) bool {
	if function == nil || function.Body == nil || len(function.Body.List) != 1 {
		return false
	}

	switchStatement, ok := function.Body.List[0].(*ast.SwitchStmt)

	return ok && isMethodSwitch(switchStatement.Tag)
}

func isMethodSwitch(expression ast.Expr) bool {
	selector, ok := unparen(expression).(*ast.SelectorExpr)
	if !ok || (selector.Sel.Name != "Method" && selector.Sel.Name != "method") {
		return false
	}

	identifier := rootIdentifier(selector.X)

	return identifier != nil && (identifier.Name == "message" || identifier.Name == "envelope")
}

func expectedFramePairs(routes map[string][]SignalingRoute) map[string]map[signalingFramePair]bool {
	expected := make(map[string]map[signalingFramePair]bool)
	for method, candidates := range routes {
		expected[method] = make(map[signalingFramePair]bool)
		for _, route := range candidates {
			expected[method][signalingFramePair{frame: route.Frame, body: route.Body}] = true
		}
	}

	return expected
}

func collectFramePairs(
	node ast.Node,
	method string,
	functions map[string]*ast.FuncDecl,
	imports map[string]string,
	protocolValues map[string]string,
	contracts Contracts,
	visited map[string]bool,
) map[signalingFramePair]bool {
	pairs := make(map[signalingFramePair]bool)

	ast.Inspect(node, func(candidate ast.Node) bool {
		call, ok := candidate.(*ast.CallExpr)
		if !ok {
			return true
		}

		identifier, isIdentifier := call.Fun.(*ast.Ident)
		if isIdentifier && identifier.Name == generatedFrameMarshallerName {
			for pair := range framePairFromGeneratedCall(call, method, imports, protocolValues, contracts) {
				pairs[pair] = true
			}

			return true
		}

		if isIdentifier && functions[identifier.Name] != nil && !visited[identifier.Name] {
			visited[identifier.Name] = true
			for pair := range collectFramePairs(
				functions[identifier.Name].Body,
				method,
				functions,
				imports,
				protocolValues,
				contracts,
				visited,
			) {
				pairs[pair] = true
			}

			return true
		}

		return true
	})

	return pairs
}

func framePairFromGeneratedCall(
	call *ast.CallExpr,
	method string,
	imports map[string]string,
	protocolValues map[string]string,
	contracts Contracts,
) map[signalingFramePair]bool {
	pairs := make(map[signalingFramePair]bool)
	if len(call.Args) < 2 {
		return pairs
	}

	callMethod, valid := signalingMethod(call.Args[1], imports, protocolValues)

	if !valid || callMethod != method {
		return pairs
	}

	callback, ok := call.Args[len(call.Args)-1].(*ast.FuncLit)
	if !ok || callback.Type.Params == nil || len(callback.Type.Params.List) == 0 {
		return pairs
	}

	bodyType := generatedSelectorType(callback.Type.Params.List[0].Type, imports, contracts)
	if bodyType == "" {
		return pairs
	}

	ast.Inspect(callback.Body, func(node ast.Node) bool {
		composite, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}

		frameType := generatedSelectorType(composite.Type, imports, contracts)
		if frameType != "" && strings.HasSuffix(frameType, "Frame") {
			if !frameMethodMatches(composite, method, imports, protocolValues) {
				return true
			}

			frameName := schemaFrameName(frameType, contracts)
			bodyName := schemaFrameName(bodyType, contracts)
			pairs[signalingFramePair{frame: frameName, body: bodyName}] = true
		}

		return true
	})

	return pairs
}

func frameMethodMatches(
	frame *ast.CompositeLit,
	method string,
	imports map[string]string,
	protocolValues map[string]string,
) bool {
	for _, element := range frame.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}

		name, ok := field.Key.(*ast.Ident)

		if !ok || name.Name != "Method" {
			continue
		}

		value, valid := signalingMethod(field.Value, imports, protocolValues)

		return valid && value == method
	}

	return false
}

func generatedFrameMarshallerSafe(function *ast.FuncDecl, imports map[string]string) bool {
	if function == nil || function.Body == nil {
		return false
	}

	decodedBody := false
	marshaledFrame := false
	safeReturns := true
	marshalOutputs := generatedMarshalOutputs(function, imports)
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		packagePath, imported, shadowed := selectorImport(selector, imports)

		if !imported || shadowed || packagePath != jsonPackageImportPath {
			return true
		}

		switch selector.Sel.Name {
		case jsonUnmarshalMethodName:
			decodedBody = decodedBody || unmarshalTargetsBody(call)
		case "Marshal":
			marshaledFrame = marshaledFrame || marshalUsesGeneratedBuilder(call)
		}

		return true
	})

	ast.Inspect(function.Body, func(node ast.Node) bool {
		returned, ok := node.(*ast.ReturnStmt)
		if !ok {
			return true
		}

		if isGeneratedFrameMarshalReturn(returned, marshalOutputs, imports) || isMarshalErrorReturn(returned) {
			return true
		}

		safeReturns = false

		return false
	})

	if bodyIsMutated(function) {
		safeReturns = false
	}

	return decodedBody && marshaledFrame && safeReturns
}

func generatedMarshalOutputs(function *ast.FuncDecl, imports map[string]string) map[string]bool {
	outputs := make(map[string]bool)

	ast.Inspect(function.Body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok || len(assignment.Lhs) == 0 || len(assignment.Rhs) != 1 {
			return true
		}

		call, ok := assignment.Rhs[0].(*ast.CallExpr)

		if !ok || !isJSONMarshalOfGeneratedBuilder(call, imports) {
			return true
		}

		identifier, ok := assignment.Lhs[0].(*ast.Ident)
		if ok {
			outputs[identifier.Name] = true
		}

		return true
	})

	return outputs
}

func bodyIsMutated(function *ast.FuncDecl) bool {
	mutated := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		switch statement := node.(type) {
		case *ast.AssignStmt:
			for _, left := range statement.Lhs {
				identifier := rootIdentifier(left)
				if identifier != nil && identifier.Name == "body" {
					mutated = true
				}
			}
		case *ast.IncDecStmt:
			identifier := rootIdentifier(statement.X)
			mutated = identifier != nil && identifier.Name == "body"
		}

		return !mutated
	})

	return mutated
}

func unmarshalTargetsBody(call *ast.CallExpr) bool {
	if len(call.Args) != 2 {
		return false
	}

	source, sourceOK := call.Args[0].(*ast.SelectorExpr)
	if !sourceOK || source.Sel.Name != "Body" || rootIdentifier(source.X) == nil ||
		rootIdentifier(source.X).Name != "message" {
		return false
	}

	return destinationIs(call.Args[1], "body")
}

func marshalUsesGeneratedBuilder(call *ast.CallExpr) bool {
	if len(call.Args) != 1 {
		return false
	}

	built, ok := call.Args[0].(*ast.CallExpr)

	if !ok {
		return false
	}

	builder, ok := built.Fun.(*ast.Ident)

	if !ok || builder.Name != "build" || len(built.Args) != 1 {
		return false
	}

	address, ok := built.Args[0].(*ast.UnaryExpr)

	if !ok || address.Op != token.AND {
		return false
	}

	body, ok := address.X.(*ast.Ident)

	return ok && body.Name == "body"
}

func isJSONMarshalOfGeneratedBuilder(call *ast.CallExpr, imports map[string]string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Marshal" {
		return false
	}

	packagePath, imported, shadowed := selectorImport(selector, imports)

	return imported && !shadowed && packagePath == "encoding/json" && marshalUsesGeneratedBuilder(call)
}

func isGeneratedFrameMarshalReturn(
	returned *ast.ReturnStmt,
	outputs map[string]bool,
	imports map[string]string,
) bool {
	if len(returned.Results) == 2 {
		encoded, encodedOK := returned.Results[0].(*ast.Ident)
		nilValue, nilOK := returned.Results[1].(*ast.Ident)

		return encodedOK && nilOK && nilValue.Name == "nil" && outputs[encoded.Name]
	}

	if len(returned.Results) != 1 {
		return false
	}

	call, ok := returned.Results[0].(*ast.CallExpr)

	if !ok {
		return false
	}

	return isJSONMarshalOfGeneratedBuilder(call, imports)
}

func isMarshalErrorReturn(returned *ast.ReturnStmt) bool {
	if len(returned.Results) != 2 {
		return false
	}

	first, ok := returned.Results[0].(*ast.Ident)
	if !ok || first.Name != "nil" {
		return false
	}

	return expressionContainsError(returned.Results[1])
}
