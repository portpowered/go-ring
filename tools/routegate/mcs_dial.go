package routegate

import (
	"go/ast"
	"go/token"
)

func verifyMCSDialCallsite(
	sources map[string]*parsedGoFile,
	socket mcsSocketInventory,
) (*ast.CallExpr, bool) {
	file := sources["third_party/go-push-receiver/fcm.go"]
	if file == nil || socket.Callsite != mcsExpectedCallsite {
		return nil, false
	}

	imports := importAliases(file.file)
	if imports["protocol"] != mcsProtocolImportPath || imports["tls"] != mcsTLSImportPath {
		return nil, false
	}

	target := mcsTryToConnect(file.file)
	if target == nil || target.Body == nil {
		return nil, false
	}

	dials := inspectMCSDialExpressions(target.Body)
	if !mcsDialBranchesMatch(dials) {
		return nil, false
	}

	return dials.tlsCall, true
}

func mcsTryToConnect(file *ast.File) *ast.FuncDecl {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "tryToConnect" {
			return function
		}
	}

	return nil
}

type mcsDialExpressions struct {
	tlsCall      *ast.CallExpr
	injectedCall *ast.CallExpr
	tlsDialer    bool
	branch       *ast.IfStmt
}

func inspectMCSDialExpressions(body *ast.BlockStmt) mcsDialExpressions {
	var dials mcsDialExpressions

	ast.Inspect(body, func(node ast.Node) bool {
		mcsCollectDialCall(node, &dials)
		mcsCollectDialerValue(node, &dials)
		mcsCollectDialBranch(node, &dials)

		return true
	})

	return dials
}

func mcsCollectDialCall(node ast.Node, dials *mcsDialExpressions) {
	call, ok := node.(*ast.CallExpr)
	if !ok {
		return
	}

	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !mcsDialArguments(call.Args) {
		return
	}

	if selector.Sel.Name == websocketDialContextMethodName && isIdent(selector.X, "dialer") {
		dials.tlsCall = call
	}

	if selector.Sel.Name == mcsInjectedDialMethodName && isIdent(selector.X, "c") {
		dials.injectedCall = call
	}
}

func mcsCollectDialerValue(node ast.Node, dials *mcsDialExpressions) {
	assignment, ok := node.(*ast.AssignStmt)
	if !ok || len(assignment.Lhs) != 1 || !isIdent(assignment.Lhs[0], "dialer") ||
		len(assignment.Rhs) != 1 {
		return
	}

	dials.tlsDialer = mcsTLSDialerLiteral(assignment.Rhs[0])
}

func mcsCollectDialBranch(node ast.Node, dials *mcsDialExpressions) {
	conditional, ok := node.(*ast.IfStmt)
	if ok && mcsDialSelectorIsNilCheck(conditional.Cond) {
		dials.branch = conditional
	}
}

func mcsDialBranchesMatch(dials mcsDialExpressions) bool {
	return dials.tlsCall != nil && dials.injectedCall != nil && dials.tlsDialer && dials.branch != nil &&
		containsNode(dials.branch.Body, dials.injectedCall) && containsNode(dials.branch.Else, dials.tlsCall)
}

func mcsDialArguments(arguments []ast.Expr) bool {
	return len(arguments) == 3 && isIdent(arguments[0], "ctx") &&
		mcsProtocolAlias(arguments[1], "MCSNetwork") && mcsProtocolAlias(arguments[2], "MCSAddress")
}

func mcsTLSDialerLiteral(expression ast.Expr) bool {
	address, ok := expression.(*ast.UnaryExpr)
	if !ok || address.Op != token.AND {
		return false
	}

	composite, ok := address.X.(*ast.CompositeLit)
	if !ok {
		return false
	}

	typeSelector, ok := composite.Type.(*ast.SelectorExpr)
	if !ok || !isIdent(typeSelector.X, "tls") || typeSelector.Sel.Name != "Dialer" || len(composite.Elts) != 2 {
		return false
	}

	fields := make(map[string]ast.Expr)

	for _, element := range composite.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			return false
		}

		name, ok := field.Key.(*ast.Ident)
		if !ok {
			return false
		}

		fields[name.Name] = field.Value
	}

	return len(fields) == 2 && mcsClientField(fields["NetDialer"], "dialer") &&
		mcsClientField(fields["Config"], "tlsConfig")
}

func mcsClientField(expression ast.Expr, field string) bool {
	selector, ok := expression.(*ast.SelectorExpr)

	return ok && isIdent(selector.X, "c") && selector.Sel.Name == field
}

func mcsDialSelectorIsNilCheck(expression ast.Expr) bool {
	comparison, ok := expression.(*ast.BinaryExpr)
	if !ok || comparison.Op != token.NEQ || !isIdent(comparison.Y, "nil") {
		return false
	}

	selector, ok := comparison.X.(*ast.SelectorExpr)

	return ok && isIdent(selector.X, "c") && selector.Sel.Name == mcsInjectedDialMethodName
}

func containsNode(parent ast.Node, target ast.Node) bool {
	if parent == nil || target == nil {
		return false
	}

	contains := false

	ast.Inspect(parent, func(node ast.Node) bool {
		if node == target {
			contains = true

			return false
		}

		return !contains
	})

	return contains
}

func verifyMCSDependencyDialInventory(
	parsed []*parsedGoFile,
	verifiedCall *ast.CallExpr,
	callsiteValid bool,
) bool {
	var calls []*ast.CallExpr

	for _, file := range parsed {
		if !parsedPathUnder(file, "third_party/go-push-receiver") {
			continue
		}

		ast.Inspect(file.file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}

			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && isMCSNetworkDialName(selector.Sel.Name) {
				calls = append(calls, call)
			}

			return true
		})
	}

	if !callsiteValid || len(calls) != 2 {
		return false
	}

	allowed := make(map[*ast.CallExpr]bool, 2)
	allowed[verifiedCall] = true

	for _, file := range parsed {
		if !parsedPathIs(file, "third_party/go-push-receiver/fcm.go") {
			continue
		}

		ast.Inspect(file.file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}

			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == mcsInjectedDialMethodName && mcsDialArguments(call.Args) {
				allowed[call] = true
			}

			return true
		})
	}

	if len(allowed) != 2 {
		return false
	}

	for _, call := range calls {
		if !allowed[call] {
			return false
		}
	}

	return true
}

func auditRawNetworkDial(
	call *ast.CallExpr,
	name, packagePath string,
	imported, shadowed bool,
	imports map[string]string,
	verifiedFCMCalls map[*ast.CallExpr]bool,
	add func(ast.Node, string, string),
) {
	if verifiedFCMCalls[call] {
		return
	}

	if !isMCSNetworkDialName(name) {
		return
	}

	if imported && !shadowed && (packagePath == "net" || packagePath == mcsTLSImportPath) ||
		name == websocketDialContextMethodName && hasImportPath(imports, mcsTLSImportPath) {
		add(call, "unbound-network-dial", "opens a raw network connection outside a contract-bound transport helper")
	}
}
