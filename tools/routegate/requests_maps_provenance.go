package routegate

import (
	"go/ast"
	"net/http"
	"strings"
)

func (state *requestWireMapState) safeQueryAssignment(assignment *ast.AssignStmt, request *ast.Object) bool {
	for index, left := range assignment.Lhs {
		if index >= len(assignment.Rhs) || !isRequestRawQuery(left, request) {
			continue
		}

		info, found := state.mapInfo(assignment.Rhs[index], wireMapQuery)
		if !found || info.fromURLQuery || !info.valid || info.kind != wireMapQuery {
			return false
		}
	}

	return true
}

func (state *requestWireMapState) safeRequestHeaderAlias(assignment *ast.AssignStmt, request *ast.Object) bool {
	for index, left := range assignment.Lhs {
		if index >= len(assignment.Rhs) {
			continue
		}

		identifier, ok := unparen(left).(*ast.Ident)
		if !ok || identifier.Obj == nil || requestRoot(assignment.Rhs[index]) != request ||
			!requestHeaderExpression(assignment.Rhs[index], state.requests.routes) &&
				!headerConversionExpression(assignment.Rhs[index], request, state.context.imports, state.requests.routes) {
			continue
		}

		state.maps[identifier.Obj] = wireMapProvenance{
			kind:           wireMapHeader,
			valid:          true,
			requestHeaders: request,
			keys:           make(map[string]bool),
		}

		return true
	}

	return false
}

func headerConversionExpression(expression ast.Expr, request *ast.Object, imports map[string]string, requests map[*ast.Object]HTTPRoute) bool {
	call, ok := unparen(expression).(*ast.CallExpr)
	if !ok || len(call.Args) != 1 || requestRoot(call.Args[0]) != request {
		return false
	}

	selector, ok := unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Header" {
		return false
	}

	packagePath, imported, shadowed := selectorImport(selector, imports)

	return imported && !shadowed && packagePath == httpImportPath && requestHeaderExpression(call.Args[0], requests)
}

func headerMapConversion(call *ast.CallExpr, imports map[string]string, requests map[*ast.Object]HTTPRoute) bool {
	if len(call.Args) != 1 {
		return false
	}

	request := requestRoot(call.Args[0])

	return request != nil && headerConversionExpression(call, request, imports, requests)
}

func (state *requestWireMapState) mapInfo(expression ast.Expr, expected wireMapKind) (wireMapProvenance, bool) {
	expression = unparen(expression)
	switch value := expression.(type) {
	case *ast.Ident:
		if value.Obj == nil {
			return wireMapProvenance{}, false
		}

		if expected == wireMapQuery {
			if key, ok := state.queryKeys[value.Obj]; ok {
				return wireMapProvenance{kind: wireMapQuery, valid: true, keys: map[string]bool{key: true}}, true
			}
		}

		if expected == wireMapHeader {
			if key, ok := state.headerKeys[value.Obj]; ok {
				return wireMapProvenance{kind: wireMapHeader, valid: true, keys: map[string]bool{key: true}}, true
			}
		}

		info, found := state.maps[value.Obj]
		if found && (expected == wireMapUnknown || info.kind == expected) {
			return info, true
		}
	case *ast.SelectorExpr:
		if requestHeaderExpression(value, state.requests.routes) && (expected == wireMapUnknown || expected == wireMapHeader) {
			request := requestRoot(value.X)

			return wireMapProvenance{
				kind:           wireMapHeader,
				valid:          true,
				requestHeaders: request,
				keys:           make(map[string]bool),
			}, true
		}
	case *ast.CompositeLit:
		kind := mapKindFromType(value.Type, state.context.imports)
		if kind == wireMapUnknown {
			kind = expected
		}

		if kind == wireMapUnknown {
			kind = inferWireMapKind(value, state)
		}

		isMapLiteral := false
		if _, isMap := unparen(value.Type).(*ast.MapType); isMap {
			isMapLiteral = true
		}

		if kind == wireMapUnknown && !isMapLiteral && !looksLikeHeaderMap(value.Type) {
			return wireMapProvenance{}, false
		}

		info := wireMapProvenance{kind: kind, valid: true, keys: make(map[string]bool), literal: value}

		for _, element := range value.Elts {
			entry, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}

			keyKind := kind
			if keyKind == wireMapUnknown {
				keyKind = inferGeneratedKeyKind(entry.Key, state)
			}

			key, valid := state.generatedKey(entry.Key, keyKind)
			if !valid {
				info.valid = false

				continue
			}

			if info.kind == wireMapUnknown {
				info.kind = keyKind
			} else if keyKind != info.kind {
				info.valid = false
			}

			info.keys[key] = true
		}

		return info, true
	case *ast.CallExpr:
		if selector, ok := unparen(value.Fun).(*ast.SelectorExpr); ok && selector.Sel.Name == "Encode" && len(value.Args) == 0 {
			if info, found := state.mapInfo(selector.X, wireMapQuery); found {
				return info, true
			}
		}

		if kind := mapKindFromConversion(value, state.context.imports); kind != wireMapUnknown {
			if kind == wireMapHeader && len(value.Args) == 1 && requestHeaderExpression(value.Args[0], state.requests.routes) {
				request := requestRoot(value.Args[0])

				return wireMapProvenance{kind: wireMapHeader, valid: true, requestHeaders: request, keys: make(map[string]bool)}, true
			}

			if len(value.Args) == 1 {
				if info, found := state.mapInfo(value.Args[0], kind); found {
					return info, true
				}
			}
		}

		if querySourceExpression(value, state.context.imports, state.requests.routes) && (expected == wireMapUnknown || expected == wireMapQuery) {
			return wireMapProvenance{kind: wireMapQuery, valid: false, fromURLQuery: true, keys: make(map[string]bool)}, true
		}

		if isMakeMapCall(value, expected, state.context.imports) {
			return wireMapProvenance{kind: expected, valid: true, keys: make(map[string]bool)}, true
		}
	}

	return wireMapProvenance{}, false
}

