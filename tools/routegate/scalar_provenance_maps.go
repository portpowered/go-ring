package routegate

import (
	"go/ast"
	"path/filepath"
	"strings"
)

func (resolver *scalarResolver) isSchemaKeyMapType(expression ast.Expr) bool {
	mapping, ok := unparen(expression).(*ast.MapType)
	if !ok || !isStringIdent(mapping.Key) {
		return false
	}

	return schemaKeyMapValueType(mapping.Value)
}

func schemaKeyMapValueType(expression ast.Expr) bool {
	switch value := unparen(expression).(type) {
	case *ast.StarExpr:
		return schemaKeyMapValueType(value.X)
	case *ast.Ident:
		switch value.Name {
		case "any", "bool", "byte", "float32", "float64", "int", "int8", "int16", "int32", "int64",
			"rune", "string", "uint", "uint8", "uint16", "uint32", "uint64":
			return true
		default:
			return false
		}
	case *ast.SelectorExpr:
		return value.Sel.Name == "RawMessage"
	case *ast.InterfaceType, *ast.ArrayType, *ast.MapType:
		return true
	default:
		return false
	}
}

func (resolver *scalarResolver) isSchemaKeyMapExpression(expression ast.Expr, context *scalarContext) bool {
	return resolver.isSchemaKeyMapExpressionAt(expression, context, make(map[ast.Node]bool))
}

func (resolver *scalarResolver) isSchemaKeyMapExpressionAt(
	expression ast.Expr,
	context *scalarContext,
	seen map[ast.Node]bool,
) bool {
	switch value := unparen(expression).(type) {
	case *ast.CompositeLit:
		return resolver.isSchemaKeyMapType(value.Type)
	case *ast.Ident:
		if context != nil {
			symbol := context.lookupSymbol(value)
			if symbol != nil && resolver.isSchemaKeyMapSymbol(symbol, context, seen) {
				return true
			}
		}
	}

	return false
}

func (resolver *scalarResolver) isSchemaKeyMapSymbol(
	symbol *scalarSymbol,
	context *scalarContext,
	seen map[ast.Node]bool,
) bool {
	for current := context; current != nil; current = current.parent {
		if current.stringMaps[symbol] {
			return true
		}

		for _, definition := range current.definitions[symbol] {
			if seen[definition.expression] {
				continue
			}

			seen[definition.expression] = true
			if resolver.isSchemaKeyMapExpressionAt(definition.expression, current, seen) {
				return true
			}

			delete(seen, definition.expression)
		}
	}

	return false
}

func (resolver *scalarResolver) mapKeyOrigins(
	expression ast.Expr,
	context *scalarContext,
	file *parsedGoFile,
	depth int,
) scalarOrigin {
	if depth > maxScalarResolutionDepth {
		return unknownScalarOrigin()
	}

	switch value := unparen(expression).(type) {
	case *ast.CompositeLit:
		if !resolver.isSchemaKeyMapType(value.Type) {
			return unknownScalarOrigin()
		}

		if len(value.Elts) == 0 {
			return emptyScalarOrigin()
		}

		var result scalarOrigin

		for _, rawElement := range value.Elts {
			pair, ok := rawElement.(*ast.KeyValueExpr)
			if ok {
				result = combineScalarOrigins(result, resolver.evaluateAt(pair.Key, context, file, depth+1))
			}
		}

		if !scalarOriginPresent(result) {
			return unknownScalarOrigin()
		}

		return result
	case *ast.Ident:
		if origin, found := resolver.mapKeyIdentifierOrigins(value, context, depth+1); found {
			return origin
		}
	}

	return resolver.evaluateAt(expression, context, file, depth+1)
}

func (resolver *scalarResolver) mapKeyIdentifierOrigins(
	identifier *ast.Ident,
	context *scalarContext,
	depth int,
) (scalarOrigin, bool) {
	if context == nil {
		return unknownScalarOrigin(), false
	}

	symbol := context.lookupSymbol(identifier)
	if symbol == nil {
		symbol = resolver.globalSymbols[identifier.Name]
	}

	if symbol == nil {
		return unknownScalarOrigin(), false
	}

	for current := context; current != nil; current = current.parent {
		if binding, found := current.bindings[symbol]; found && binding.hasValue {
			return binding.value, true
		}

		if source, found := current.rangeSources[symbol]; found {
			return resolver.rangeSourceOrigin(source, current, depth), true
		}

		definitions := current.definitions[symbol]
		if len(definitions) > 0 {
			return resolver.mapKeyDefinitions(definitions, current, depth), true
		}
	}

	return unknownScalarOrigin(), false
}

