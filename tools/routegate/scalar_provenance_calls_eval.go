package routegate

import (
	"go/ast"
	"go/token"
	"strconv"
)

func (resolver *scalarResolver) evaluateCall(
	call *ast.CallExpr,
	context *scalarContext,
	file *parsedGoFile,
	depth int,
) scalarOrigin {
	if identifier, ok := unparen(call.Fun).(*ast.Ident); ok {
		if origin, handled := resolver.evaluateIdentifierCall(identifier, call.Args, context, file, depth+1); handled {
			return origin
		}
	}

	if selector, ok := unparen(call.Fun).(*ast.SelectorExpr); ok {
		if origin, handled := resolver.evaluateImportedSelectorCall(selector, call.Args, context, file, depth+1); handled {
			return origin
		}
	}

	if literal, ok := unparen(call.Fun).(*ast.FuncLit); ok {
		return resolver.evaluateFunctionLiteral(literal, file, context, call.Args, file, context, depth+1)
	}

	return resolver.evaluateUnknownCallArguments(call.Args, context, file, depth+1)
}

func (resolver *scalarResolver) evaluateExpressionResult(
	expression ast.Expr,
	resultIndex int,
	context *scalarContext,
	file *parsedGoFile,
	depth int,
) scalarOrigin {
	if resultIndex == 0 {
		return resolver.evaluateAt(expression, context, file, depth)
	}

	call, ok := unparen(expression).(*ast.CallExpr)
	if !ok {
		return unknownScalarOrigin()
	}

	return resolver.evaluateCallResult(call, resultIndex, context, file, depth+1)
}

func (resolver *scalarResolver) evaluateCallResult(
	call *ast.CallExpr,
	resultIndex int,
	context *scalarContext,
	file *parsedGoFile,
	depth int,
) scalarOrigin {
	if resultIndex == 0 {
		return resolver.evaluateCall(call, context, file, depth)
	}

	function, source, functionFile, found := resolver.functionValue(call.Fun, context, file)
	if !found {
		return unknownScalarOrigin()
	}

	switch value := function.(type) {
	case *ast.FuncDecl:
		return resolver.evaluateFunctionResult(
			value, functionFile, nil, call.Args, file, context, resultIndex, depth+1,
		)
	case *ast.FuncLit:
		return resolver.evaluateFunctionLiteralResult(
			value, functionFile, source, call.Args, file, context, resultIndex, depth+1,
		)
	default:
		return unknownScalarOrigin()
	}
}

func (resolver *scalarResolver) evaluateIdentifierCall(
	identifier *ast.Ident,
	arguments []ast.Expr,
	context *scalarContext,
	file *parsedGoFile,
	depth int,
) (scalarOrigin, bool) {
	var symbol *scalarSymbol
	if context != nil {
		symbol = context.lookupSymbol(identifier)
	}

	if symbol == nil && resolver.globalSymbols[identifier.Name] == nil &&
		identifier.Name == "string" && len(arguments) == 1 {
		return resolver.evaluateAt(arguments[0], context, file, depth), true
	}

	if symbol != nil {
		if binding, found := resolver.lookupBinding(symbol, context); found {
			if binding.function != nil {
				return resolver.invokeFunctionValue(binding, arguments, context, depth), true
			}

			return unknownScalarOrigin(), true
		}

		return unknownScalarOrigin(), true
	}

	functions := resolver.functions[identifier.Name]
	if len(functions) == 0 {
		return unknownScalarOrigin(), false
	}

	return resolver.invokeFunctions(functions, arguments, file, context, depth), true
}

func (resolver *scalarResolver) evaluateImportedSelectorCall(
	selector *ast.SelectorExpr,
	arguments []ast.Expr,
	context *scalarContext,
	file *parsedGoFile,
	depth int,
) (scalarOrigin, bool) {
	packagePath, imported, shadowed := selectorImport(selector, resolver.imports[file.file])
	if !imported || shadowed {
		return unknownScalarOrigin(), false
	}

	functions := resolver.packageFunctions(packagePath, selector.Sel.Name)
	if len(functions) == 0 {
		return unknownScalarOrigin(), true
	}

	return resolver.invokeFunctions(functions, arguments, file, context, depth), true
}

func (resolver *scalarResolver) evaluateUnknownCallArguments(
	arguments []ast.Expr,
	context *scalarContext,
	file *parsedGoFile,
	depth int,
) scalarOrigin {
	var result scalarOrigin
	for _, argument := range arguments {
		result = combineScalarOrigins(result, resolver.evaluateAt(argument, context, file, depth))
	}

	result.unknown = true
	result.emptyOnly = false

	return result
}

func (resolver *scalarResolver) packageFunctions(packagePath, name string) []scalarFunction {
	if packagePath != resolver.packagePath {
		return nil
	}

	return resolver.functions[name]
}

func (resolver *scalarResolver) invokeFunctions(
	functions []scalarFunction,
	arguments []ast.Expr,
	callerFile *parsedGoFile,
	caller *scalarContext,
	depth int,
) scalarOrigin {
	var result scalarOrigin
	for _, candidate := range functions {
		result = combineScalarOrigins(
			result,
			resolver.evaluateFunction(candidate.function, candidate.file, nil, arguments, callerFile, caller, depth+1),
		)
	}

	return result
}

