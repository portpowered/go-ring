package routegate

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

const (
	fcmPostArgumentCount = 2
	fcmWithContextMethod = "WithContext"
)

type fcmRouteCallsite struct {
	file     string
	function string
	route    string
}

func expectedFCMCallsites() []fcmRouteCallsite {
	return []fcmRouteCallsite{
		{file: "third_party/go-push-receiver/instanceid.go", function: "checkIn", route: "/checkin"},
		{file: "third_party/go-push-receiver/instanceid.go", function: "doRegister", route: "/c2dm/register3"},
		{file: "third_party/go-push-receiver/fcm.go", function: "installFCM", route: "/v1/projects/ring-17770/installations"},
		{
			file:     "third_party/go-push-receiver/fcm.go",
			function: "registerFCM",
			route:    "/v1/projects/ring-17770/registrations",
		},
	}
}

func verifyFCMCallSites(sources map[string]*parsedGoFile, contract fcmOpenAPI) bool {
	if len(contract.Paths) != len(expectedFCMRoutes()) {
		return false
	}

	thirdPartyFiles := make([]*parsedGoFile, 0)

	for path, file := range sources {
		if len(path) >= len("third_party/go-push-receiver/") &&
			path[:len("third_party/go-push-receiver/")] == "third_party/go-push-receiver/" {
			thirdPartyFiles = append(thirdPartyFiles, file)
		}
	}

	if countSelectorUses(thirdPartyFiles, "post") != len(expectedFCMCallsites()) {
		return false
	}

	for _, expected := range expectedFCMRoutes() {
		if countSelectorUses(thirdPartyFiles, expected.builder) != 1 {
			return false
		}
	}

	for _, expected := range expectedFCMCallsites() {
		file := sources[expected.file]
		if file == nil {
			return false
		}

		function := findMethod([]*parsedGoFile{file}, "Client", expected.function)
		if function == nil {
			return false
		}

		postCalls := selectorCalls(function, "post")

		route, exists := expectedFCMRoutes()[expected.route]
		if !exists {
			return false
		}

		builderCalls := selectorCalls(function, route.builder)
		if len(postCalls) != 1 || len(builderCalls) != 1 ||
			!postCallUsesCurrentReceiver(postCalls[0], function) ||
			!postCallConsumesGeneratedBuilder(postCalls[0], builderCalls[0], function, route, file.file) {
			return false
		}
	}

	return true
}

func countSelectorUses(files []*parsedGoFile, name string) int {
	count := 0

	for _, file := range files {
		ast.Inspect(file.file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == name {
				count++
			}

			return true
		})
	}

	return count
}

func selectorCalls(node ast.Node, name string) []*ast.CallExpr {
	calls := make([]*ast.CallExpr, 0, 1)

	ast.Inspect(node, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == name {
			calls = append(calls, call)
		}

		return true
	})

	return calls
}

func stringLiteralEquals(expression ast.Expr, expected string) bool {
	literal, ok := unparen(expression).(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return false
	}

	value, err := strconv.Unquote(literal.Value)

	return err == nil && value == expected
}

func postCallUsesCurrentReceiver(call *ast.CallExpr, function *ast.FuncDecl) bool {
	if len(call.Args) != fcmPostArgumentCount {
		return false
	}

	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "post" {
		return false
	}

	identifier, ok := selector.X.(*ast.Ident)
	if !ok || identifier.Obj == nil {
		return false
	}

	receiver := methodReceiver(function)

	return receiver != nil && identifier.Obj == receiver.Obj
}

func postCallConsumesGeneratedBuilder(
	call *ast.CallExpr,
	builder *ast.CallExpr,
	function *ast.FuncDecl,
	route fcmRouteContract,
	file *ast.File,
) bool {
	if !generatedBuilderCallMatches(builder, route, file) {
		return false
	}

	request, ok := call.Args[1].(*ast.Ident)
	if !ok || request.Obj == nil || !requestCreatedBy(function, builder, request.Obj) ||
		identifierWriteCount(function.Body, request.Obj) != 1 {
		return false
	}

	parameters := functionParameterObjects(function)

	return identifierObjectEquals(call.Args[0], parameters["ctx"]) &&
		identifierObjectEquals(call.Args[1], request.Obj)
}

func generatedBuilderCallMatches(
	call *ast.CallExpr,
	route fcmRouteContract,
	file *ast.File,
) bool {
	if len(call.Args) < 3 || len(call.Args) > 4 {
		return false
	}

	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != route.builder ||
		!isImportedSelector(selector, importAliases(file), generatedFCMImportPath) {
		return false
	}

	server, ok := unparen(call.Args[0]).(*ast.BinaryExpr)
	if !ok || server.Op != token.ADD || !stringLiteralEquals(server.X, "https://") {
		return false
	}

	serverHost, ok := unparen(server.Y).(*ast.SelectorExpr)
	if !ok || serverHost.Sel.Name != route.constant+"Host" ||
		!isImportedSelector(serverHost, importAliases(file), "github.com/portpowered/go-ring/internal/protocol") {
		return false
	}

	if len(call.Args) == 4 && !generatedContentTypeMatches(call.Args[2], route, file) {
		return false
	}

	return true
}

