package protocols_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestSignalingTransportDoesNotDefineHandwrittenWireStructsOrLiteralKeys(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	for _, relative := range []string{
		"pkg/dependencies/websocket/wire.go",
		"pkg/dependencies/websocket/wire_close_union.go",
		"pkg/dependencies/websocket/writer.go",
		"pkg/dependencies/websocket/live_negotiation.go",
		"internal/signaling/session.go",
	} {
		path := filepath.Join(root, relative)

		source, err := os.ReadFile(path) // #nosec G304 -- fixed production source paths in this test.
		if err != nil {
			t.Fatal(err)
		}

		file, err := parser.ParseFile(token.NewFileSet(), path, source, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}

		if declarations := jsonTaggedStructs(file); len(declarations) > 0 {
			t.Errorf("%s defines handwritten JSON wire structs %v", relative, declarations)
		}

		if literals := signalingWireKeyLiterals(file); len(literals) > 0 {
			t.Errorf("%s contains signaling wire-key literals %v; use generated protocol.Field constants", relative, literals)
		}
	}
}

func TestSignalingModelGateRejectsHandwrittenWireStructFixture(t *testing.T) {
	t.Parallel()

	fixture := `package sample
type internalOnly struct { Value string ` + "`json:\"-\"`" + ` }
type wireFrame struct { Session string ` + "`json:\"session_id\"`" + ` }
`

	file, err := parser.ParseFile(token.NewFileSet(), "wire_fixture.go", fixture, 0)
	if err != nil {
		t.Fatal(err)
	}

	if got := jsonTaggedStructs(file); !slices.Equal(got, []string{"wireFrame"}) {
		t.Fatalf("handwritten JSON struct gate findings = %v, want [wireFrame]", got)
	}
}

func TestSignalingWireKeyGateRejectsUnregisteredKeysAndAllowsCallerKeys(t *testing.T) {
	t.Parallel()

	negative := `package sample
func f(fields map[string]any) {
	_ = fields["unregistered_nested_key"]
	_ = []string{"unregistered_required_key"}
	_ = map[string]any{"unregistered_payload_key": true}
}`

	file, err := parser.ParseFile(token.NewFileSet(), "negative.go", negative, 0)
	if err != nil {
		t.Fatal(err)
	}

	got := signalingWireKeyLiterals(file)

	want := []string{"unregistered_nested_key", "unregistered_payload_key", "unregistered_required_key"}

	if !slices.Equal(got, want) {
		t.Fatalf("literal key gate findings = %v, want %v", got, want)
	}

	positive := `package sample
func f(fields map[string]any, callerKey string) {
	_ = fields[callerKey]
	_ = fields[protocol.FieldBody]
	_ = []string{protocol.FieldBody}
	_ = map[string]any{protocol.FieldBody: true}
}`

	file, err = parser.ParseFile(token.NewFileSet(), "positive.go", positive, 0)
	if err != nil {
		t.Fatal(err)
	}

	if got := signalingWireKeyLiterals(file); len(got) != 0 {
		t.Fatalf("caller-defined or schema-constant keys were rejected: %v", got)
	}
}

func jsonTaggedStructs(file *ast.File) []string {
	var declarations []string

	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}

		for _, spec := range general.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}

			structure, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}

			for _, field := range structure.Fields.List {
				if field.Tag == nil {
					continue
				}

				rawTag, err := strconv.Unquote(field.Tag.Value)
				if err == nil {
					jsonName, found := reflect.StructTag(rawTag).Lookup("json")

					jsonName, _, _ = strings.Cut(jsonName, ",")

					if !found || jsonName == "-" {
						continue
					}

					declarations = append(declarations, typeSpec.Name.Name)

					break
				}
			}
		}
	}

	slices.Sort(declarations)

	return slices.Compact(declarations)
}

func signalingWireKeyLiterals(file *ast.File) []string {
	var keys []string

	ast.Inspect(file, func(node ast.Node) bool {
		keys = append(keys, wireKeyLiteralsForNode(node)...)

		return true
	})

	slices.Sort(keys)

	return keys
}

func wireKeyLiteralsForNode(node ast.Node) []string {
	var keys []string

	switch expression := node.(type) {
	case *ast.IndexExpr:
		if key, ok := stringBasicLiteral(expression.Index); ok {
			keys = append(keys, key)
		}
	case *ast.CompositeLit:
		keys = append(keys, stringListKeys(expression)...)
		keys = append(keys, stringMapKeys(expression)...)
	}

	return keys
}

func stringListKeys(expression *ast.CompositeLit) []string {
	arrayType, ok := expression.Type.(*ast.ArrayType)
	if !ok || arrayType.Len != nil || !isBuiltinString(arrayType.Elt) {
		return nil
	}

	var keys []string

	for _, element := range expression.Elts {
		if key, ok := stringBasicLiteral(element); ok {
			keys = append(keys, key)
		}
	}

	return keys
}

func stringMapKeys(expression *ast.CompositeLit) []string {
	mapType, ok := expression.Type.(*ast.MapType)
	if !ok || !isBuiltinString(mapType.Key) {
		return nil
	}

	var keys []string

	for _, element := range expression.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}

		if key, ok := stringBasicLiteral(pair.Key); ok {
			keys = append(keys, key)
		}
	}

	return keys
}

func findByKeyStringArguments(file *ast.File) []string {
	var keys []string

	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}

		name, ok := call.Fun.(*ast.Ident)
		if !ok || name.Name != "findByKey" {
			return true
		}

		if key, ok := stringBasicLiteral(call.Args[1]); ok {
			keys = append(keys, key)
		}

		return true
	})
	slices.Sort(keys)

	return keys
}

func schemaValueLiterals(file *ast.File, values map[string]bool) []string {
	var literals []string

	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}

		value, err := strconv.Unquote(literal.Value)
		if err == nil && values[value] {
			literals = append(literals, value)
		}

		return true
	})

	slices.Sort(literals)

	return literals
}

func collectSchemaPropertyNames(value any, properties map[string]bool) {
	switch current := value.(type) {
	case map[string]any:
		if nested := mapValue(current["properties"]); nested != nil {
			for name := range nested {
				properties[name] = true
			}
		}

		for _, child := range current {
			collectSchemaPropertyNames(child, properties)
		}
	case []any:
		for _, child := range current {
			collectSchemaPropertyNames(child, properties)
		}
	}
}

func collectSchemaKnownValues(value any, known map[string]bool) {
	switch current := value.(type) {
	case map[string]any:
		if constant, ok := current["const"].(string); ok {
			known[constant] = true
		}

		for _, name := range []string{"enum", "x-extensible-enum"} {
			for _, raw := range sliceValue(current[name]) {
				if constant, ok := raw.(string); ok {
					known[constant] = true
				}
			}
		}

		for _, child := range current {
			collectSchemaKnownValues(child, known)
		}
	case []any:
		for _, child := range current {
			collectSchemaKnownValues(child, known)
		}
	}
}

func stringBasicLiteral(expression ast.Expr) (string, bool) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}

	value, err := strconv.Unquote(literal.Value)

	return value, err == nil
}

func isBuiltinString(expression ast.Expr) bool {
	identifier, ok := expression.(*ast.Ident)

	return ok && identifier.Name == "string"
}
