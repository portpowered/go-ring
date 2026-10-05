package routegate

import (
	"fmt"
	"go/ast"
)

const signalingPrimitiveValueRule = "unregistered-signaling-primitive-value"

func auditSchemaPrimitiveProvenance(
	root string,
	files []*parsedGoFile,
	protocolValues map[string]string,
) ([]Finding, error) {
	modelFields, err := generatedAsyncPrimitiveFields(root)
	if err != nil {
		return nil, err
	}

	groups := make(map[string][]*parsedGoFile)
	for _, file := range files {
		groups[sourcePackageKey(file)] = append(groups[sourcePackageKey(file)], file)
	}

	var findings []Finding

	for _, packageFiles := range groups {
		resolver, err := newScalarResolver(root, packageFiles, protocolValues, modelFields)
		if err != nil {
			return nil, err
		}

		for _, file := range packageFiles {
			findings = append(findings, auditPackageSignalingKeys(root, file, resolver)...)
			findings = append(findings, auditGeneratedPrimitiveValues(root, file, resolver)...)
		}
	}

	return findings, nil
}

func auditPackageSignalingKeys(root string, file *parsedGoFile, resolver *scalarResolver) []Finding {
	if !signalingSourcePackage(file.path) {
		return nil
	}

	parents := parentNodes(file.file)

	var findings []Finding

	seenFindings := make(map[ast.Node]bool)

	addKeyFinding := func(node ast.Node) {
		if seenFindings[node] {
			return
		}

		seenFindings[node] = true

		findings = append(findings, Finding{
			Path: relativePath(root, file.path), Line: file.fileSet.Position(node.Pos()).Line,
			Rule:    handwrittenSignalingKeyRule,
			Message: "library-defined signaling map keys must use generated protocol field constants",
		})
	}

	for _, declaration := range file.file.Decls {
		inspectSignalingKeyNode(declaration, nil, file, resolver, parents, addKeyFinding)
	}

	return findings
}

func inspectSignalingKeyNode(
	node ast.Node,
	context *scalarContext,
	file *parsedGoFile,
	resolver *scalarResolver,
	parents map[ast.Node]ast.Node,
	addFinding func(ast.Node),
) {
	switch value := node.(type) {
	case *ast.FuncDecl:
		for _, functionContext := range resolver.functionContexts(value) {
			inspectSignalingKeyBody(value.Body, functionContext, file, resolver, parents, addFinding)
		}

		return
	case *ast.FuncLit:
		inspectSignalingKeyFunction(value.Type, value.Body, context, file, resolver, parents, addFinding)

		return
	case *ast.IndexExpr:
		inspectSignalingKeyIndex(value, context, file, resolver, addFinding)
	case *ast.CompositeLit:
		inspectSignalingKeyComposite(value, context, file, resolver, parents, addFinding)
	case *ast.CallExpr:
		inspectSignalingKeyCall(value, context, file, resolver, addFinding)
	}

	for _, child := range childNodes(node) {
		inspectSignalingKeyNode(child, context, file, resolver, parents, addFinding)
	}
}

func inspectSignalingKeyFunction(
	signature *ast.FuncType,
	body *ast.BlockStmt,
	parent *scalarContext,
	file *parsedGoFile,
	resolver *scalarResolver,
	parents map[ast.Node]ast.Node,
	addFinding func(ast.Node),
) {
	if body == nil {
		return
	}

	context := resolver.newScalarContext(file, parent, signature, body)
	inspectSignalingKeyBody(body, context, file, resolver, parents, addFinding)
}

func inspectSignalingKeyBody(
	body *ast.BlockStmt,
	context *scalarContext,
	file *parsedGoFile,
	resolver *scalarResolver,
	parents map[ast.Node]ast.Node,
	addFinding func(ast.Node),
) {
	if body == nil {
		return
	}

	for _, statement := range body.List {
		inspectSignalingKeyNode(statement, context, file, resolver, parents, addFinding)
	}
}

func inspectSignalingKeyIndex(
	expression *ast.IndexExpr,
	context *scalarContext,
	file *parsedGoFile,
	resolver *scalarResolver,
	addFinding func(ast.Node),
) {
	if !resolver.isSchemaKeyMapExpression(expression.X, context) {
		return
	}

	if origin := resolver.evaluate(expression.Index, context, file); origin.hardcoded || origin.unknown {
		addFinding(expression.Index)
	}
}