func (resolver *scalarResolver) mapKeyDefinitions(
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

		origin := resolver.mapKeyOrigins(definition.expression, context, definition.file, depth)

		if definition.resultIndex > 0 {
			origin = resolver.evaluateExpressionResult(
				definition.expression, definition.resultIndex, context, definition.file, depth,
			)
		}

		result = combineScalarOrigins(result, origin)

		delete(resolver.active, definition.expression)
	}

	return result
}

func scalarOriginPresent(origin scalarOrigin) bool {
	return origin.hardcoded || origin.generated || origin.caller || origin.unknown
}

func resolverModelType(expression ast.Expr, context *scalarContext) string {
	switch value := unparen(expression).(type) {
	case *ast.CompositeLit:
		return modelTypeName(value.Type, context.file)
	case *ast.UnaryExpr:
		return resolverModelType(value.X, context)
	case *ast.Ident:
		if context != nil {
			if symbol := context.lookupSymbol(value); symbol != nil {
				for current := context; current != nil; current = current.parent {
					if name := current.modelTypes[symbol]; name != "" {
						return name
					}
				}
			}
		}
	}

	return ""
}

func modelTypeName(expression ast.Expr, file *parsedGoFile) string {
	switch value := unparen(expression).(type) {
	case *ast.StarExpr:
		return modelTypeName(value.X, file)
	case *ast.SelectorExpr:
		packagePath, imported, shadowed := selectorImport(value, importAliases(file.file))
		if imported && !shadowed && strings.HasSuffix(packagePath, "/pkg/dependencymodels/signaling") {
			return value.Sel.Name
		}
	case *ast.Ident:
		if filepath.ToSlash(filepath.Dir(file.path)) == "pkg/dependencymodels/signaling" {
			return value.Name
		}
	}

	return ""
}

func isFunctionType(expression ast.Expr) bool {
	_, ok := unparen(expression).(*ast.FuncType)

	return ok
}

func combineScalarOrigins(left, right scalarOrigin) scalarOrigin {
	hardcoded := left.hardcoded || right.hardcoded
	emptyOnly := hardcoded && (!left.hardcoded || left.emptyOnly) && (!right.hardcoded || right.emptyOnly)

	return scalarOrigin{
		hardcoded:       hardcoded,
		generated:       left.generated || right.generated,
		generatedValues: combineStringSets(left.generatedValues, right.generatedValues),
		generatedTypes:  combineStringSets(left.generatedTypes, right.generatedTypes),
		caller:          left.caller || right.caller,
		unknown:         left.unknown || right.unknown,
		emptyOnly:       emptyOnly,
	}
}

func generatedScalarOrigin(value string) scalarOrigin {
	return scalarOrigin{
		hardcoded: false, generated: true, generatedValues: map[string]bool{value: true}, generatedTypes: nil,
		caller: false, unknown: false, emptyOnly: false,
	}
}

func scalarTypeOrigin(typeName string) scalarOrigin {
	return scalarOrigin{
		hardcoded: false, generated: true, generatedValues: nil, generatedTypes: map[string]bool{typeName: true},
		caller: false, unknown: false, emptyOnly: false,
	}
}

func combineStringSets(left, right map[string]bool) map[string]bool {
	if len(left) == 0 && len(right) == 0 {
		return nil
	}

	combined := make(map[string]bool, len(left)+len(right))
	for value := range left {
		combined[value] = true
	}

	for value := range right {
		combined[value] = true
	}

	return combined
}

func generatedPrimitiveTypes(fields map[string]map[string]schemaPrimitiveField) map[string]bool {
	types := make(map[string]bool)

	for _, modelFields := range fields {
		for _, field := range modelFields {
			if field.generatedType != "" && !isBuiltinScalarType(field.generatedType) {
				types[field.generatedType] = true
			}
		}
	}

	return types
}

func isBuiltinScalarType(typeName string) bool {
	switch typeName {
	case "any", "bool", "byte", "complex64", "complex128", "error", "float32", "float64",
		"int", "int8", "int16", "int32", "int64", "rune", "string", "uint", "uint8", "uint16",
		"uint32", "uint64", "uintptr":
		return true
	default:
		return false
	}
}

func sourcePackageKey(file *parsedGoFile) string {
	return filepath.Clean(filepath.Dir(file.path))
}
