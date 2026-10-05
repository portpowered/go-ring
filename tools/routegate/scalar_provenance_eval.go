package routegate

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

func (resolver *scalarResolver) evaluate(expression ast.Expr, context *scalarContext, file *parsedGoFile) scalarOrigin {
	return resolver.evaluateAt(expression, context, file, 0)
}

func (resolver *scalarResolver) evaluateAt(
	expression ast.Expr,
	context *scalarContext,
	file *parsedGoFile,
	depth int,
) scalarOrigin {
	if expression == nil || depth > maxScalarResolutionDepth {
		return unknownScalarOrigin()
	}

	switch value := unparen(expression).(type) {
	case *ast.BasicLit:
		if value.Kind == token.STRING {
			text, err := strconv.Unquote(value.Value)

			return hardcodedScalarOrigin(err == nil && text == "")
		}

		if value.Kind == token.INT || value.Kind == token.FLOAT {
			return hardcodedScalarOrigin(false)
		}
	case *ast.Ident:
		return resolver.evaluateIdentifier(value, context, depth+1)
	case *ast.SelectorExpr:
		return resolver.evaluateSelector(value, context, file, depth+1)
	case *ast.IndexExpr:
		return resolver.evaluateGeneratedEnumLookup(value, file)
	case *ast.UnaryExpr:
		if value.Op == token.AND || value.Op == token.MUL {
			return resolver.evaluateAt(value.X, context, file, depth+1)
		}
	case *ast.BinaryExpr:
		if value.Op == token.ADD {
			return combineScalarOrigins(
				resolver.evaluateAt(value.X, context, file, depth+1),
				resolver.evaluateAt(value.Y, context, file, depth+1),
			)
		}
	case *ast.CallExpr:
		return resolver.evaluateCall(value, context, file, depth+1)
	case *ast.FuncLit:
		return resolver.evaluateFunctionLiteral(value, file, context, nil, file, context, depth+1)
	case *ast.ParenExpr:
		return resolver.evaluateAt(value.X, context, file, depth+1)
	}

	return unknownScalarOrigin()
}

func (resolver *scalarResolver) evaluateIdentifier(
	identifier *ast.Ident,
	context *scalarContext,
	depth int,
) scalarOrigin {
	if origin, found := resolver.scopedIdentifierOrigin(identifier, context, depth); found {
		return origin
	}

	if resolver.globalSymbols[identifier.Name] == nil && (identifier.Name == "true" || identifier.Name == "false") {
		return hardcodedScalarOrigin(false)
	}

	return resolver.globalIdentifierOrigin(identifier.Name, depth)
}

func (resolver *scalarResolver) scopedIdentifierOrigin(
	identifier *ast.Ident,
	context *scalarContext,
	depth int,
) (scalarOrigin, bool) {
	if context == nil {
		return unknownScalarOrigin(), false
	}

	if symbol := context.lookupSymbol(identifier); symbol != nil {
		origin, _ := resolver.contextIdentifierOrigin(symbol, context, depth)

		return origin, true
	}

	symbol := resolver.globalSymbols[identifier.Name]
	if symbol == nil {
		return unknownScalarOrigin(), false
	}

	return resolver.contextIdentifierOrigin(symbol, context, depth)
}

func (resolver *scalarResolver) contextIdentifierOrigin(
	symbol *scalarSymbol,
	context *scalarContext,
	depth int,
) (scalarOrigin, bool) {
	for current := context; current != nil; current = current.parent {
		if binding, found := current.bindings[symbol]; found && binding.hasValue {
			return binding.value, true
		}

		if source, found := current.rangeSources[symbol]; found {
			return resolver.rangeSourceOrigin(source, current, depth+1), true
		}

		definitions := current.definitions[symbol]
		if len(definitions) > 0 {
			return resolver.evaluateDefinitions(definitions, current, depth+1), true
		}
	}

	return unknownScalarOrigin(), false
}

func (resolver *scalarResolver) rangeSourceOrigin(
	source scalarRangeSource,
	context *scalarContext,
	depth int,
) scalarOrigin {
	if source.key {
		return resolver.mapKeyOrigins(source.expression, context, context.file, depth)
	}

	return resolver.sequenceOrigins(source.expression, context, context.file, depth)
}

func (resolver *scalarResolver) sequenceOrigins(
	expression ast.Expr,
	context *scalarContext,
	file *parsedGoFile,
	depth int,
) scalarOrigin {
	if expression == nil || depth > maxScalarResolutionDepth {
		return unknownScalarOrigin()
	}

	switch value := unparen(expression).(type) {
	case *ast.CompositeLit:
		return resolver.sequenceCompositeOrigins(value, context, file, depth+1)
	case *ast.Ident:
		return resolver.sequenceIdentifierOrigins(value, context, depth+1)
	case *ast.Ellipsis:
		return resolver.sequenceOrigins(value.Elt, context, file, depth+1)
	}

	return resolver.evaluateAt(expression, context, file, depth+1)
}

func (resolver *scalarResolver) sequenceCompositeOrigins(
	literal *ast.CompositeLit,
	context *scalarContext,
	file *parsedGoFile,
	depth int,
) scalarOrigin {
	if len(literal.Elts) == 0 {
		return emptyScalarOrigin()
	}

	var result scalarOrigin

	for _, element := range literal.Elts {
		if pair, ok := element.(*ast.KeyValueExpr); ok {
			element = pair.Value
		}

		result = combineScalarOrigins(result, resolver.evaluateAt(element, context, file, depth+1))
	}

	return result
}