func inspectSignalingKeyComposite(
	literal *ast.CompositeLit,
	context *scalarContext,
	file *parsedGoFile,
	resolver *scalarResolver,
	parents map[ast.Node]ast.Node,
	addFinding func(ast.Node),
) {
	if resolver.isSchemaKeyMapType(literal.Type) {
		inspectSignalingKeyMapElements(literal, context, file, resolver, addFinding)
	}

	keyListType, ok := unparen(literal.Type).(*ast.ArrayType)
	if ok && keyListType.Len == nil && isStringIdent(keyListType.Elt) && isSignalingKeyList(literal, parents) {
		inspectSignalingKeyListElements(literal, context, file, resolver, addFinding)
	}
}

func inspectSignalingKeyMapElements(
	literal *ast.CompositeLit,
	context *scalarContext,
	file *parsedGoFile,
	resolver *scalarResolver,
	addFinding func(ast.Node),
) {
	for _, rawElement := range literal.Elts {
		pair, ok := rawElement.(*ast.KeyValueExpr)
		if !ok {
			continue
		}

		if origin := resolver.evaluate(pair.Key, context, file); origin.hardcoded || origin.unknown {
			addFinding(pair.Key)
		}
	}
}

func inspectSignalingKeyListElements(
	literal *ast.CompositeLit,
	context *scalarContext,
	file *parsedGoFile,
	resolver *scalarResolver,
	addFinding func(ast.Node),
) {
	for _, element := range literal.Elts {
		if origin := resolver.evaluate(element, context, file); origin.hardcoded || origin.unknown {
			addFinding(element)
		}
	}
}

func inspectSignalingKeyCall(
	call *ast.CallExpr,
	context *scalarContext,
	file *parsedGoFile,
	resolver *scalarResolver,
	addFinding func(ast.Node),
) {
	identifier, ok := unparen(call.Fun).(*ast.Ident)
	if !ok || identifier.Name != "delete" || len(call.Args) != 2 {
		return
	}

	if !resolver.isSchemaKeyMapExpression(call.Args[0], context) {
		return
	}

	key := call.Args[1]
	if origin := resolver.evaluate(key, context, file); origin.hardcoded || origin.unknown {
		addFinding(key)
	}
}

func isSignalingKeyList(literal *ast.CompositeLit, parents map[ast.Node]ast.Node) bool {
	current := ast.Node(literal)
	for current != nil {
		parent := parents[current]
		if call, ok := parent.(*ast.CallExpr); ok {
			switch function := unparen(call.Fun).(type) {
			case *ast.Ident:
				return isSignalingKeyValidator(function.Name)
			case *ast.SelectorExpr:
				return isSignalingKeyValidator(function.Sel.Name)
			}
		}

		current = parent
	}

	return false
}

func isSignalingKeyValidator(name string) bool {
	return name == generatedFrameMarshallerName || name == "validateRPCFields"
}

func auditGeneratedPrimitiveValues(root string, file *parsedGoFile, resolver *scalarResolver) []Finding {
	var findings []Finding

	seenFindings := make(map[ast.Node]bool)

	add := func(node ast.Node, field string) {
		if seenFindings[node] {
			return
		}

		seenFindings[node] = true

		findings = append(findings, Finding{
			Path: relativePath(root, file.path), Line: file.fileSet.Position(node.Pos()).Line,
			Rule: signalingPrimitiveValueRule,
			Message: fmt.Sprintf(
				"schema-constrained signaling field %s must use a generated protocol value or preserve caller input",
				field,
			),
		})
	}

	for _, declaration := range file.file.Decls {
		inspectGeneratedPrimitiveNode(declaration, nil, file, resolver, add)
	}

	return findings
}

func inspectGeneratedPrimitiveNode(
	node ast.Node,
	context *scalarContext,
	file *parsedGoFile,
	resolver *scalarResolver,
	addFinding func(ast.Node, string),
) {
	switch value := node.(type) {
	case *ast.FuncDecl:
		for _, functionContext := range resolver.functionContexts(value) {
			inspectGeneratedPrimitiveBody(value.Body, functionContext, file, resolver, addFinding)
		}

		return
	case *ast.FuncLit:
		inspectGeneratedPrimitiveFunction(value.Type, value.Body, context, file, resolver, addFinding)

		return
	case *ast.CompositeLit:
		inspectGeneratedPrimitiveComposite(value, context, file, resolver, addFinding)
	case *ast.AssignStmt:
		inspectGeneratedPrimitiveAssignment(value, context, file, resolver, addFinding)
	}

	for _, child := range childNodes(node) {
		inspectGeneratedPrimitiveNode(child, context, file, resolver, addFinding)
	}
}

