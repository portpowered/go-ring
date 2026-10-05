package routegate

import "go/ast"

func (resolver *scalarResolver) functionContexts(function *ast.FuncDecl) []*scalarContext {
	resolver.prepareCallContexts()

	if contexts := resolver.callContexts[function]; len(contexts) > 0 {
		return contexts
	}

	if function.Body == nil {
		return nil
	}

	file := functionFile(resolver.functionDecls, function)
	if file == nil {
		return nil
	}

	return []*scalarContext{resolver.newScalarContext(file, nil, function.Type, function.Body)}
}

func functionFile(functions []scalarFunction, target *ast.FuncDecl) *parsedGoFile {
	for _, candidate := range functions {
		if candidate.function == target {
			return candidate.file
		}
	}

	return nil
}

func (resolver *scalarResolver) prepareCallContexts() {
	if resolver.contextsReady {
		return
	}

	resolver.contextsReady = true

	called := make(map[*ast.FuncDecl]bool, len(resolver.functionDecls))

	for _, caller := range resolver.functionDecls {
		if caller.function.Body == nil {
			continue
		}

		ast.Inspect(caller.function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}

			callee, _, _, found := resolver.functionValue(call.Fun, nil, caller.file)
			if target, ok := callee.(*ast.FuncDecl); found && ok {
				called[target] = true
			}

			return true
		})
	}

	for _, candidate := range resolver.functionDecls {
		function := candidate.function
		if function.Body == nil || (!function.Name.IsExported() && called[function]) {
			continue
		}

		context := resolver.newScalarContext(candidate.file, nil, function.Type, function.Body)
		resolver.markExternalCallerParameters(context, function.Type, function.Name.IsExported())
		resolver.addFunctionContext(function, context)
		resolver.collectCallContexts(function.Body, candidate.file, context, make(map[ast.Node]bool), 0)
	}

	for _, candidate := range resolver.functionDecls {
		function := candidate.function
		if function.Body == nil || len(resolver.callContexts[function]) > 0 {
			continue
		}

		context := resolver.newScalarContext(candidate.file, nil, function.Type, function.Body)
		resolver.addFunctionContext(function, context)
		resolver.collectCallContexts(function.Body, candidate.file, context, make(map[ast.Node]bool), 0)
	}
}

func (resolver *scalarResolver) addFunctionContext(function *ast.FuncDecl, context *scalarContext) {
	resolver.callContexts[function] = append(resolver.callContexts[function], context)
}

func (resolver *scalarResolver) collectCallContexts(
	node ast.Node,
	file *parsedGoFile,
	context *scalarContext,
	active map[ast.Node]bool,
	depth int,
) {
	if node == nil || depth > maxScalarResolutionDepth {
		return
	}

	if literal, ok := node.(*ast.FuncLit); ok {
		context := resolver.newScalarContext(file, context, literal.Type, literal.Body)
		resolver.collectCallContexts(literal.Body, file, context, active, depth+1)

		return
	}

	if call, ok := node.(*ast.CallExpr); ok {
		resolver.collectCallContext(call, file, context, active, depth)

		for _, child := range childNodes(call) {
			if child == call.Fun {
				continue
			}

			resolver.collectCallContexts(child, file, context, active, depth+1)
		}

		return
	}

	for _, child := range childNodes(node) {
		resolver.collectCallContexts(child, file, context, active, depth+1)
	}
}

func (resolver *scalarResolver) collectCallContext(
	call *ast.CallExpr,
	file *parsedGoFile,
	caller *scalarContext,
	active map[ast.Node]bool,
	depth int,
) {
	callee, source, calleeFile, found := resolver.functionValue(call.Fun, caller, file)
	if !found {
		return
	}

	switch function := callee.(type) {
	case *ast.FuncDecl:
		if function.Body == nil {
			return
		}

		context := resolver.newScalarContext(calleeFile, nil, function.Type, function.Body)
		resolver.bindArguments(context, function.Type.Params, call.Args, file, caller, depth+1)
		resolver.addFunctionContext(function, context)

		if active[function] {
			return
		}

		active[function] = true
		resolver.collectCallContexts(function.Body, calleeFile, context, active, depth+1)
		delete(active, function)
	case *ast.FuncLit:
		if active[function] {
			return
		}

		context := resolver.newScalarContext(calleeFile, source, function.Type, function.Body)
		resolver.bindArguments(context, function.Type.Params, call.Args, file, caller, depth+1)

		active[function] = true
		resolver.collectCallContexts(function.Body, calleeFile, context, active, depth+1)
		delete(active, function)
	}
}
