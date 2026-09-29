package routegate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

const generatedFCMImportPath = "github.com/portpowered/go-ring/internal/generatedfcm"

func verifyFCMGeneratedBuilders(root string, contract fcmOpenAPI) bool {
	path := filepath.Join(root, "internal", "generatedfcm", "client.gen.go")

	raw, err := fs.ReadFile(os.DirFS(root), filepath.ToSlash(filepath.Join("internal", "generatedfcm", "client.gen.go")))
	if err != nil {
		return false
	}

	file, err := parser.ParseFile(token.NewFileSet(), path, raw, parser.ParseComments)
	if err != nil || file.Name.Name != "generatedfcm" {
		return false
	}

	if len(contract.Paths) != len(expectedFCMRoutes()) {
		return false
	}

	for routePath, expected := range expectedFCMRoutes() {
		if _, exists := contract.Paths[routePath]; !exists {
			return false
		}

		pathBuilder := findFunction(file, expected.pathBuilder)
		if pathBuilder == nil || !generatedFCMPathBuilderMatches(pathBuilder, file, expected) {
			return false
		}

		if expected.builder != expected.pathBuilder {
			wrapper := findFunction(file, expected.builder)
			if wrapper == nil || !generatedFCMWrapperMatches(wrapper, file, expected) {
				return false
			}
		}
	}

	return true
}

func generatedFCMPathBuilderMatches(function *ast.FuncDecl, file *ast.File, route fcmRouteContract) bool {
	imports := importAliases(file)
	parameters := functionParameterObjects(function)

	formatCalls := selectorCalls(function, "Sprintf")
	if len(formatCalls) != 1 || len(formatCalls[0].Args) != 1 ||
		!stringLiteralEquals(formatCalls[0].Args[0], route.path) ||
		!selectorCallUsesImport(formatCalls[0], imports, "fmt") {
		return false
	}

	operationPath := assignedIdentifierForCall(function, formatCalls[0])
	if operationPath == nil || identifierWriteCount(function.Body, operationPath) != 2 ||
		!generatedPathNormalizationMatches(function, operationPath) {
		return false
	}

	urlParseCalls := importedSelectorCalls(function, "Parse", imports, "net/url")
	if len(urlParseCalls) != 1 || len(urlParseCalls[0].Args) != 1 ||
		!identifierObjectEquals(urlParseCalls[0].Args[0], parameters["server"]) {
		return false
	}

	serverURL := assignedIdentifierForCall(function, urlParseCalls[0])
	if serverURL == nil || identifierWriteCount(function.Body, serverURL) != 1 {
		return false
	}

	serverMethodParse := methodCalls(function, "Parse", serverURL)
	if len(serverMethodParse) != 1 || len(serverMethodParse[0].Args) != 1 ||
		!identifierObjectEquals(serverMethodParse[0].Args[0], operationPath) {
		return false
	}

	queryURL := assignedIdentifierForCall(function, serverMethodParse[0])
	if queryURL == nil || identifierWriteCount(function.Body, queryURL) != 1 {
		return false
	}

	newRequests := importedSelectorCalls(function, "NewRequest", imports, httpImportPath)
	if len(newRequests) != 1 || len(newRequests[0].Args) != 3 ||
		!isHTTPConstant(newRequests[0].Args[0], imports, "MethodPost") ||
		!selectorMethodCallUsesObject(newRequests[0].Args[1], "String", queryURL) ||
		!identifierObjectEquals(newRequests[0].Args[2], parameters["body"]) {
		return false
	}

	return true
}

func generatedPathNormalizationMatches(function *ast.FuncDecl, operationPath *ast.Object) bool {
	count := 0
	valid := true

	ast.Inspect(function.Body, func(node ast.Node) bool {
		conditional, ok := node.(*ast.IfStmt)
		if !ok || !pathSlashCondition(conditional.Cond, operationPath) {
			return true
		}

		count++

		if len(conditional.Body.List) != 1 {
			valid = false

			return true
		}

		assignment, ok := conditional.Body.List[0].(*ast.AssignStmt)
		if !ok || assignment.Tok != token.ASSIGN || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
			valid = false

			return true
		}

		left, ok := assignment.Lhs[0].(*ast.Ident)
		if !ok || left.Obj != operationPath {
			valid = false

			return true
		}

		joined, ok := assignment.Rhs[0].(*ast.BinaryExpr)
		if !ok || joined.Op != token.ADD || !stringLiteralEquals(joined.X, ".") ||
			!identifierObjectEquals(joined.Y, operationPath) {
			valid = false
		}

		return true
	})

	return valid && count == 1
}

