package routegate

import "go/ast"

func (state *requestWireMapState) audit() {
	state.discoverAliasesAndStores()
	state.auditMapWritesAndEscapes()
}

func (state *requestWireMapState) discoverAliasesAndStores() {
	ast.Inspect(state.function.Body, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}

		switch value := node.(type) {
		case *ast.ValueSpec:
			state.discoverValueSpec(value)
		case *ast.AssignStmt:
			state.discoverAssignment(value)
		case *ast.RangeStmt:
			state.discoverHeaderRange(value)
		case *ast.ReturnStmt:
			state.auditMapReturn(value)
		case *ast.CompositeLit:
			state.auditAggregateMapStorage(value)
		}

		return true
	})
}

func (state *requestWireMapState) discoverValueSpec(value *ast.ValueSpec) {
	for index, name := range value.Names {
		if index >= len(value.Values) || name.Obj == nil {
			continue
		}

		state.bindMap(name.Obj, value.Values[index], name, false)
		state.bindGeneratedKey(name.Obj, value.Values[index])
	}
}

func (state *requestWireMapState) discoverAssignment(assignment *ast.AssignStmt) {
	for index, left := range assignment.Lhs {
		if index >= len(assignment.Rhs) {
			continue
		}

		right := assignment.Rhs[index]

		if identifier, ok := unparen(left).(*ast.Ident); ok && identifier.Obj != nil {
			if state.packageObject(left) {
				if _, schemaBacked := state.mapInfo(right, wireMapUnknown); schemaBacked {
					state.rejectMapEscape(assignment, "schema-keyed map is stored in package state")
					state.invalidateMapExpression(right)
				}

				continue
			}

			state.bindMap(identifier.Obj, right, assignment, true)
			state.bindGeneratedKey(identifier.Obj, right)

			continue
		}

		if mapInfo, schemaBacked := state.mapInfo(right, wireMapUnknown); schemaBacked {
			request := requestRoot(left)
			if request != nil && state.requests.routes[request].OperationID != "" && isRequestHeaderMap(left, request) {
				if !mapInfo.valid {
					state.add(assignment, "unmodeled-header-key", "outbound request Header must come from a generated Header-keyed map")
				}

				continue
			}

			if request != nil && state.requests.routes[request].OperationID != "" && isRequestRawQuery(left, request) {
				if !state.safeQueryAssignment(assignment, request) {
					state.add(assignment, "unmodeled-query-key", "outbound RawQuery must come from a generated QueryParam map for this operation")
				}

				continue
			}

			if state.packageObject(left) || isAggregateMapStorage(left) {
				state.rejectMapEscape(assignment, "schema-keyed map is stored outside the current function scope")
				state.invalidateMapExpression(right)
			}

			_ = mapInfo
		}
	}
}

func (state *requestWireMapState) bindMap(object *ast.Object, expression ast.Expr, node ast.Node, reassignment bool) {
	info, recognized := state.mapInfo(expression, wireMapUnknown)
	if recognized {
		if info.fromURLQuery {
			state.add(node, "untrusted-query-map-source", "URL.Query or url.ParseQuery results cannot be treated as generated outbound query maps")

			info.valid = false
		}

		if !info.valid {
			state.maps[object] = info

			return
		}

		if prior, exists := state.maps[object]; exists && reassignment && !sameWireMap(prior, info) {
			state.add(node, "schema-keyed-map-reassignment", "schema-keyed query/header map is reassigned from a different source")

			info.valid = false
		}

		state.maps[object] = info

		return
	}

	if _, tracked := state.maps[object]; tracked && reassignment {
		state.add(node, "schema-keyed-map-reassignment", "schema-keyed query/header map loses trust after reassignment from an untrusted source")
		delete(state.maps, object)
	}
}

func sameWireMap(left, right wireMapProvenance) bool {
	return left.kind == right.kind && left.requestHeaders == right.requestHeaders && left.valid == right.valid
}

