package routegate

import (
	"go/ast"
	"go/token"
	"go/types"
)

const generatedRequestBodyBackingRule = "generated-request-body-backing-mutation"
const bytesNewBufferName = "NewBuffer"
const bytesNewReaderName = "NewReader"

type bodyReaderState struct {
	mutable   map[types.Object]map[types.Object]bool
	immutable map[types.Object]bool
}

func auditGeneratedRequestBodyBacking(
	function *ast.FuncDecl,
	state generatedRequestState,
	context generatedRequestAuditContext,
) {
	identities := context.identities
	aliases := bodySliceAliasGroups(function, identities)
	readers := bodyReaderOrigins(function, context.imports, aliases, identities)

	for _, assignments := range state.initializers {
		for assignment := range assignments {
			auditRequestBodyAssignment(function, assignment, readers, aliases, identities, context)
		}
	}
}

func auditRequestBodyAssignment(
	function *ast.FuncDecl,
	assignment *ast.AssignStmt,
	readers bodyReaderState,
	aliases map[types.Object]map[types.Object]bool,
	identities *types.Info,
	context generatedRequestAuditContext,
) {
	for index, expression := range assignment.Rhs {
		if index >= len(assignment.Lhs) {
			continue
		}

		constructor, ok := unparen(expression).(*ast.CallExpr)
		if !ok {
			continue
		}

		route, generated := generatedRequestRoute(constructor, context.imports, context.contracts)
		if !generated || !route.HasBody {
			continue
		}

		requestIdentifier, ok := unparen(assignment.Lhs[index]).(*ast.Ident)
		if !ok {
			continue
		}

		request := identifierType(identities, requestIdentifier)
		if request == nil {
			context.add(constructor, generatedRequestBodyBackingRule,
				"schema-owned request body provenance cannot resolve the generated request value")

			continue
		}

		bodyExpression := constructor.Args[len(constructor.Args)-1]

		backing := generatedRequestReaderBacking(constructor, readers, aliases, context.imports, identities)
		if len(backing) == 0 {
			if expressionIsReader(bodyExpression, identities) &&
				!generatedRequestBodyIsImmutable(constructor, readers, context.imports, identities) {
				context.add(constructor, generatedRequestBodyBackingRule,
					"schema-owned request body reader has unresolved mutable backing provenance")
			}

			continue
		}

		send := verifiedGeneratedRequestSendPosition(function, request, identities, context.packageFiles)
		if !send.IsValid() {
			continue
		}

		readerObjects := generatedRequestReaderObjects(readers, backing)
		auditRequestBackingMutations(
			function, constructor, send, backing, readerObjects, context.imports, identities, context.add,
		)
	}
}

func bodySliceAliasGroups(
	function *ast.FuncDecl,
	identities *types.Info,
) map[types.Object]map[types.Object]bool {
	groups := make(map[types.Object]map[types.Object]bool)
	addEdge := func(left, right types.Object) {
		if left == nil || right == nil || left == right {
			return
		}

		if groups[left] == nil {
			groups[left] = make(map[types.Object]bool)
		}

		if groups[right] == nil {
			groups[right] = make(map[types.Object]bool)
		}

		groups[left][right] = true
		groups[right][left] = true
	}

	ast.Inspect(function.Body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.AssignStmt:
			for index, left := range value.Lhs {
				if index >= len(value.Rhs) {
					continue
				}

				addBodyAlias(left, value.Rhs[index], identities, addEdge)
			}
		case *ast.ValueSpec:
			for index, name := range value.Names {
				if index >= len(value.Values) {
					continue
				}

				addBodyAlias(name, value.Values[index], identities, addEdge)
			}
		}

		return true
	})

	return groups
}

func addBodyAlias(
	left, right ast.Expr,
	identities *types.Info,
	addEdge func(types.Object, types.Object),
) {
	leftIdentifier, ok := unparen(left).(*ast.Ident)
	if !ok {
		return
	}

	rightIdentifier := sliceAliasIdentifier(right)
	if rightIdentifier == nil {
		return
	}

	addEdge(identifierType(identities, leftIdentifier), identifierType(identities, rightIdentifier))
}