func (resolver *scalarResolver) evaluateFunction(
	function *ast.FuncDecl,
	file *parsedGoFile,
	parent *scalarContext,
	arguments []ast.Expr,
	callerFile *parsedGoFile,
	caller *scalarContext,
	depth int,
) scalarOrigin {
	return resolver.evaluateFunctionResult(function, file, parent, arguments, callerFile, caller, 0, depth)
}

func (resolver *scalarResolver) evaluateFunctionResult(
	function *ast.FuncDecl,
	file *parsedGoFile,
	parent *scalarContext,
	arguments []ast.Expr,
	callerFile *parsedGoFile,
	caller *scalarContext,
	resultIndex int,
	depth int,
) scalarOrigin {
	if function.Body == nil || resolver.active[function] || depth > maxScalarResolutionDepth {
		return unknownScalarOrigin()
	}

	resolver.active[function] = true
	defer delete(resolver.active, function)

	context := resolver.newScalarContext(file, parent, function.Type, function.Body)
	resolver.bindArguments(context, function.Type.Params, arguments, callerFile, caller, depth+1)

	return resolver.evaluateReturns(function.Type, function.Body, context, file, resultIndex, depth+1)
}

func (resolver *scalarResolver) evaluateFunctionLiteral(
	literal *ast.FuncLit,
	functionFile *parsedGoFile,
	parent *scalarContext,
	arguments []ast.Expr,
	callerFile *parsedGoFile,
	caller *scalarContext,
	depth int,
) scalarOrigin {
	return resolver.evaluateFunctionLiteralResult(
		literal, functionFile, parent, arguments, callerFile, caller, 0, depth,
	)
}

func (resolver *scalarResolver) evaluateFunctionLiteralResult(
	literal *ast.FuncLit,
	functionFile *parsedGoFile,
	parent *scalarContext,
	arguments []ast.Expr,
	callerFile *parsedGoFile,
	caller *scalarContext,
	resultIndex int,
	depth int,
) scalarOrigin {
	if resolver.active[literal] || depth > maxScalarResolutionDepth {
		return unknownScalarOrigin()
	}

	resolver.active[literal] = true
	defer delete(resolver.active, literal)

	context := resolver.newScalarContext(functionFile, parent, literal.Type, literal.Body)
	resolver.bindArguments(context, literal.Type.Params, arguments, callerFile, caller, depth+1)

	return resolver.evaluateReturns(literal.Type, literal.Body, context, functionFile, resultIndex, depth+1)
}

func (resolver *scalarResolver) invokeFunctionValue(
	binding scalarBinding,
	arguments []ast.Expr,
	caller *scalarContext,
	depth int,
) scalarOrigin {
	callerFile := binding.functionFile
	if caller != nil {
		callerFile = caller.file
	}

	if function, ok := binding.function.(*ast.FuncDecl); ok {
		return resolver.evaluateFunction(function, binding.functionFile, nil, arguments, callerFile, caller, depth+1)
	}

	if literal, ok := binding.function.(*ast.FuncLit); ok {
		return resolver.evaluateFunctionLiteral(
			literal, binding.functionFile, binding.source, arguments, callerFile, caller, depth+1,
		)
	}

	return unknownScalarOrigin()
}

func (resolver *scalarResolver) bindArguments(
	context *scalarContext,
	parameters *ast.FieldList,
	arguments []ast.Expr,
	callerFile *parsedGoFile,
	caller *scalarContext,
	depth int,
) {
	if parameters == nil {
		return
	}

	argumentIndex := 0

	for _, parameter := range parameters.List {
		for _, name := range parameter.Names {
			symbol := context.declarations[name]
			if symbol == nil {
				argumentIndex++

				continue
			}

			if _, variadic := parameter.Type.(*ast.Ellipsis); variadic {
				var origin scalarOrigin
				for _, argument := range arguments[argumentIndex:] {
					origin = combineScalarOrigins(
						origin, resolver.sequenceOrigins(argument, caller, callerFile, depth+1),
					)
				}

				context.bindings[symbol] = valueScalarBinding(origin)
				argumentIndex = len(arguments)

				continue
			}

			if argumentIndex >= len(arguments) {
				argumentIndex++

				continue
			}

			argument := arguments[argumentIndex]
			argumentIndex++

			if isFunctionType(parameter.Type) {
				if function, source, functionFile, found := resolver.functionValue(argument, caller, callerFile); found {
					context.bindings[symbol] = functionScalarBinding(function, source, functionFile)
				}

				continue
			}

			origin := resolver.evaluateAt(argument, caller, callerFile, depth+1)
			if _, sequence := unparen(parameter.Type).(*ast.ArrayType); sequence {
				origin = resolver.sequenceOrigins(argument, caller, callerFile, depth+1)
			}

			context.bindings[symbol] = valueScalarBinding(origin)
		}
	}
}