func (state *requestWireMapState) bindGeneratedKey(object *ast.Object, expression ast.Expr) {
	if key, ok := state.generatedKey(expression, wireMapQuery); ok {
		state.queryKeys[object] = key
		delete(state.headerKeys, object)

		return
	}

	if key, ok := state.generatedKey(expression, wireMapHeader); ok {
		state.headerKeys[object] = key
		delete(state.queryKeys, object)

		return
	}

	delete(state.queryKeys, object)
	delete(state.headerKeys, object)
}

func (state *requestWireMapState) discoverHeaderRange(statement *ast.RangeStmt) {
	mapObject, ok := unparen(statement.X).(*ast.Ident)
	if !ok || mapObject.Obj == nil || !rangeWritesRequestHeader(statement, state.requests.routes) {
		return
	}

	info, exists := state.maps[mapObject.Obj]
	if !exists {
		info, exists = state.mapInfo(statement.X, wireMapHeader)
	}

	if !exists {
		return
	}

	info.kind = wireMapHeader
	info.valid = state.literalKeysAreGenerated(statement.X, wireMapHeader)

	state.maps[mapObject.Obj] = info

	if key, ok := unparen(statement.Key).(*ast.Ident); ok && key.Obj != nil {
		state.rangeKeys[key.Obj] = info
	}

	if !info.valid {
		state.add(statement, "unmodeled-header-key", "custom header map contains a key absent from the generated OpenAPI Header constants")
	}
}

func rangeWritesRequestHeader(statement *ast.RangeStmt, requests map[*ast.Object]HTTPRoute) bool {
	key, _ := unparen(statement.Key).(*ast.Ident)

	value, _ := unparen(statement.Value).(*ast.Ident)

	if key == nil || value == nil || key.Obj == nil || value.Obj == nil {
		return false
	}

	found := false

	ast.Inspect(statement.Body, func(node ast.Node) bool {
		if assignment, ok := node.(*ast.AssignStmt); ok {
			for index, left := range assignment.Lhs {
				if index >= len(assignment.Rhs) {
					continue
				}

				indexed, ok := unparen(left).(*ast.IndexExpr)
				if !ok || !requestHeaderExpression(indexed.X, requests) {
					continue
				}

				keyIdentifier, keyIsKey := unparen(indexed.Index).(*ast.Ident)

				right, valueIsValue := unparen(assignment.Rhs[index]).(*ast.Ident)

				if keyIsKey && valueIsValue && keyIdentifier.Obj == key.Obj && right.Obj == value.Obj {
					found = true
				}
			}
		}

		return !found
	})

	return found
}

func requestHeaderExpression(expression ast.Expr, requests map[*ast.Object]HTTPRoute) bool {
	expression = unparen(expression)

	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Header" {
		return false
	}

	request := requestRoot(selector.X)

	return request != nil && requests[request].OperationID != ""
}

func (state *requestWireMapState) auditMapWritesAndEscapes() {
	parents := parentNodes(state.function.Body)
	ast.Inspect(state.function.Body, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}

		switch value := node.(type) {
		case *ast.AssignStmt:
			state.auditIndexedMapWrite(value)
		case *ast.CallExpr:
			state.auditMapCall(value)
			state.auditMapCallArguments(value)
		case *ast.SelectorExpr:
			if _, call := parents[value].(*ast.CallExpr); call {
				return true
			}

			if _, mapExpression := state.mapInfo(value.X, wireMapUnknown); mapExpression {
				state.rejectMapEscape(value, "schema-keyed query/header map method value can bypass key validation")
			}
		case *ast.CompositeLit:
			state.auditAggregateMapStorage(value)
		}

		return true
	})
}

func (state *requestWireMapState) auditIndexedMapWrite(assignment *ast.AssignStmt) {
	for _, left := range assignment.Lhs {
		indexed, ok := unparen(left).(*ast.IndexExpr)
		if !ok {
			continue
		}

		info, found := state.mapInfo(indexed.X, wireMapUnknown)
		if !found {
			continue
		}

		if info.fromURLQuery || !info.valid {
			state.rejectMapEscape(assignment, "untrusted query/header map is used for an outbound write")

			continue
		}

		if !state.generatedKeyIsAllowed(indexed.Index, info.kind, info.requestHeaders) {
			state.rejectUnmodeledKey(assignment, info.kind)
		}
	}
}