func mapKindFromType(expression ast.Expr, imports map[string]string) wireMapKind {
	selector, ok := unparen(expression).(*ast.SelectorExpr)
	if !ok {
		return wireMapUnknown
	}

	packagePath, imported, shadowed := selectorImport(selector, imports)
	if !imported || shadowed {
		return wireMapUnknown
	}

	switch {
	case packagePath == "net/url" && selector.Sel.Name == "Values":
		return wireMapQuery
	case packagePath == httpImportPath && selector.Sel.Name == "Header":
		return wireMapHeader
	default:
		return wireMapUnknown
	}
}

func mapKindFromConversion(call *ast.CallExpr, imports map[string]string) wireMapKind {
	selector, ok := unparen(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return wireMapUnknown
	}

	return mapKindFromType(selector, imports)
}

func isWireMapType(expression ast.Expr, imports map[string]string) bool {
	return mapKindFromType(expression, imports) != wireMapUnknown
}

func looksLikeHeaderMap(expression ast.Expr) bool {
	identifier := rootIdentifier(expression)

	return identifier != nil && (strings.Contains(strings.ToLower(identifier.Name), "header") || strings.Contains(strings.ToLower(identifier.Name), "custom"))
}

func isMakeMapCall(call *ast.CallExpr, expected wireMapKind, imports map[string]string) bool {
	identifier, ok := unparen(call.Fun).(*ast.Ident)
	if !ok || identifier.Name != "make" || len(call.Args) == 0 {
		return false
	}

	kind := mapKindFromType(call.Args[0], imports)

	return kind != wireMapUnknown && (expected == wireMapUnknown || expected == kind)
}

func querySourceExpression(call *ast.CallExpr, imports map[string]string, requests map[*ast.Object]HTTPRoute) bool {
	selector, ok := unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || len(call.Args) != 0 || selector.Sel.Name != "Query" {
		return false
	}

	if requestURLExpression(selector.X, requests) {
		return true
	}

	packagePath, imported, shadowed := selectorImport(selector, imports)

	return imported && !shadowed && packagePath == "net/url"
}

func isRequestRawQuery(expression ast.Expr, request *ast.Object) bool {
	selectors := selectorChain(expression)

	return requestRoot(expression) == request && len(selectors) == 2 && selectors[0] == "URL" && selectors[1] == rawQueryFieldName
}

func inferWireMapKind(literal *ast.CompositeLit, state *requestWireMapState) wireMapKind {
	for _, element := range literal.Elts {
		entry, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}

		if kind := inferGeneratedKeyKind(entry.Key, state); kind != wireMapUnknown {
			return kind
		}
	}

	return wireMapUnknown
}

func inferGeneratedKeyKind(expression ast.Expr, state *requestWireMapState) wireMapKind {
	if _, ok := state.generatedKey(expression, wireMapQuery); ok {
		return wireMapQuery
	}

	if _, ok := state.generatedKey(expression, wireMapHeader); ok {
		return wireMapHeader
	}

	return wireMapUnknown
}

func (state *requestWireMapState) generatedKey(expression ast.Expr, kind wireMapKind) (string, bool) {
	if conversion, ok := unparen(expression).(*ast.CallExpr); ok && len(conversion.Args) == 1 {
		if name, builtin := unparen(conversion.Fun).(*ast.Ident); builtin && name.Name == "string" && name.Obj == nil {
			return state.generatedKey(conversion.Args[0], kind)
		}
	}

	if identifier, ok := unparen(expression).(*ast.Ident); ok && identifier.Obj != nil {
		var key string

		switch kind {
		case wireMapQuery:
			key = state.queryKeys[identifier.Obj]
		case wireMapHeader:
			key = state.headerKeys[identifier.Obj]
		case wireMapUnknown:
			return "", false
		}

		if key != "" && state.allowedKey(key, kind, nil) {
			return key, true
		}
	}

	selector, ok := unparen(expression).(*ast.SelectorExpr)
	if !ok {
		return "", false
	}

	packagePath, imported, shadowed := selectorImport(selector, state.context.imports)
	if !imported || shadowed || !hasGeneratedHTTPPath(state.context.contracts, packagePath) {
		return "", false
	}

	key, exists := state.registry.constants[packagePath][selector.Sel.Name]
	if !exists || !state.allowedKey(key, kind, nil) {
		return "", false
	}

	return key, true
}

