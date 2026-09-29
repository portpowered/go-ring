package routegate

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

const (
	sendTypedMinimumArgumentCount = 5
	sendMethodArgumentIndex       = 1
	sendTypedBodyArgumentIndex    = 4
	legacySendArgumentCount       = 3
	legacySendBodyArgumentIndex   = 2
	messageSendArgumentCount      = 2
)

func verifiedWebSocketCallsite(
	call *ast.CallExpr,
	helperName, channel string,
	imports map[string]string,
	parents map[ast.Node]ast.Node,
) bool {
	function := enclosingFunction(call, parents)

	urlIndex := 1
	if helperName == openEventsHelperName {
		urlIndex = 1
	}

	if function == nil || len(call.Args) <= urlIndex {
		return false
	}

	urlExpression := call.Args[urlIndex]
	strict, override := findWebSocketValidators(function, urlExpression, channel, imports)

	return strict != nil && override != nil &&
		validationPropagatedBefore(strict, call, function, parents) &&
		validationPropagatedBefore(override, call, function, parents)
}

func verifiedWebSocketDial(
	call *ast.CallExpr,
	parents map[ast.Node]ast.Node,
	packageFiles []*parsedGoFile,
	imports map[string]string,
) bool {
	function := enclosingFunction(call, parents)
	if function == nil || len(call.Args) < 2 {
		return false
	}

	urlExpression := call.Args[1]

	switch function.Name.Name {
	case openEventsWithDialerHelperName:
		strict, override := findWebSocketValidators(function, urlExpression, "accountEvent", imports)

		return strict != nil && override != nil &&
			validationPropagatedBefore(strict, call, function, parents) &&
			validationPropagatedBefore(override, call, function, parents)
	case dialSignalingHelperName:
		validator := packageFunction(packageFiles, "validateSignalingDialURL")
		if validator == nil || !webSocketValidatorReturns(validator, "serverEnvelope", imports) ||
			!webSocketOverrideValidatorReturns(validator, imports) {
			return false
		}

		return localValidationPropagatedBefore(function, "validateSignalingDialURL", urlExpression, call, parents)
	default:
		return false
	}
}

func findWebSocketValidators(
	function *ast.FuncDecl,
	urlExpression ast.Expr,
	channel string,
	imports map[string]string,
) (*ast.CallExpr, *ast.CallExpr) {
	var (
		strict   *ast.CallExpr
		override *ast.CallExpr
	)

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
		if !imported || shadowed || packagePath != protocolImportPath() {
			return true
		}

		switch selector.Sel.Name {
		case validateWebSocketURLName:
			if len(call.Args) == 2 && isProtocolChannel(call.Args[0], channel, imports) &&
				sameExpression(call.Args[1], urlExpression) {
				strict = call
			}
		case validateWebSocketOverrideName:
			if len(call.Args) == 1 && sameExpression(call.Args[0], urlExpression) {
				override = call
			}
		}

		return true
	})

	return strict, override
}

func webSocketValidatorReturns(function *ast.FuncDecl, channel string, imports map[string]string) bool {
	for _, call := range functionCalls(function) {
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != validateWebSocketURLName || len(call.Args) != 2 {
			continue
		}

		packagePath, imported, shadowed := selectorImport(selector, imports)
		if imported && !shadowed && packagePath == protocolImportPath() &&
			isProtocolChannel(call.Args[0], channel, imports) &&
			(isReturnedCall(call, function) || validatorFailureReturned(call, function)) {
			return true
		}
	}

	return false
}

func webSocketOverrideValidatorReturns(function *ast.FuncDecl, imports map[string]string) bool {
	for _, call := range functionCalls(function) {
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != validateWebSocketOverrideName || len(call.Args) != 1 {
			continue
		}

		packagePath, imported, shadowed := selectorImport(selector, imports)
		if imported && !shadowed && packagePath == protocolImportPath() &&
			(isReturnedCall(call, function) || validatorFailureReturned(call, function)) {
			return true
		}
	}

	return false
}