func (resolver *scalarResolver) sequenceIdentifierOrigins(
	identifier *ast.Ident,
	context *scalarContext,
	depth int,
) scalarOrigin {
	if context == nil {
		return resolver.globalSequenceIdentifierOrigins(identifier, depth+1)
	}

	symbol := context.lookupSymbol(identifier)
	if symbol == nil {
		return resolver.globalSequenceIdentifierOrigins(identifier, depth+1)
	}

	for current := context; current != nil; current = current.parent {
		if binding, found := current.bindings[symbol]; found && binding.hasValue {
			return binding.value
		}

		if definitions := current.definitions[symbol]; len(definitions) > 0 {
			return resolver.sequenceDefinitionOrigins(definitions, current, depth+1)
		}
	}

	return unknownScalarOrigin()
}

func (resolver *scalarResolver) globalSequenceIdentifierOrigins(
	identifier *ast.Ident,
	depth int,
) scalarOrigin {
	if definitions := resolver.globals[identifier.Name]; len(definitions) > 0 {
		return resolver.sequenceDefinitionOrigins(definitions, nil, depth+1)
	}

	return unknownScalarOrigin()
}

func (resolver *scalarResolver) sequenceDefinitionOrigins(
	definitions []scalarDefinition,
	context *scalarContext,
	depth int,
) scalarOrigin {
	var result scalarOrigin

	for _, definition := range definitions {
		if resolver.active[definition.expression] {
			return unknownScalarOrigin()
		}

		resolver.active[definition.expression] = true

		origin := resolver.sequenceOrigins(
			definition.expression, context, definition.file, depth+1,
		)

		if definition.resultIndex > 0 {
			origin = resolver.evaluateExpressionResult(
				definition.expression, definition.resultIndex, context, definition.file, depth+1,
			)
		}

		result = combineScalarOrigins(result, origin)

		delete(resolver.active, definition.expression)
	}

	return result
}

func (resolver *scalarResolver) evaluateDefinitions(
	definitions []scalarDefinition,
	context *scalarContext,
	depth int,
) scalarOrigin {
	var result scalarOrigin

	for _, definition := range definitions {
		if resolver.active[definition.expression] {
			result = combineScalarOrigins(result, unknownScalarOrigin())

			continue
		}

		resolver.active[definition.expression] = true
		result = combineScalarOrigins(result, resolver.evaluateExpressionResult(
			definition.expression, definition.resultIndex, context, definition.file, depth,
		))

		delete(resolver.active, definition.expression)
	}

	return result
}

func (resolver *scalarResolver) globalIdentifierOrigin(name string, depth int) scalarOrigin {
	definitions := resolver.globals[name]
	if len(definitions) > 0 {
		var result scalarOrigin

		for _, definition := range definitions {
			if resolver.active[definition.expression] {
				return unknownScalarOrigin()
			}

			resolver.active[definition.expression] = true
			result = combineScalarOrigins(
				result,
				resolver.evaluateExpressionResult(
					definition.expression, definition.resultIndex, nil, definition.file, depth+1,
				),
			)

			delete(resolver.active, definition.expression)
		}

		return result
	}

	return unknownScalarOrigin()
}

func (resolver *scalarResolver) evaluateSelector(
	selector *ast.SelectorExpr,
	context *scalarContext,
	file *parsedGoFile,
	depth int,
) scalarOrigin {
	imports := resolver.imports[file.file]

	packagePath, imported, shadowed := selectorImport(selector, imports)
	if imported && !shadowed && packagePath == resolver.modulePath+"/internal/protocol" {
		if value, exists := resolver.protocolValues[selector.Sel.Name]; exists {
			return generatedScalarOrigin(value)
		}
	}

	if !imported {
		base := resolver.evaluateAt(selector.X, context, file, depth+1)
		if base.caller && !base.unknown && !base.hardcoded {
			field, constrained := resolver.generatedFieldMetadata(
				selector.X, context, file, selector.Sel.Name,
			)
			if constrained && resolver.generatedTypes[field.generatedType] {
				return combineScalarOrigins(base, scalarTypeOrigin(field.generatedType))
			}

			return callerScalarOrigin()
		}

		if base.hardcoded || base.unknown {
			return hardcodedUnknownScalarOrigin(base.hardcoded)
		}
	}

	return unknownScalarOrigin()
}

func (resolver *scalarResolver) evaluateGeneratedEnumLookup(
	index *ast.IndexExpr,
	file *parsedGoFile,
) scalarOrigin {
	selector, ok := unparen(index.X).(*ast.SelectorExpr)
	if !ok {
		return unknownScalarOrigin()
	}

	packagePath, imported, shadowed := selectorImport(selector, resolver.imports[file.file])
	if !imported || shadowed || packagePath != resolver.modulePath+"/pkg/dependencymodels/signaling" {
		return unknownScalarOrigin()
	}

	const generatedMapPrefix = "ValuesTo"
	if !strings.HasPrefix(selector.Sel.Name, generatedMapPrefix) {
		return unknownScalarOrigin()
	}

	typeName := strings.TrimPrefix(selector.Sel.Name, generatedMapPrefix)
	if !resolver.generatedTypes[typeName] {
		return unknownScalarOrigin()
	}

	return scalarTypeOrigin(typeName)
}