func (resolver *scalarResolver) functionValue(
	expression ast.Expr,
	context *scalarContext,
	file *parsedGoFile,
) (ast.Node, *scalarContext, *parsedGoFile, bool) {
	switch value := unparen(expression).(type) {
	case *ast.FuncLit:
		return value, context, file, true
	case *ast.IndexExpr:
		return resolver.functionValue(value.X, context, file)
	case *ast.IndexListExpr:
		return resolver.functionValue(value.X, context, file)
	case *ast.Ident:
		if context != nil {
			if symbol := context.lookupSymbol(value); symbol != nil {
				binding, found := resolver.lookupBinding(symbol, context)
				if !found || binding.function == nil {
					return nil, nil, nil, false
				}

				return binding.function, binding.source, binding.functionFile, true
			}
		}

		for _, candidate := range resolver.functions[value.Name] {
			return candidate.function, nil, candidate.file, true
		}
	case *ast.SelectorExpr:
		packagePath, imported, shadowed := selectorImport(value, resolver.imports[file.file])
		if imported && !shadowed {
			for _, candidate := range resolver.packageFunctions(packagePath, value.Sel.Name) {
				return candidate.function, nil, candidate.file, true
			}
		}
	}

	return nil, nil, nil, false
}

func (resolver *scalarResolver) lookupBinding(symbol *scalarSymbol, context *scalarContext) (scalarBinding, bool) {
	for current := context; current != nil; current = current.parent {
		if binding, found := current.bindings[symbol]; found {
			return binding, true
		}

		if definitions := current.definitions[symbol]; len(definitions) > 0 {
			for _, definition := range definitions {
				if function, source, file, found := resolver.functionValue(
					definition.expression, current, current.file,
				); found {
					return functionScalarBinding(function, source, file), true
				}
			}
		}
	}

	return emptyScalarBinding(), false
}

func (resolver *scalarResolver) evaluateReturns(
	typeExpression *ast.FuncType,
	body *ast.BlockStmt,
	context *scalarContext,
	file *parsedGoFile,
	resultIndex int,
	depth int,
) scalarOrigin {
	var result scalarOrigin

	found := false

	ast.Inspect(body, func(node ast.Node) bool {
		if node != body {
			if _, nested := node.(*ast.FuncLit); nested {
				return false
			}
		}

		statement, ok := node.(*ast.ReturnStmt)
		if !ok {
			return true
		}

		if resolver.isGeneratedZeroErrorReturn(typeExpression, statement, resultIndex, file) {
			return true
		}

		if len(statement.Results) > resultIndex {
			result = combineScalarOrigins(
				result, resolver.evaluateAt(statement.Results[resultIndex], context, file, depth+1),
			)
			found = true

			return true
		}

		if len(statement.Results) == 1 && resultIndex > 0 {
			result = combineScalarOrigins(
				result, resolver.evaluateExpressionResult(
					statement.Results[0], resultIndex, context, file, depth+1,
				),
			)
			found = true

			return true
		}

		if typeExpression != nil && typeExpression.Results != nil && len(typeExpression.Results.List) > resultIndex {
			for _, name := range typeExpression.Results.List[resultIndex].Names {
				result = combineScalarOrigins(
					result, resolver.evaluateSymbol(context.namedResults[name.Name], context, depth+1),
				)
				found = true
			}
		}

		return true
	})

	if !found && typeExpression != nil && typeExpression.Results != nil && len(typeExpression.Results.List) > resultIndex {
		for _, name := range typeExpression.Results.List[resultIndex].Names {
			result = combineScalarOrigins(
				result, resolver.evaluateSymbol(context.namedResults[name.Name], context, depth+1),
			)
			found = true
		}
	}

	if !found {
		return unknownScalarOrigin()
	}

	return result
}

func (resolver *scalarResolver) isGeneratedZeroErrorReturn(
	typeExpression *ast.FuncType,
	statement *ast.ReturnStmt,
	resultIndex int,
	file *parsedGoFile,
) bool {
	if typeExpression == nil || typeExpression.Results == nil || len(statement.Results) <= resultIndex {
		return false
	}

	errorIndex := len(typeExpression.Results.List) - 1
	if errorIndex <= resultIndex || errorIndex >= len(statement.Results) {
		return false
	}

	if _, isErrorConstructor := unparen(statement.Results[errorIndex]).(*ast.CallExpr); !isErrorConstructor {
		return false
	}

	resultType := typeExpression.Results.List[resultIndex].Type

	generatedType := modelTypeName(resultType, file)
	if !resolver.generatedTypes[generatedType] {
		return false
	}

	literal, ok := unparen(statement.Results[resultIndex]).(*ast.BasicLit)
	if !ok || literal.Kind != token.INT {
		return false
	}

	zero, err := strconv.ParseInt(literal.Value, 0, 64)

	return err == nil && zero == 0
}

func (resolver *scalarResolver) evaluateSymbol(
	symbol *scalarSymbol,
	context *scalarContext,
	depth int,
) scalarOrigin {
	if symbol == nil {
		return unknownScalarOrigin()
	}

	if origin, found := resolver.contextIdentifierOrigin(symbol, context, depth); found {
		return origin
	}

	return unknownScalarOrigin()
}