func validatorFailureReturned(call *ast.CallExpr, function *ast.FuncDecl) bool {
	parents := parentNodes(function.Body)
	object := assignedErrorObject(call, parents)

	if object == nil {
		return false
	}

	propagated := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		conditional, ok := node.(*ast.IfStmt)
		if !ok || !isErrorNotNil(conditional.Cond, object) || !hasImmediateReturn(conditional.Body) {
			return true
		}

		returned, ok := conditional.Body.List[0].(*ast.ReturnStmt)

		if ok && containsObject(returned, object) {
			propagated = true
		}

		return !propagated
	})

	return propagated
}

func isProtocolChannel(expression ast.Expr, expected string, imports map[string]string) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != protocolConstantForChannel(expected) {
		return false
	}

	packagePath, imported, shadowed := selectorImport(selector, imports)

	return imported && !shadowed && packagePath == protocolImportPath()
}

func protocolConstantForChannel(channel string) string {
	switch channel {
	case "serverEnvelope":
		return "SignalingChannel"
	case "accountEvent":
		return "AccountEventsChannel"
	default:
		return ""
	}
}

func localValidationPropagatedBefore(
	function *ast.FuncDecl,
	name string,
	urlExpression ast.Expr,
	sink *ast.CallExpr,
	parents map[ast.Node]ast.Node,
) bool {
	for _, call := range functionCalls(function) {
		identifier, ok := call.Fun.(*ast.Ident)
		if ok && identifier.Name == name && len(call.Args) == 1 && sameExpression(call.Args[0], urlExpression) &&
			validationPropagatedBefore(call, sink, function, parents) {
			return true
		}
	}

	return false
}

func validationPropagatedBefore(
	validation, sink *ast.CallExpr,
	function *ast.FuncDecl,
	parents map[ast.Node]ast.Node,
) bool {
	if validation.Pos() >= sink.Pos() {
		return false
	}

	object := assignedErrorObject(validation, parents)
	if object == nil {
		return false
	}

	verified := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		statement, ok := node.(*ast.IfStmt)
		if !ok || statement.End() > sink.Pos() || !hasImmediateReturn(statement.Body) ||
			!isErrorNotNil(statement.Cond, object) {
			return true
		}

		if statement.Init != nil && nodeContainsCall(statement.Init, validation) {
			verified = true

			return false
		}

		if statement.Pos() > validation.End() {
			verified = true

			return false
		}

		return true
	})

	return verified
}

func assignedErrorObject(call *ast.CallExpr, parents map[ast.Node]ast.Node) *ast.Object {
	assignment, ok := parents[call].(*ast.AssignStmt)
	if !ok || len(assignment.Lhs) != 1 {
		return nil
	}

	identifier, ok := assignment.Lhs[0].(*ast.Ident)
	if !ok {
		return nil
	}

	return identifier.Obj
}

func isErrorNotNil(expression ast.Expr, object *ast.Object) bool {
	comparison, ok := expression.(*ast.BinaryExpr)
	if !ok || comparison.Op != token.NEQ {
		return false
	}

	left, leftOK := comparison.X.(*ast.Ident)

	right, rightOK := comparison.Y.(*ast.Ident)

	if !leftOK || !rightOK {
		return false
	}

	return (left.Obj == object && right.Name == "nil") || (right.Obj == object && left.Name == "nil")
}

func hasImmediateReturn(block *ast.BlockStmt) bool {
	if len(block.List) == 0 {
		return false
	}

	_, ok := block.List[0].(*ast.ReturnStmt)

	return ok
}

func isReturnedCall(call *ast.CallExpr, function *ast.FuncDecl) bool {
	found := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		statement, ok := node.(*ast.ReturnStmt)
		if ok && nodeContainsCall(statement, call) {
			found = true

			return false
		}

		return !found
	})

	return found
}