func inspectGeneratedPrimitiveFunction(
	signature *ast.FuncType,
	body *ast.BlockStmt,
	parent *scalarContext,
	file *parsedGoFile,
	resolver *scalarResolver,
	addFinding func(ast.Node, string),
) {
	if body == nil {
		return
	}

	context := resolver.newScalarContext(file, parent, signature, body)
	inspectGeneratedPrimitiveBody(body, context, file, resolver, addFinding)
}

func inspectGeneratedPrimitiveBody(
	body *ast.BlockStmt,
	context *scalarContext,
	file *parsedGoFile,
	resolver *scalarResolver,
	addFinding func(ast.Node, string),
) {
	if body == nil {
		return
	}

	for _, statement := range body.List {
		inspectGeneratedPrimitiveNode(statement, context, file, resolver, addFinding)
	}
}

func inspectGeneratedPrimitiveComposite(
	literal *ast.CompositeLit,
	context *scalarContext,
	file *parsedGoFile,
	resolver *scalarResolver,
	addFinding func(ast.Node, string),
) {
	modelName := modelTypeName(literal.Type, file)

	for _, rawElement := range literal.Elts {
		pair, ok := rawElement.(*ast.KeyValueExpr)
		if !ok {
			continue
		}

		field, ok := unparen(pair.Key).(*ast.Ident)
		if !ok {
			continue
		}

		primitiveField, constrained := resolver.modelFields[modelName][field.Name]
		if !constrained {
			continue
		}

		if origin := resolver.evaluate(pair.Value, context, file); primitiveValueNeedsFinding(origin, primitiveField) {
			addFinding(pair.Value, field.Name)
		}
	}
}

func inspectGeneratedPrimitiveAssignment(
	assignment *ast.AssignStmt,
	context *scalarContext,
	file *parsedGoFile,
	resolver *scalarResolver,
	addFinding func(ast.Node, string),
) {
	for index, left := range assignment.Lhs {
		if index >= len(assignment.Rhs) {
			continue
		}

		selector, ok := unparen(left).(*ast.SelectorExpr)
		if !ok {
			continue
		}

		primitiveField, constrained := resolver.generatedFieldMetadata(
			selector.X, context, file, selector.Sel.Name,
		)
		if !constrained {
			continue
		}

		origin := resolver.evaluate(assignment.Rhs[index], context, file)
		if primitiveValueNeedsFinding(origin, primitiveField) {
			addFinding(assignment.Rhs[index], selector.Sel.Name)
		}
	}
}

func primitiveValueNeedsFinding(origin scalarOrigin, field schemaPrimitiveField) bool {
	if origin.unknown {
		return true
	}

	if origin.hardcoded {
		return !field.omitEmpty || field.pointer || !origin.emptyOnly
	}

	matchingGeneratedType := len(origin.generatedTypes) > 0

	for typeName := range origin.generatedTypes {
		if typeName != field.generatedType {
			return true
		}
	}

	for value := range origin.generatedValues {
		if !field.knownValues[value] {
			return true
		}
	}

	if origin.caller && !field.openValues && !matchingGeneratedType {
		return true
	}

	if origin.generated && len(origin.generatedValues) == 0 && len(origin.generatedTypes) == 0 {
		return true
	}

	return !origin.caller && !origin.generated
}

func (resolver *scalarResolver) generatedFieldMetadata(
	expression ast.Expr,
	context *scalarContext,
	file *parsedGoFile,
	fieldName string,
) (schemaPrimitiveField, bool) {
	modelName := modelTypeName(expression, file)
	if modelName == "" {
		if identifier, ok := unparen(expression).(*ast.Ident); ok && context != nil {
			symbol := context.lookupSymbol(identifier)
			for current := context; current != nil; current = current.parent {
				modelName = current.modelTypes[symbol]
				if modelName != "" {
					break
				}
			}
		}
	}

	field, exists := resolver.modelFields[modelName][fieldName]

	return field, exists
}

func childNodes(node ast.Node) []ast.Node {
	var children []ast.Node

	ast.Inspect(node, func(child ast.Node) bool {
		if child == nil {
			return false
		}

		if child != node {
			children = append(children, child)

			return false
		}

		return true
	})

	return children
}

func isStringIdent(expression ast.Expr) bool {
	identifier, ok := unparen(expression).(*ast.Ident)

	return ok && identifier.Name == "string"
}