func sliceAliasIdentifier(expression ast.Expr) *ast.Ident {
	switch value := unparen(expression).(type) {
	case *ast.Ident:
		return value
	case *ast.SliceExpr:
		return sliceAliasIdentifier(value.X)
	default:
		return nil
	}
}

func bodyReaderOrigins(
	function *ast.FuncDecl,
	imports map[string]string,
	aliases map[types.Object]map[types.Object]bool,
	identities *types.Info,
) bodyReaderState {
	state := bodyReaderState{
		mutable:   make(map[types.Object]map[types.Object]bool),
		immutable: make(map[types.Object]bool),
	}

	for {
		changed := false

		ast.Inspect(function.Body, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.AssignStmt:
				for index, left := range value.Lhs {
					if index < len(value.Rhs) {
						changed = bindBodyReader(left, value.Rhs[index], imports, aliases, identities, &state) || changed
					}
				}
			case *ast.ValueSpec:
				for index, name := range value.Names {
					if index < len(value.Values) {
						changed = bindBodyReader(name, value.Values[index], imports, aliases, identities, &state) || changed
					}
				}
			}

			return true
		})

		if !changed {
			return state
		}
	}
}

func bindBodyReader(
	left ast.Expr,
	right ast.Expr,
	imports map[string]string,
	aliases map[types.Object]map[types.Object]bool,
	identities *types.Info,
	state *bodyReaderState,
) bool {
	identifier, ok := unparen(left).(*ast.Ident)
	if !ok {
		return false
	}

	object := identifierType(identities, identifier)
	if object == nil {
		return false
	}

	mutable, immutable := bodyReaderSource(right, imports, aliases, identities, *state)
	changed := mergeObjectSet(state.mutable, object, mutable)

	if immutable && !state.immutable[object] {
		state.immutable[object] = true
		changed = true
	}

	return changed
}

func bodyReaderSource(
	expression ast.Expr,
	imports map[string]string,
	aliases map[types.Object]map[types.Object]bool,
	identities *types.Info,
	state bodyReaderState,
) (map[types.Object]bool, bool) {
	mutable := make(map[types.Object]bool)

	switch value := unparen(expression).(type) {
	case *ast.Ident:
		object := identifierType(identities, value)
		mergeObjects(mutable, state.mutable[object])

		return mutable, state.immutable[object]
	case *ast.CallExpr:
		switch readerFactoryKind(value, imports) {
		case "bytes":
			if len(value.Args) == 1 {
				if identifier := sliceAliasIdentifier(value.Args[0]); identifier != nil {
					mergeObjects(mutable, aliasGroup(identifierType(identities, identifier), aliases))
				}
			}

			return mutable, false
		case "strings":
			return mutable, true
		default:
			return bodyReaderWrapperSource(value, imports, aliases, identities, state)
		}
	default:
		return mutable, false
	}
}

func bodyReaderWrapperSource(
	call *ast.CallExpr,
	imports map[string]string,
	aliases map[types.Object]map[types.Object]bool,
	identities *types.Info,
	state bodyReaderState,
) (map[types.Object]bool, bool) {
	selector, ok := unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || len(call.Args) == 0 {
		return make(map[types.Object]bool), false
	}

	packagePath, imported, shadowed := selectorImport(selector, imports)
	if !imported || shadowed || packagePath != "io" || selector.Sel.Name != "NopCloser" {
		return make(map[types.Object]bool), false
	}

	return bodyReaderSource(call.Args[0], imports, aliases, identities, state)
}

func expressionIsReader(expression ast.Expr, identities *types.Info) bool {
	if identities == nil {
		return false
	}

	typeAndValue, exists := identities.Types[expression]
	if !exists || typeAndValue.Type == nil {
		return false
	}

	methods := types.NewMethodSet(typeAndValue.Type)
	for index := range methods.Len() {
		if methods.At(index).Obj().Name() == "Read" {
			return true
		}
	}

	return false
}