func functionCalls(function *ast.FuncDecl) []*ast.CallExpr {
	var calls []*ast.CallExpr

	ast.Inspect(function.Body, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			calls = append(calls, call)
		}

		return true
	})

	return calls
}

func nodeContainsCall(node ast.Node, target *ast.CallExpr) bool {
	found := false

	ast.Inspect(node, func(candidate ast.Node) bool {
		if candidate == target {
			found = true

			return false
		}

		return !found
	})

	return found
}

func sameExpression(left, right ast.Expr) bool {
	left = unparen(left)
	right = unparen(right)

	switch leftValue := left.(type) {
	case *ast.Ident:
		rightValue, ok := right.(*ast.Ident)
		if !ok {
			return false
		}

		if leftValue.Obj != nil || rightValue.Obj != nil {
			return leftValue.Obj != nil && leftValue.Obj == rightValue.Obj
		}

		return leftValue.Name == rightValue.Name
	case *ast.SelectorExpr:
		rightValue, ok := right.(*ast.SelectorExpr)

		return ok && leftValue.Sel.Name == rightValue.Sel.Name && sameExpression(leftValue.X, rightValue.X)
	case *ast.BasicLit:
		rightValue, ok := right.(*ast.BasicLit)

		return ok && leftValue.Kind == rightValue.Kind && leftValue.Value == rightValue.Value
	default:
		return false
	}
}

func unparen(expression ast.Expr) ast.Expr {
	if parenthesized, ok := expression.(*ast.ParenExpr); ok {
		return unparen(parenthesized.X)
	}

	return expression
}

func packageFunction(files []*parsedGoFile, name string) *ast.FuncDecl {
	for _, file := range files {
		for _, declaration := range file.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if ok && function.Name.Name == name {
				return function
			}
		}
	}

	return nil
}

func auditWebSocketHelperValue(
	selector *ast.SelectorExpr,
	imports map[string]string,
	contracts Contracts,
	adapterVerified bool,
	add func(ast.Node, string, string),
) {
	packagePath, imported, shadowed := selectorImport(selector, imports)
	if !imported || !strings.HasSuffix(packagePath, "/pkg/dependencies/websocket") {
		return
	}

	if shadowed {
		add(
			selector,
			"shadowed-websocket-helper",
			selectorName(selector.X)+" resolves to a shadowing local, not the inventoried WebSocket package",
		)

		return
	}

	switch selector.Sel.Name {
	case dialSignalingHelperName, openEventsWithDialerHelperName, openEventsHelperName:
		address := "/ws"
		if selector.Sel.Name != dialSignalingHelperName {
			address = "/clients_api/ws"
		}

		if channelAddress(contracts, address) == "" {
			add(
				selector,
				"missing-async-channel",
				fmt.Sprintf("WebSocket helper %s has no AsyncAPI channel for %s", selector.Sel.Name, address),
			)
		} else {
			message := fmt.Sprintf(
				"WebSocket helper %s is stored as a value before its channel URL is checked",
				selector.Sel.Name,
			)
			add(selector, "unverified-websocket-url", message)
		}
	case writeSignalingHelperName:
		if !adapterVerified {
			add(
				selector,
				"handwritten-signaling-envelope",
				"WriteSignaling method value can serialize an unverified internal/signaling.Message",
			)
		}
	}
}

