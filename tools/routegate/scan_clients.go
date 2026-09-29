package routegate

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func generatedClientObjects(
	file *ast.File,
	imports map[string]string,
	contracts Contracts,
) (map[*ast.Object]string, map[*ast.Object]string) {
	initializers := generatedClientAssignmentOrigins(file, imports)
	writes := generatedClientAssignmentCounts(file)
	objects, candidates := verifiedGeneratedClientMaps(initializers, writes, contracts)
	collectDeclaredGeneratedClients(file, imports, writes, contracts, objects, candidates)

	return objects, candidates
}

func generatedClientAssignmentOrigins(file *ast.File, imports map[string]string) map[*ast.Object]string {
	origins := make(map[*ast.Object]string)

	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}

		for _, statement := range function.Body.List {
			assignment, ok := statement.(*ast.AssignStmt)
			if !ok {
				continue
			}

			for _, expression := range assignment.Rhs {
				packagePath, ok := generatedConstructorPackage(expression, imports)
				if !ok {
					continue
				}

				for _, left := range assignment.Lhs {
					identifier, ok := left.(*ast.Ident)
					if !ok || identifier.Obj == nil || identifier.Name == "_" {
						continue
					}

					origins[identifier.Obj] = packagePath
				}
			}
		}
	}

	return origins
}

func generatedConstructorPackage(expression ast.Expr, imports map[string]string) (string, bool) {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return "", false
	}

	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !isGeneratedHTTPConstructor(selector.Sel.Name) {
		return "", false
	}

	packagePath, imported, shadowed := selectorImport(selector, imports)

	return packagePath, imported && !shadowed
}

func generatedClientAssignmentCounts(file *ast.File) map[*ast.Object]int {
	writes := make(map[*ast.Object]int)

	ast.Inspect(file, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}

		for _, left := range assignment.Lhs {
			identifier, ok := left.(*ast.Ident)
			if ok && identifier.Obj != nil {
				writes[identifier.Obj]++
			}
		}

		return true
	})

	return writes
}

func verifiedGeneratedClientMaps(
	initializers map[*ast.Object]string,
	writes map[*ast.Object]int,
	contracts Contracts,
) (map[*ast.Object]string, map[*ast.Object]string) {
	objects := make(map[*ast.Object]string)
	candidates := make(map[*ast.Object]string, len(initializers))

	for object, packagePath := range initializers {
		candidates[object] = packagePath

		if writes[object] == 1 && hasGeneratedHTTPPath(contracts, packagePath) {
			objects[object] = packagePath
		}
	}

	return objects, candidates
}

func collectDeclaredGeneratedClients(
	file *ast.File,
	imports map[string]string,
	writes map[*ast.Object]int,
	contracts Contracts,
	objects, candidates map[*ast.Object]string,
) {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}

		for _, statement := range function.Body.List {
			general, ok := statement.(*ast.DeclStmt)
			if !ok {
				continue
			}

			group, ok := general.Decl.(*ast.GenDecl)
			if ok && group.Tok == token.VAR {
				collectGeneratedClientValueSpecs(group, imports, writes, contracts, objects, candidates)
			}
		}
	}
}

func collectGeneratedClientValueSpecs(
	group *ast.GenDecl,
	imports map[string]string,
	writes map[*ast.Object]int,
	contracts Contracts,
	objects, candidates map[*ast.Object]string,
) {
	for _, specification := range group.Specs {
		value, ok := specification.(*ast.ValueSpec)
		if !ok {
			continue
		}

		for index, expression := range value.Values {
			packagePath, ok := generatedConstructorPackage(expression, imports)
			if !ok || index >= len(value.Names) {
				continue
			}

			identifier := value.Names[index]
			if identifier.Obj == nil {
				continue
			}

			candidates[identifier.Obj] = packagePath

			if writes[identifier.Obj] == 0 && hasGeneratedHTTPPath(contracts, packagePath) {
				objects[identifier.Obj] = packagePath
			}
		}
	}
}

func generatedOperationNames(contracts Contracts) map[string]map[string]HTTPRoute {
	return contracts.GeneratedHTTP
}

func generatedOperationPaths(name string, operations map[string]map[string]HTTPRoute) []string {
	var paths []string

	for packagePath, packageOperations := range operations {
		if _, exists := packageOperations[name]; exists {
			paths = append(paths, packagePath)
		}
	}

	sort.Strings(paths)

	return paths
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}

	return false
}

func isGeneratedHTTPConstructor(name string) bool {
	return name == generatedClientConstructorName || name == generatedClientResponsesConstructorName
}

func isClientConsumerPath(path string) bool {
	return path == "cmd" || strings.HasPrefix(path, "cmd/") || path == "examples" ||
		strings.HasPrefix(path, "examples/")
}

func protocolStringConstants(root string) map[string]string {
	values := make(map[string]string)
	directory := filepath.Join(root, "internal", "protocol")

	entries, err := os.ReadDir(directory)
	if err != nil {
		return values
	}

	for _, entry := range entries {
		if !isProtocolSourceFile(entry) {
			continue
		}

		path := filepath.Join(directory, entry.Name())
		collectProtocolFileConstants(path, values)
	}

	return values
}