func readerFactoryKind(call *ast.CallExpr, imports map[string]string) string {
	selector, ok := unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || (selector.Sel.Name != bytesNewReaderName && selector.Sel.Name != bytesNewBufferName) {
		return ""
	}

	packagePath, imported, shadowed := selectorImport(selector, imports)
	if !imported || shadowed {
		return ""
	}

	switch {
	case packagePath == "bytes" && selector.Sel.Name == bytesNewReaderName:
		return "bytes"
	case packagePath == "bytes" && selector.Sel.Name == bytesNewBufferName:
		return "bytes"
	case packagePath == "strings" && selector.Sel.Name == bytesNewReaderName:
		return "strings"
	default:
		return ""
	}
}

func generatedRequestReaderBacking(
	constructor *ast.CallExpr,
	readers bodyReaderState,
	aliases map[types.Object]map[types.Object]bool,
	imports map[string]string,
	identities *types.Info,
) map[types.Object]bool {
	backing, _ := bodyReaderSource(constructor.Args[len(constructor.Args)-1], imports, aliases, identities, readers)

	return backing
}

func generatedRequestBodyIsImmutable(
	constructor *ast.CallExpr,
	readers bodyReaderState,
	imports map[string]string,
	identities *types.Info,
) bool {
	if len(constructor.Args) == 0 {
		return false
	}

	_, immutable := bodyReaderSource(constructor.Args[len(constructor.Args)-1], imports, nil, identities, readers)

	return immutable
}

func generatedRequestReaderObjects(readers bodyReaderState, backing map[types.Object]bool) map[types.Object]bool {
	objects := make(map[types.Object]bool)

	for object, origins := range readers.mutable {
		for source := range origins {
			if backing[source] {
				objects[object] = true

				break
			}
		}
	}

	return objects
}

func aliasGroup(object types.Object, aliases map[types.Object]map[types.Object]bool) map[types.Object]bool {
	if object == nil {
		return nil
	}

	result := map[types.Object]bool{object: true}
	queue := []types.Object{object}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		for alias := range aliases[current] {
			if result[alias] {
				continue
			}

			result[alias] = true

			queue = append(queue, alias)
		}
	}

	return result
}

func mergeObjectSet(
	destination map[types.Object]map[types.Object]bool,
	key types.Object,
	source map[types.Object]bool,
) bool {
	if key == nil || len(source) == 0 {
		return false
	}

	if destination[key] == nil {
		destination[key] = make(map[types.Object]bool)
	}

	changed := false

	for object := range source {
		if destination[key][object] {
			continue
		}

		destination[key][object] = true
		changed = true
	}

	return changed
}

func mergeObjects(destination, source map[types.Object]bool) {
	for object := range source {
		destination[object] = true
	}
}

func verifiedGeneratedRequestSendPosition(
	function *ast.FuncDecl,
	request types.Object,
	identities *types.Info,
	packageFiles []*parsedGoFile,
) token.Pos {
	var send token.Pos

	ast.Inspect(function.Body, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}

		call, ok := node.(*ast.CallExpr)
		if !ok || !callUsesTypedObject(call, request, identities) ||
			!typedRequestSendExpression(call, request, identities, function, packageFiles) {
			return true
		}

		if !send.IsValid() || call.Pos() < send {
			send = call.Pos()
		}

		return true
	})

	return send
}

func callUsesTypedObject(call *ast.CallExpr, object types.Object, identities *types.Info) bool {
	for _, argument := range call.Args {
		found := false

		ast.Inspect(argument, func(node ast.Node) bool {
			identifier, ok := node.(*ast.Ident)
			if ok && identifierType(identities, identifier) == object {
				found = true

				return false
			}

			return !found
		})

		if found {
			return true
		}
	}

	return false
}

