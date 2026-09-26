package lint_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"testing"
)

// Classification tokens belong in OpenAPI, which generates the Go enums.
// goconst catches repeated literals; this check catches even one-off tokens.
func TestDeviceClassifierUsesOnlyGeneratedTokens(t *testing.T) {
	path := filepath.Join("..", "..", "pkg", "ring", "client_devices.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "classifyDevice" {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Errorf("invalid string literal: %v", err)
			} else if value != "" {
				t.Errorf("device classifier has hardcoded token %q; add it to api/openapi.yaml", value)
			}
			return true
		})
		return
	}
	t.Fatal("device classifier function not found")
}
