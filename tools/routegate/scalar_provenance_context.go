package routegate

import (
	"go/ast"
	"go/token"
)

func (resolver *scalarResolver) newScalarContext(
	file *parsedGoFile,
	parent *scalarContext,
	signature *ast.FuncType,
	body *ast.BlockStmt,
) *scalarContext {
	context := &scalarContext{
		file: file, parent: parent, body: body,
		symbols:      nil,
		declarations: make(map[*ast.Ident]*scalarSymbol),
		definitions:  make(map[*scalarSymbol][]scalarDefinition),
		bindings:     make(map[*scalarSymbol]scalarBinding),
		modelTypes:   make(map[*scalarSymbol]string),
		stringMaps:   make(map[*scalarSymbol]bool),
		rangeSources: make(map[*scalarSymbol]scalarRangeSource),
		namedResults: make(map[string]*scalarSymbol),
	}

	resolver.recordNamedResults(context, signature)
	resolver.recordScalarParameters(context, signature)
	resolver.recordScalarBody(context, body)

	return context
}

func (resolver *scalarResolver) recordNamedResults(context *scalarContext, signature *ast.FuncType) {
	if signature == nil || signature.Results == nil || context.body == nil {
		return
	}

	for _, result := range signature.Results.List {
		for _, name := range result.Names {
			symbol := newScalarSymbol(name.Name, context.body, context.body.Pos())
			context.addSymbol(symbol)
			context.declarations[name] = symbol
			context.namedResults[name.Name] = symbol
		}
	}
}

func (resolver *scalarResolver) recordScalarParameters(context *scalarContext, signature *ast.FuncType) {
	if signature == nil || signature.Params == nil || context.body == nil {
		return
	}

	for _, parameter := range signature.Params.List {
		modelName := modelTypeName(parameter.Type, context.file)
		isMap := resolver.isSchemaKeyMapType(parameter.Type)

		for _, name := range parameter.Names {
			if name.Name == "_" {
				continue
			}

			symbol := newScalarSymbol(name.Name, context.body, context.body.Pos())
			context.addSymbol(symbol)
			context.declarations[name] = symbol

			if !isFunctionType(parameter.Type) {
				context.bindings[symbol] = valueScalarBinding(unknownScalarOrigin())
			}

			if modelName != "" {
				context.modelTypes[symbol] = modelName
			}

			if isMap {
				context.stringMaps[symbol] = true
			}
		}
	}
}

func (resolver *scalarResolver) isCallerOwnedParameterType(expression ast.Expr, generatedType string) bool {
	if generatedType != "" && resolver.generatedTypes[generatedType] {
		return true
	}

	switch value := unparen(expression).(type) {
	case *ast.StarExpr:
		return resolver.isCallerOwnedParameterType(value.X, generatedType)
	case *ast.Ident:
		return resolver.structTypes[value.Name] || len(resolver.modelFields[generatedType]) > 0
	case *ast.SelectorExpr:
		return len(resolver.modelFields[generatedType]) > 0
	default:
		return false
	}
}

func (resolver *scalarResolver) markExternalCallerParameters(
	context *scalarContext,
	signature *ast.FuncType,
	allParameters bool,
) {
	if signature == nil || signature.Params == nil {
		return
	}

	for _, parameter := range signature.Params.List {
		modelName := modelTypeName(parameter.Type, context.file)
		if !allParameters && !resolver.isCallerOwnedParameterType(parameter.Type, modelName) {
			continue
		}

		if isFunctionType(parameter.Type) {
			continue
		}

		origin := callerScalarOrigin()
		if resolver.generatedTypes[modelName] {
			origin = combineScalarOrigins(origin, scalarTypeOrigin(modelName))
		}

		for _, name := range parameter.Names {
			if symbol := context.declarations[name]; symbol != nil {
				context.bindings[symbol] = valueScalarBinding(origin)
			}
		}
	}
}

func (resolver *scalarResolver) recordScalarBody(context *scalarContext, body *ast.BlockStmt) {
	if body == nil {
		return
	}

	parents := scalarNodeParents(body)
	ast.Inspect(body, func(node ast.Node) bool {
		if node != body {
			if _, nested := node.(*ast.FuncLit); nested {
				return false
			}
		}

		resolver.recordScalarNode(context, node, parents)

		return true
	})
}