func typedRequestSendExpression(
	call *ast.CallExpr,
	request types.Object,
	identities *types.Info,
	function *ast.FuncDecl,
	packageFiles []*parsedGoFile,
) bool {
	selector, ok := unparen(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return false
	}

	if selector.Sel.Name == "Do" && len(call.Args) == 1 &&
		identifierTypeOfExpression(call.Args[0], identities) == request {
		return httpClientReceiverIsOwner(selector.X, function, packageFiles)
	}

	return selector.Sel.Name == generatedJSONHelperName &&
		verifiedRequestHelperCall(call, selector, function, packageFiles)
}

func identifierTypeOfExpression(expression ast.Expr, identities *types.Info) types.Object {
	identifier, ok := unparen(expression).(*ast.Ident)
	if !ok {
		return nil
	}

	return identifierType(identities, identifier)
}

func auditRequestBackingMutations(
	function *ast.FuncDecl,
	constructor *ast.CallExpr,
	send token.Pos,
	backing map[types.Object]bool,
	readerObjects map[types.Object]bool,
	imports map[string]string,
	identities *types.Info,
	add func(ast.Node, string, string),
) {
	if !constructor.Pos().IsValid() || !send.IsValid() || constructor.Pos() >= send {
		return
	}

	ast.Inspect(function.Body, func(node ast.Node) bool {
		if node == nil {
			return true
		}

		if literal, nested := node.(*ast.FuncLit); nested {
			if literal.Pos() > constructor.Pos() && literal.Pos() < send &&
				containsTypedObject(literal.Body, backing, readerObjects, identities) {
				add(literal, generatedRequestBodyBackingRule,
					"request body backing is captured by a callback before the verified send")
			}

			return false
		}

		if !node.Pos().IsValid() || node.Pos() <= constructor.Pos() || node.Pos() >= send ||
			node.Pos() > constructor.Pos() && node.End() <= constructor.End() {
			return true
		}

		if bodyBackingWrite(node, backing, identities) {
			add(node, generatedRequestBodyBackingRule,
				"mutable bytes backing a generated request body are changed after construction and before the verified send")
		}

		if bodyBackingEscape(node, backing, readerObjects, imports, identities) {
			add(node, generatedRequestBodyBackingRule,
				"mutable request body backing escapes before the verified send")
		}

		if bodyReaderMutation(node, readerObjects, identities) {
			add(node, generatedRequestBodyBackingRule,
				"mutable request body reader state changes before the verified send")
		}

		return true
	})
}

func bodyBackingWrite(node ast.Node, backing map[types.Object]bool, identities *types.Info) bool {
	switch value := node.(type) {
	case *ast.AssignStmt:
		for index, left := range value.Lhs {
			if index < len(value.Rhs) && isBackingIndex(left, backing, identities) {
				return true
			}

			identifier, ok := unparen(left).(*ast.Ident)
			if ok && backing[identifierType(identities, identifier)] && index < len(value.Rhs) &&
				callMutatesBacking(value.Rhs[index], backing, identities) {
				return true
			}
		}
	case *ast.IncDecStmt:
		return isBackingIndex(value.X, backing, identities)
	case *ast.CallExpr:
		return builtinMutatesBacking(value, backing, identities)
	}

	return false
}

func builtinMutatesBacking(call *ast.CallExpr, backing map[types.Object]bool, identities *types.Info) bool {
	identifier, ok := unparen(call.Fun).(*ast.Ident)
	if !ok || len(call.Args) == 0 {
		return false
	}

	object := identifierType(identities, identifier)
	builtin, ok := object.(*types.Builtin)

	if !ok || (builtin.Name() != "append" && builtin.Name() != "copy" && builtin.Name() != "clear") {
		return false
	}

	return containsBackingObject(call.Args[0], backing, identities)
}

func callMutatesBacking(expression ast.Expr, backing map[types.Object]bool, identities *types.Info) bool {
	call, ok := unparen(expression).(*ast.CallExpr)

	return ok && builtinMutatesBacking(call, backing, identities)
}