func generatedContentTypeMatches(expression ast.Expr, route fcmRouteContract, file *ast.File) bool {
	call, ok := unparen(expression).(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}

	function, ok := unparen(call.Fun).(*ast.Ident)
	if !ok || function.Name != "string" {
		return false
	}

	selector, ok := unparen(call.Args[0]).(*ast.SelectorExpr)
	if !ok || !strings.Contains(selector.Sel.Name, "ContentType") ||
		!isImportedSelector(selector, importAliases(file), "github.com/portpowered/go-ring/internal/generatedfcm") {
		return false
	}

	return strings.Contains(strings.ToLower(selector.Sel.Name), contentTypeIdentifier(route.contentType))
}

func contentTypeIdentifier(value string) string {
	identifier := strings.ReplaceAll(value, "-", "")
	identifier = strings.ReplaceAll(identifier, "/", "")
	identifier = strings.ReplaceAll(identifier, ".", "")

	return identifier
}

func verifyFCMRequestHelper(sources map[string]*parsedGoFile) ([]*ast.CallExpr, bool) {
	file := sources["third_party/go-push-receiver/client.go"]
	if file == nil {
		return nil, false
	}

	function := findMethod([]*parsedGoFile{file}, "Client", "post")
	if function == nil || len(selectorCalls(function, "NewRequestWithContext")) != 0 ||
		len(selectorCalls(function, "NewRequest")) != 0 || len(selectorCalls(function, "Do")) != 1 ||
		len(selectorCalls(function, fcmWithContextMethod)) != 1 {
		return nil, false
	}

	doCall := selectorCalls(function, "Do")[0]
	withContext := selectorCalls(function, "WithContext")[0]
	parameters := functionParameterObjects(function)

	withContextSelector, ok := withContext.Fun.(*ast.SelectorExpr)

	if !ok || withContextSelector.Sel.Name != fcmWithContextMethod || len(withContext.Args) != 1 ||
		!identifierObjectEquals(withContextSelector.X, parameters["request"]) ||
		!identifierObjectEquals(withContext.Args[0], parameters["ctx"]) ||
		!requestCreatedBy(function, withContext, parameters["request"]) ||
		identifierWriteCount(function.Body, parameters["request"]) != 1 {
		return nil, false
	}

	doSelector, ok := doCall.Fun.(*ast.SelectorExpr)
	if !ok || doSelector.Sel.Name != "Do" || !methodFieldReceiverIs(doSelector.X, function, "httpClient") ||
		len(doCall.Args) != 1 {
		return nil, false
	}

	if !identifierObjectEquals(doCall.Args[0], parameters["request"]) || withContext.Pos() >= doCall.Pos() {
		return nil, false
	}

	return []*ast.CallExpr{withContext, doCall}, true
}

func isImportedSelector(selector *ast.SelectorExpr, imports map[string]string, path string) bool {
	importPath, imported, shadowed := selectorImport(selector, imports)

	return imported && !shadowed && importPath == path
}

func isHTTPConstant(expression ast.Expr, imports map[string]string, name string) bool {
	selector, ok := unparen(expression).(*ast.SelectorExpr)

	return ok && selector.Sel.Name == name && isImportedSelector(selector, imports, httpImportPath)
}

func functionParameterObjects(function *ast.FuncDecl) map[string]*ast.Object {
	objects := make(map[string]*ast.Object)
	if function.Type.Params == nil {
		return objects
	}

	for _, field := range function.Type.Params.List {
		for _, name := range field.Names {
			objects[name.Name] = name.Obj
		}
	}

	return objects
}

func identifierObjectEquals(expression ast.Expr, object *ast.Object) bool {
	identifier, ok := unparen(expression).(*ast.Ident)

	return ok && object != nil && identifier.Obj == object
}

func methodFieldReceiverIs(expression ast.Expr, function *ast.FuncDecl, field string) bool {
	selector, ok := unparen(expression).(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != field {
		return false
	}

	identifier, ok := unparen(selector.X).(*ast.Ident)
	receiver := methodReceiver(function)

	return ok && receiver != nil && identifier.Obj == receiver.Obj
}

func methodReceiver(function *ast.FuncDecl) *ast.Ident {
	if function.Recv == nil || len(function.Recv.List) != 1 || len(function.Recv.List[0].Names) != 1 {
		return nil
	}

	return function.Recv.List[0].Names[0]
}

func requestCreatedBy(function *ast.FuncDecl, call *ast.CallExpr, request *ast.Object) bool {
	if request == nil {
		return false
	}

	found := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}

		containsCall := false

		for _, expression := range assignment.Rhs {
			if expression == call {
				containsCall = true
			}
		}

		if !containsCall {
			return true
		}

		for _, expression := range assignment.Lhs {
			identifier, ok := expression.(*ast.Ident)
			if ok && identifier.Obj == request {
				found = true
			}
		}

		return true
	})

	return found
}

func identifierWriteCount(body *ast.BlockStmt, object *ast.Object) int {
	count := 0

	ast.Inspect(body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}

		for _, left := range assignment.Lhs {
			identifier, ok := left.(*ast.Ident)
			if ok && identifier.Obj == object {
				count++
			}
		}

		return true
	})

	return count
}