func scalarNodeParents(root ast.Node) map[ast.Node]ast.Node {
	parents := make(map[ast.Node]ast.Node)
	stack := []ast.Node{}

	ast.Inspect(root, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]

			return false
		}

		if len(stack) > 0 {
			parents[node] = stack[len(stack)-1]
		}

		if node != root {
			if _, nested := node.(*ast.FuncLit); nested {
				return false
			}
		}

		stack = append(stack, node)

		return true
	})

	return parents
}

func (resolver *scalarResolver) recordScalarNode(
	context *scalarContext,
	node ast.Node,
	parents map[ast.Node]ast.Node,
) {
	switch value := node.(type) {
	case *ast.ValueSpec:
		resolver.prepareScalarValueSpec(context, value, parents)
		resolver.recordScalarValueSpec(context, value)
	case *ast.AssignStmt:
		resolver.prepareScalarAssignment(context, value, parents)
		resolver.recordScalarAssignment(context, value)
	case *ast.RangeStmt:
		resolver.prepareScalarRange(context, value)
		resolver.recordScalarRange(context, value)
	}
}

func newScalarSymbol(name string, scope ast.Node, visibleFrom token.Pos) *scalarSymbol {
	if name == "_" {
		return nil
	}

	return &scalarSymbol{
		name: name, scope: scope, scopeStart: scope.Pos(), scopeEnd: scope.End(),
		visibleFrom: visibleFrom,
	}
}

func (context *scalarContext) addSymbol(symbol *scalarSymbol) {
	if symbol != nil {
		context.symbols = append(context.symbols, symbol)
	}
}

func (context *scalarContext) lookupSymbol(identifier *ast.Ident) *scalarSymbol {
	if identifier == nil {
		return nil
	}

	for current := context; current != nil; current = current.parent {
		if symbol := current.lookupCurrentSymbol(identifier); symbol != nil {
			return symbol
		}
	}

	return nil
}

func (context *scalarContext) lookupCurrentSymbol(identifier *ast.Ident) *scalarSymbol {
	var (
		match            *scalarSymbol
		matchScopeLength token.Pos
	)

	for _, symbol := range context.symbols {
		if symbol.name != identifier.Name || identifier.Pos() < symbol.visibleFrom ||
			identifier.Pos() < symbol.scopeStart || identifier.Pos() > symbol.scopeEnd {
			continue
		}

		scopeLength := symbol.scopeEnd - symbol.scopeStart
		if match == nil || scopeLength < matchScopeLength ||
			(scopeLength == matchScopeLength && symbol.visibleFrom > match.visibleFrom) {
			match = symbol
			matchScopeLength = scopeLength
		}
	}

	return match
}

func scalarDeclarationScope(declaration ast.Node, parents map[ast.Node]ast.Node) ast.Node {
	for parent := parents[declaration]; parent != nil; parent = parents[parent] {
		switch value := parent.(type) {
		case *ast.IfStmt:
			if value.Init == declaration {
				return value
			}
		case *ast.ForStmt:
			if value.Init == declaration || value.Post == declaration {
				return value
			}
		case *ast.SwitchStmt:
			if value.Init == declaration {
				return value
			}
		case *ast.TypeSwitchStmt:
			if value.Init == declaration {
				return value
			}
		case *ast.BlockStmt, *ast.CaseClause, *ast.CommClause:
			return parent
		}
	}

	return declaration
}

func (resolver *scalarResolver) prepareScalarValueSpec(
	context *scalarContext,
	value *ast.ValueSpec,
	parents map[ast.Node]ast.Node,
) {
	scope := scalarDeclarationScope(value, parents)

	visibleFrom := value.End()
	for _, name := range value.Names {
		symbol := newScalarSymbol(name.Name, scope, visibleFrom)
		context.addSymbol(symbol)

		if symbol != nil {
			context.declarations[name] = symbol
		}
	}
}

func (resolver *scalarResolver) prepareScalarAssignment(
	context *scalarContext,
	value *ast.AssignStmt,
	parents map[ast.Node]ast.Node,
) {
	if value.Tok != token.DEFINE {
		return
	}

	scope := scalarDeclarationScope(value, parents)

	for _, left := range value.Lhs {
		identifier, ok := unparen(left).(*ast.Ident)
		if !ok || identifier.Name == "_" {
			continue
		}

		if symbol := context.symbolInScope(identifier.Name, scope, value.Pos()); symbol != nil {
			context.declarations[identifier] = symbol

			continue
		}

		symbol := newScalarSymbol(identifier.Name, scope, value.End())
		context.addSymbol(symbol)
		context.declarations[identifier] = symbol
	}
}