func (state *requestWireMapState) auditMapCall(call *ast.CallExpr) {
	selector, ok := unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || !isMapWriteMethod(selector.Sel.Name) {
		return
	}

	info, found := state.mapInfo(selector.X, wireMapUnknown)
	if !found {
		return
	}

	if info.fromURLQuery || !info.valid {
		state.rejectMapEscape(call, "untrusted query/header map is used for an outbound write")

		return
	}

	if len(call.Args) == 0 || !state.generatedKeyIsAllowed(call.Args[0], info.kind, info.requestHeaders) {
		state.rejectUnmodeledKey(call, info.kind)
	}
}

func isMapWriteMethod(name string) bool {
	switch name {
	case "Set", "Add", "Del", "Delete", "Clear":
		return true
	default:
		return false
	}
}

func (state *requestWireMapState) auditMapCallArguments(call *ast.CallExpr) {
	if _, generated := generatedRequestRoute(call, state.context.imports, state.context.contracts); generated {
		return
	}

	if headerMapConversion(call, state.context.imports, state.requests.routes) {
		return
	}

	if selector, ok := unparen(call.Fun).(*ast.SelectorExpr); ok && selector.Sel.Name == "NewReader" {
		packagePath, imported, shadowed := selectorImport(selector, state.context.imports)
		if imported && !shadowed && packagePath == "strings" {
			return
		}
	}

	selector, _ := unparen(call.Fun).(*ast.SelectorExpr)
	if selector != nil && isMapWriteMethod(selector.Sel.Name) {
		return
	}

	if selector != nil && selector.Sel.Name == "Encode" && len(call.Args) == 0 {
		return
	}

	for _, argument := range call.Args {
		if state.containsSchemaMap(argument) {
			state.rejectMapEscape(call, "schema-keyed query/header map escapes to an unverified helper argument")
		}
	}
}

func (state *requestWireMapState) containsSchemaMap(expression ast.Expr) bool {
	if _, found := state.mapInfo(expression, wireMapUnknown); found {
		return true
	}

	contains := false

	ast.Inspect(expression, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}

		if identifier, ok := node.(*ast.Ident); ok && identifier.Obj != nil {
			if _, exists := state.maps[identifier.Obj]; exists {
				contains = true

				return false
			}
		}

		if literal, ok := node.(*ast.CompositeLit); ok {
			if _, recognized := state.mapInfo(literal, wireMapUnknown); recognized {
				contains = true

				return false
			}
		}

		return !contains
	})

	return contains
}

func (state *requestWireMapState) auditMapReturn(statement *ast.ReturnStmt) {
	for _, result := range statement.Results {
		if state.containsSchemaMap(result) {
			state.rejectMapEscape(statement, "schema-keyed query/header map is returned from an unverified helper")
		}
	}
}

func (state *requestWireMapState) auditAggregateMapStorage(literal *ast.CompositeLit) {
	if isWireMapType(literal.Type, state.context.imports) {
		return
	}

	for _, element := range literal.Elts {
		if state.containsSchemaMapElement(element) {
			state.rejectMapEscape(literal, "schema-keyed query/header map is stored in an aggregate field or indexed element")
		}
	}
}

func (state *requestWireMapState) containsSchemaMapElement(expression ast.Expr) bool {
	if state.containsSchemaMap(expression) {
		return true
	}

	contains := false

	ast.Inspect(expression, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if ok && isWireMapType(literal.Type, state.context.imports) {
			info, found := state.mapInfo(literal, wireMapUnknown)
			if found && len(info.keys) > 0 {
				contains = true

				return false
			}
		}

		return !contains
	})

	return contains
}