func pathSlashCondition(expression ast.Expr, operationPath *ast.Object) bool {
	comparison, ok := unparen(expression).(*ast.BinaryExpr)
	if !ok || comparison.Op != token.EQL {
		return false
	}

	index, ok := unparen(comparison.X).(*ast.IndexExpr)
	if !ok || !identifierObjectEquals(index.X, operationPath) || !integerLiteralEquals(index.Index, 0) {
		return false
	}

	character, ok := unparen(comparison.Y).(*ast.BasicLit)
	if !ok || character.Kind != token.CHAR {
		return false
	}

	value, err := strconv.Unquote(character.Value)

	return err == nil && value == "/"
}

func generatedFCMWrapperMatches(function *ast.FuncDecl, file *ast.File, route fcmRouteContract) bool {
	parameters := functionParameterObjects(function)

	calls := selectorOrIdentifierCalls(function, route.pathBuilder)
	if len(calls) != 1 || len(calls[0].Args) != 4 || !callIsReturned(function, calls[0]) {
		return false
	}

	identifier, ok := calls[0].Fun.(*ast.Ident)
	if !ok || identifier.Obj == nil || len(calls[0].Args) != 4 ||
		!identifierObjectEquals(calls[0].Args[0], parameters["server"]) ||
		!identifierObjectEquals(calls[0].Args[1], parameters["params"]) ||
		!stringLiteralEquals(calls[0].Args[2], route.contentType) {
		return false
	}

	if route.contentType == "application/x-www-form-urlencoded" {
		return functionHasImportedSelector(function, importAliases(file), "MarshalForm", "github.com/oapi-codegen/runtime") &&
			functionHasImportedSelector(function, importAliases(file), "NewReader", "strings")
	}

	return functionHasImportedSelector(function, importAliases(file), "Marshal", "encoding/json") &&
		functionHasImportedSelector(function, importAliases(file), "NewReader", "bytes")
}

func callIsReturned(function *ast.FuncDecl, call *ast.CallExpr) bool {
	found := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		returned, ok := node.(*ast.ReturnStmt)
		if !ok {
			return true
		}

		for _, expression := range returned.Results {
			if expression == call {
				found = true

				return false
			}
		}

		return true
	})

	return found
}

func selectorOrIdentifierCalls(function *ast.FuncDecl, name string) []*ast.CallExpr {
	var calls []*ast.CallExpr

	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		switch function := unparen(call.Fun).(type) {
		case *ast.Ident:
			if function.Name == name {
				calls = append(calls, call)
			}
		case *ast.SelectorExpr:
			if function.Sel.Name == name {
				calls = append(calls, call)
			}
		}

		return true
	})

	return calls
}

func assignedIdentifierForCall(function *ast.FuncDecl, call *ast.CallExpr) *ast.Object {
	var assigned *ast.Object

	ast.Inspect(function.Body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok || !assignmentContainsCall(assignment, call) {
			return true
		}

		for _, expression := range assignment.Lhs {
			identifier, ok := expression.(*ast.Ident)
			if ok && identifier.Name != "_" {
				assigned = identifier.Obj

				return false
			}
		}

		return true
	})

	return assigned
}

func assignmentContainsCall(assignment *ast.AssignStmt, call *ast.CallExpr) bool {
	for _, expression := range assignment.Rhs {
		if expression == call {
			return true
		}
	}

	return false
}

func importedSelectorCalls(
	function *ast.FuncDecl,
	name string,
	imports map[string]string,
	path string,
) []*ast.CallExpr {
	var calls []*ast.CallExpr

	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == name && isImportedSelector(selector, imports, path) {
			calls = append(calls, call)
		}

		return true
	})

	return calls
}

func methodCalls(function *ast.FuncDecl, name string, receiver *ast.Object) []*ast.CallExpr {
	var calls []*ast.CallExpr

	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == name && identifierObjectEquals(selector.X, receiver) {
			calls = append(calls, call)
		}

		return true
	})

	return calls
}

func selectorCallUsesImport(call *ast.CallExpr, imports map[string]string, importPath string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)

	return ok && isImportedSelector(selector, imports, importPath)
}

func selectorMethodCallUsesObject(expression ast.Expr, method string, object *ast.Object) bool {
	call, ok := unparen(expression).(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}

	selector, ok := call.Fun.(*ast.SelectorExpr)

	return ok && selector.Sel.Name == method && identifierObjectEquals(selector.X, object)
}

func integerLiteralEquals(expression ast.Expr, expected int64) bool {
	literal, ok := unparen(expression).(*ast.BasicLit)
	if !ok || literal.Kind != token.INT {
		return false
	}

	value, err := strconv.ParseInt(literal.Value, 0, 64)

	return err == nil && value == expected
}