func (state *requestWireMapState) generatedKeyIsAllowed(expression ast.Expr, kind wireMapKind, request *ast.Object) bool {
	if identifier, ok := unparen(expression).(*ast.Ident); ok && identifier.Obj != nil {
		if ranged, exists := state.rangeKeys[identifier.Obj]; exists && ranged.kind == kind && ranged.valid {
			for key := range ranged.keys {
				if !state.allowedKey(key, kind, request) {
					return false
				}
			}

			return len(ranged.keys) > 0
		}
	}

	key, exists := state.generatedKey(expression, kind)

	return exists && state.allowedKey(key, kind, request)
}

func (state *requestWireMapState) allowedKey(key string, kind wireMapKind, request *ast.Object) bool {
	if kind == wireMapHeader {
		key = http.CanonicalHeaderKey(key)
		if state.registry.globalHeaders[key] {
			return true
		}
	}

	allowed := state.allowed

	if request != nil {
		if route := state.requests.routes[request]; route.OperationID != "" {
			allowed = state.registry.operations[route.OperationID]
		}
	}

	if kind == wireMapQuery {
		return allowed.query[key]
	}

	if kind == wireMapHeader {
		return allowed.header[key] || state.registry.globalHeaders[key]
	}

	return false
}

func (state *requestWireMapState) literalKeysAreGenerated(expression ast.Expr, kind wireMapKind) bool {
	expression = unparen(expression)
	if identifier, ok := expression.(*ast.Ident); ok && identifier.Obj != nil {
		info, exists := state.maps[identifier.Obj]

		return exists && info.kind == kind && info.valid
	}

	literal, ok := expression.(*ast.CompositeLit)
	if !ok {
		return false
	}

	for _, element := range literal.Elts {
		entry, ok := element.(*ast.KeyValueExpr)
		if !ok {
			return false
		}

		if _, valid := state.generatedKey(entry.Key, kind); !valid {
			return false
		}
	}

	return true
}

func (state *requestWireMapState) rejectUnmodeledKey(node ast.Node, kind wireMapKind) {
	if kind == wireMapQuery {
		state.add(node, "unmodeled-query-key", "query write does not use a generated QueryParam constant declared by this OpenAPI operation")

		return
	}

	state.add(node, "unmodeled-header-key", "header write does not use a generated Header constant declared by this OpenAPI operation")
}

func (state *requestWireMapState) rejectMapEscape(node ast.Node, message string) {
	state.add(node, "schema-keyed-map-escape", message)
}

func (state *requestWireMapState) add(node ast.Node, rule, message string) {
	state.context.add(node, rule, message)
}

func (state *requestWireMapState) invalidateMapExpression(expression ast.Expr) {
	if identifier, ok := unparen(expression).(*ast.Ident); ok && identifier.Obj != nil {
		if info, found := state.maps[identifier.Obj]; found {
			info.valid = false
			state.maps[identifier.Obj] = info
		}
	}
}

func (state *requestWireMapState) packageObject(expression ast.Expr) bool {
	identifier, ok := unparen(expression).(*ast.Ident)
	if !ok {
		return false
	}

	if identifier.Obj != nil && state.packageObjects[identifier.Obj] {
		return true
	}

	return identifier.Obj == nil && state.packageNames[identifier.Name]
}

func isAggregateMapStorage(expression ast.Expr) bool {
	switch unparen(expression).(type) {
	case *ast.SelectorExpr, *ast.IndexExpr, *ast.IndexListExpr:
		return true
	default:
		return false
	}
}

func (state *requestWireMapState) auditRawQueryAssignment(assignment *ast.AssignStmt, request *ast.Object) bool {
	if state.safeQueryAssignment(assignment, request) {
		return true
	}

	state.add(assignment, "unmodeled-query-key", "outbound RawQuery must come from a generated QueryParam map for this operation")

	return false
}

func (state *requestWireMapState) auditHeaderAssignment(assignment *ast.AssignStmt, request *ast.Object) bool {
	for index, left := range assignment.Lhs {
		if index >= len(assignment.Rhs) || !isRequestHeaderMap(left, request) {
			continue
		}

		info, found := state.mapInfo(assignment.Rhs[index], wireMapHeader)
		if !found || !info.valid || info.fromURLQuery {
			state.add(assignment, "unmodeled-header-key", "outbound request Header must come from a generated Header-keyed map")

			return false
		}
	}

	return true
}

func isRequestHeaderMap(expression ast.Expr, request *ast.Object) bool {
	selectors := selectorChain(expression)

	return requestRoot(expression) == request && len(selectors) == 1 && selectors[0] == "Header"
}