func isProtocolSourceFile(entry os.DirEntry) bool {
	return !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go")
}

func collectProtocolFileConstants(path string, values map[string]string) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return
	}

	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}

		for _, specification := range general.Specs {
			collectProtocolValueConstants(specification, values)
		}
	}
}

func collectProtocolValueConstants(specification ast.Spec, values map[string]string) {
	value, ok := specification.(*ast.ValueSpec)
	if !ok || len(value.Values) != 1 || len(value.Names) == 0 {
		return
	}

	literal, ok := value.Values[0].(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return
	}

	text, err := strconv.Unquote(literal.Value)
	if err != nil {
		return
	}

	for _, name := range value.Names {
		values[name.Name] = text
	}
}

func isMessageComposite(expression ast.Expr) bool {
	for {
		parenthesized, ok := expression.(*ast.ParenExpr)
		if !ok {
			break
		}

		expression = parenthesized.X
	}

	composite, ok := expression.(*ast.CompositeLit)
	if !ok {
		return false
	}

	switch composite.Type.(type) {
	case *ast.SelectorExpr, *ast.Ident:
		return true
	default:
		return false
	}
}

func selectorImport(selector *ast.SelectorExpr, imports map[string]string) (string, bool, bool) {
	identifier := rootIdentifier(selector.X)
	if identifier == nil {
		return "", false, false
	}

	path, imported := imports[identifier.Name]

	return path, imported, imported && identifier.Obj != nil
}

func rootIdentifier(expression ast.Expr) *ast.Ident {
	for {
		switch value := expression.(type) {
		case *ast.Ident:
			return value
		case *ast.SelectorExpr:
			expression = value.X
		case *ast.ParenExpr:
			expression = value.X
		default:
			return nil
		}
	}
}

func importAliases(file *ast.File) map[string]string {
	aliases := make(map[string]string)

	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path == "" {
			continue
		}

		alias := filepath.Base(path)
		if spec.Name != nil {
			alias = spec.Name.Name
		}

		if alias != "_" && alias != "." {
			aliases[alias] = path
		}
	}

	return aliases
}

func parentNodes(root ast.Node) map[ast.Node]ast.Node {
	parents := make(map[ast.Node]ast.Node)
	stack := make([]ast.Node, 0, 64)

	ast.Inspect(root, func(node ast.Node) bool {
		if node == nil {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}

			return false
		}

		if len(stack) > 0 {
			parents[node] = stack[len(stack)-1]
		}

		stack = append(stack, node)

		return true
	})

	return parents
}

func protocolImportPath() string {
	return "github.com/portpowered/go-ring/internal/protocol"
}

func hasImportPath(imports map[string]string, path string) bool {
	for _, candidate := range imports {
		if candidate == path {
			return true
		}
	}

	return false
}

func hasGeneratedHTTPPath(contracts Contracts, path string) bool {
	_, exists := contracts.GeneratedHTTPPaths[path]

	return exists
}

func selectorName(expression ast.Expr) string {
	identifier, ok := expression.(*ast.Ident)
	if ok {
		return identifier.Name
	}

	return "receiver"
}

func isHTTPConstructor(name string) bool {
	return name == "NewRequest" || name == "NewRequestWithContext"
}

func takesRequestValue(arguments []ast.Expr) bool {
	if len(arguments) != 1 {
		return false
	}

	switch value := arguments[0].(type) {
	case *ast.Ident:
		return isRequestVariableName(value.Name)
	case *ast.SelectorExpr:
		return isRequestVariableName(value.Sel.Name)
	case *ast.UnaryExpr:
		composite, ok := value.X.(*ast.CompositeLit)
		if !ok {
			return false
		}

		selector, ok := composite.Type.(*ast.SelectorExpr)

		return ok && selector.Sel.Name == "Request"
	default:
		return false
	}
}

func isRequestVariableName(name string) bool {
	lowerName := strings.ToLower(name)

	return strings.Contains(lowerName, "req") || strings.Contains(lowerName, "request")
}

func isWebSocketDialMethod(name string) bool {
	return name == "Dial" || name == websocketDialContextMethodName
}

func isWebSocketWriteMethod(name string) bool {
	switch name {
	case websocketWriteMessageName, "WriteJSON", "WritePreparedMessage", "WriteControl":
		return true
	default:
		return false
	}
}

func channelAddress(contracts Contracts, address string) string {
	for _, candidate := range contracts.SignalingChannels {
		if candidate.Address == address {
			return candidate.Address
		}
	}

	return ""
}

func uniqueFindings(findings []Finding) []Finding {
	seen := make(map[string]struct{}, len(findings))

	result := findings[:0]

	for _, finding := range findings {
		key := fmt.Sprintf("%s:%d:%s:%s", finding.Path, finding.Line, finding.Rule, finding.Message)
		if _, exists := seen[key]; exists {
			continue
		}

		seen[key] = struct{}{}

		result = append(result, finding)
	}

	return result
}