func auditSignalingSend(
	call *ast.CallExpr,
	selector *ast.SelectorExpr,
	imports map[string]string,
	contracts Contracts,
	protocolValues map[string]string,
	add func(ast.Node, string, string),
) {
	switch selector.Sel.Name {
	case sendTypedHelperName:
		if len(call.Args) < sendTypedMinimumArgumentCount {
			add(call, "unverified-signaling-frame", "sendTyped call has no statically recoverable method/body pair")

			return
		}

		checkSignalingPair(
			call,
			call.Args[sendMethodArgumentIndex],
			call.Args[sendTypedBodyArgumentIndex],
			imports,
			contracts,
			protocolValues,
			add,
		)
	case "Send":
		if len(call.Args) == legacySendArgumentCount {
			checkSignalingPair(
				call,
				call.Args[sendMethodArgumentIndex],
				call.Args[legacySendBodyArgumentIndex],
				imports,
				contracts,
				protocolValues,
				add,
			)

			return
		}

		if len(call.Args) == messageSendArgumentCount && isMessageComposite(call.Args[sendMethodArgumentIndex]) {
			add(
				call,
				"handwritten-signaling-envelope",
				"generic Send receives a hand-authored envelope and cannot prove its method/body schema pair",
			)
		}
	case "send":
		if len(call.Args) == messageSendArgumentCount && isMessageComposite(call.Args[sendMethodArgumentIndex]) {
			add(
				call,
				"handwritten-signaling-envelope",
				"generic send receives an internal/signaling.Message outside a generated frame operation",
			)
		}
	}
}

func checkSignalingPair(
	call ast.Node,
	methodExpression, bodyExpression ast.Expr,
	imports map[string]string,
	contracts Contracts,
	protocolValues map[string]string,
	add func(ast.Node, string, string),
) {
	method, methodOK := signalingMethod(methodExpression, imports, protocolValues)

	body, bodyPath, bodyOK := generatedBodyType(bodyExpression, imports, contracts)

	if !methodOK || !bodyOK {
		add(
			call,
			"unverified-signaling-frame",
			"outbound signaling call must use an imported protocol method constant and "+
				"a discovered generated AsyncAPI body type",
		)

		return
	}

	for _, route := range contracts.SignalingRoutes[method] {
		if route.Body == body && contracts.GeneratedFrames[body] == bodyPath {
			return
		}
	}

	add(
		call,
		"signaling-schema-mismatch",
		fmt.Sprintf("method %q with body %s is not an AsyncAPI send operation/frame pair", method, body),
	)
}

func signalingMethod(expression ast.Expr, imports map[string]string, protocolValues map[string]string) (string, bool) {
	for {
		parenthesized, ok := expression.(*ast.ParenExpr)
		if !ok {
			break
		}

		expression = parenthesized.X
	}

	if literal, ok := expression.(*ast.BasicLit); ok && literal.Kind == token.STRING {
		value, err := strconv.Unquote(literal.Value)

		return value, err == nil
	}

	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}

	packagePath, imported, shadowed := selectorImport(selector, imports)
	if !imported || shadowed || packagePath != protocolImportPath() {
		return "", false
	}

	value, exists := protocolValues[selector.Sel.Name]

	return value, exists
}

func generatedBodyType(
	expression ast.Expr,
	imports map[string]string,
	contracts Contracts,
) (string, string, bool) {
	for {
		switch value := expression.(type) {
		case *ast.ParenExpr:
			expression = value.X
		case *ast.UnaryExpr:
			if value.Op != token.AND {
				return "", "", false
			}

			expression = value.X
		default:
			goto resolved
		}
	}

resolved:
	composite, ok := expression.(*ast.CompositeLit)

	if !ok {
		return "", "", false
	}

	switch typeExpression := composite.Type.(type) {
	case *ast.SelectorExpr:
		packagePath, imported, shadowed := selectorImport(typeExpression, imports)
		if !imported || shadowed {
			return "", "", false
		}

		schemaName, generatedPath, exists := generatedSchemaName(typeExpression.Sel.Name, contracts)
		if !exists || generatedPath != packagePath {
			return "", "", false
		}

		return schemaName, packagePath, true
	case *ast.Ident:
		schemaName, generatedPath, exists := generatedSchemaName(typeExpression.Name, contracts)
		if !exists {
			return "", "", false
		}

		return schemaName, generatedPath, true
	default:
		return "", "", false
	}
}

func generatedSchemaName(goName string, contracts Contracts) (string, string, bool) {
	for name, candidatePath := range contracts.GeneratedFrames {
		if name == goName || strings.EqualFold(name, goName) {
			return name, candidatePath, true
		}
	}

	return "", "", false
}