func bodyBackingEscape(
	node ast.Node,
	backing, readerObjects map[types.Object]bool,
	imports map[string]string,
	identities *types.Info,
) bool {
	switch value := node.(type) {
	case *ast.UnaryExpr:
		return value.Op == token.AND &&
			(containsBackingStorage(value.X, backing, identities) || isBackingIndex(value.X, backing, identities))
	case *ast.CompositeLit:
		return containsBackingStorage(value, backing, identities)
	case *ast.ReturnStmt:
		for _, result := range value.Results {
			if containsBackingStorage(result, backing, identities) {
				return true
			}
		}
	case *ast.AssignStmt:
		for index, right := range value.Rhs {
			if index >= len(value.Lhs) || !containsBackingStorage(right, backing, identities) {
				continue
			}

			left, ok := unparen(value.Lhs[index]).(*ast.Ident)
			if !ok || !backing[identifierType(identities, left)] {
				return true
			}
		}
	}

	call, ok := node.(*ast.CallExpr)
	if !ok || bodyReaderFactoryKind(call, imports) != "" || builtinReadOnlyCall(call, identities) {
		return false
	}

	return callHasBackingOrReader(call, backing, readerObjects, identities)
}

func bodyReaderFactoryKind(call *ast.CallExpr, imports map[string]string) string {
	return readerFactoryKind(call, imports)
}

func builtinReadOnlyCall(call *ast.CallExpr, identities *types.Info) bool {
	identifier, ok := unparen(call.Fun).(*ast.Ident)
	if !ok {
		return false
	}

	builtin, ok := identifierType(identities, identifier).(*types.Builtin)

	return ok && (builtin.Name() == "len" || builtin.Name() == "cap" || builtin.Name() == "string")
}

func callHasBackingOrReader(
	call *ast.CallExpr,
	backing, readers map[types.Object]bool,
	identities *types.Info,
) bool {
	for _, argument := range call.Args {
		if containsBackingStorage(argument, backing, identities) ||
			containsBackingStorage(argument, readers, identities) {
			return true
		}
	}

	return false
}

func bodyReaderMutation(node ast.Node, readers map[types.Object]bool, identities *types.Info) bool {
	call, ok := node.(*ast.CallExpr)
	if !ok {
		return false
	}

	selector, ok := unparen(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return false
	}

	identifier, ok := unparen(selector.X).(*ast.Ident)
	if !ok || !readers[identifierType(identities, identifier)] {
		return false
	}

	switch selector.Sel.Name {
	case "Len", "Size":
		return false
	default:
		return true
	}
}

func isBackingIndex(expression ast.Expr, backing map[types.Object]bool, identities *types.Info) bool {
	index, ok := unparen(expression).(*ast.IndexExpr)

	return ok && containsBackingObject(index.X, backing, identities)
}

func containsBackingObject(expression ast.Node, backing map[types.Object]bool, identities *types.Info) bool {
	return containsTypedObject(expression, backing, nil, identities)
}

func containsBackingStorage(expression ast.Expr, backing map[types.Object]bool, identities *types.Info) bool {
	switch value := unparen(expression).(type) {
	case *ast.Ident:
		return backing[identifierType(identities, value)]
	case *ast.SliceExpr:
		return containsBackingStorage(value.X, backing, identities)
	case *ast.CompositeLit:
		for _, element := range value.Elts {
			if containsBackingStorage(element, backing, identities) {
				return true
			}
		}
	case *ast.KeyValueExpr:
		return containsBackingStorage(value.Value, backing, identities)
	case *ast.StarExpr:
		return containsBackingStorage(value.X, backing, identities)
	default:
	}

	return false
}

func containsTypedObject(
	node ast.Node,
	first, second map[types.Object]bool,
	identities *types.Info,
) bool {
	contains := false

	ast.Inspect(node, func(child ast.Node) bool {
		identifier, ok := child.(*ast.Ident)
		if ok {
			object := identifierType(identities, identifier)
			contains = first[object] || second[object]
		}

		return !contains
	})

	return contains
}