func (context *scalarContext) symbolInScope(name string, scope ast.Node, position token.Pos) *scalarSymbol {
	for index := len(context.symbols) - 1; index >= 0; index-- {
		symbol := context.symbols[index]
		if symbol.name == name && symbol.scope == scope && symbol.visibleFrom <= position {
			return symbol
		}
	}

	return nil
}

func (resolver *scalarResolver) prepareScalarRange(context *scalarContext, value *ast.RangeStmt) {
	if value.Tok != token.DEFINE {
		return
	}

	for _, expression := range []ast.Expr{value.Key, value.Value} {
		identifier, ok := unparen(expression).(*ast.Ident)
		if !ok || identifier.Name == "_" {
			continue
		}

		symbol := newScalarSymbol(identifier.Name, value.Body, value.Body.Pos())
		context.addSymbol(symbol)
		context.declarations[identifier] = symbol
	}
}

func (resolver *scalarResolver) recordScalarValueSpec(context *scalarContext, value *ast.ValueSpec) {
	for index, name := range value.Names {
		symbol := context.declarations[name]
		if symbol == nil {
			continue
		}

		if modelName := modelTypeName(value.Type, context.file); modelName != "" {
			context.modelTypes[symbol] = modelName
		}

		if resolver.isSchemaKeyMapType(value.Type) {
			context.stringMaps[symbol] = true
		}

		expressionIndex := index
		resultIndex := 0

		if len(value.Values) == 1 && len(value.Names) > 1 {
			expressionIndex = 0
			resultIndex = index
		} else if expressionIndex >= len(value.Values) {
			continue
		}

		expression := value.Values[expressionIndex]

		context.definitions[symbol] = append(context.definitions[symbol], scalarDefinition{
			file: context.file, expression: expression, resultIndex: resultIndex,
		})
		if resolver.isSchemaKeyMapExpression(expression, context) {
			context.stringMaps[symbol] = true
		}

		if modelName := resolverModelType(expression, context); modelName != "" {
			context.modelTypes[symbol] = modelName
		}
	}
}

func (resolver *scalarResolver) recordScalarAssignment(context *scalarContext, value *ast.AssignStmt) {
	for index, left := range value.Lhs {
		identifier, ok := unparen(left).(*ast.Ident)
		if !ok {
			continue
		}

		rightIndex := index
		resultIndex := 0

		if len(value.Rhs) == 1 && len(value.Lhs) > 1 {
			rightIndex = 0
			resultIndex = index
		} else if rightIndex >= len(value.Rhs) {
			continue
		}

		symbol := context.declarations[identifier]
		if symbol == nil {
			symbol = context.lookupSymbol(identifier)
		}

		if symbol == nil {
			symbol = resolver.globalSymbols[identifier.Name]
		}

		if symbol == nil {
			continue
		}

		expression := value.Rhs[rightIndex]

		context.definitions[symbol] = append(context.definitions[symbol], scalarDefinition{
			file: context.file, expression: expression, resultIndex: resultIndex,
		})
		if resolver.isSchemaKeyMapExpression(expression, context) {
			context.stringMaps[symbol] = true
		}

		if modelName := resolverModelType(expression, context); modelName != "" {
			context.modelTypes[symbol] = modelName
		} else if modelName := resolverModelType(value.Rhs[0], context); modelName != "" && len(value.Lhs) == 1 {
			context.modelTypes[symbol] = modelName
		}
	}
}

func (resolver *scalarResolver) recordScalarRange(context *scalarContext, value *ast.RangeStmt) {
	key, keyOK := unparen(value.Key).(*ast.Ident)
	if keyOK && key.Name != "_" {
		symbol := context.declarations[key]
		if symbol == nil {
			symbol = context.lookupSymbol(key)
		}

		if symbol == nil {
			symbol = resolver.globalSymbols[key.Name]
		}

		if symbol != nil {
			context.rangeSources[symbol] = scalarRangeSource{expression: value.X, key: true}
		}
	}

	rangeValue, valueOK := unparen(value.Value).(*ast.Ident)
	if valueOK && rangeValue.Name != "_" {
		symbol := context.declarations[rangeValue]
		if symbol == nil {
			symbol = context.lookupSymbol(rangeValue)
		}

		if symbol == nil {
			symbol = resolver.globalSymbols[rangeValue.Name]
		}

		if symbol != nil {
			context.rangeSources[symbol] = scalarRangeSource{expression: value.X, key: false}
		}
	}
}
