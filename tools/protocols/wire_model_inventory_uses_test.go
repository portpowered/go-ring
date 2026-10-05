package protocols_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

type modelParentUse struct {
	parent string
	field  string
}

func productionModelUses(
	t *testing.T,
	root string,
	models []documentedWireModel,
) (map[string][]string, map[string][]modelParentUse) {
	t.Helper()

	typeIndex := make(map[string]map[string]string)
	for _, model := range models {
		if typeIndex[model.packagePath] == nil {
			typeIndex[model.packagePath] = make(map[string]string)
		}

		name := strings.TrimPrefix(model.goType, packageNameFromType(model.goType)+".")
		typeIndex[model.packagePath][name] = model.family + "/" + model.goType
	}

	aliases := compatibilityModelAliases(t, root, typeIndex)
	directUses := make(map[string][]string)
	modelPackageDirectories := map[string]bool{
		"pkg/dependencymodels/rest":               true,
		"pkg/dependencymodels/fcm":                true,
		"pkg/dependencymodels/signaling":          true,
		"pkg/ringapimodels":                       true,
		"third_party/go-push-receiver/pb/checkin": true,
		"third_party/go-push-receiver/pb/mcs":     true,
	}

	files := productionGoFiles(t, root, modelPackageDirectories)
	for _, path := range files {
		fileSet := token.NewFileSet()

		file, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			t.Fatalf("parse production source %s: %v", path, err)
		}

		imports := modelImportAliases(file)

		relative, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}

		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			packageName, ok := selector.X.(*ast.Ident)
			if !ok {
				return true
			}

			packagePath := imports[packageName.Name]

			modelKey := typeIndex[packagePath][selector.Sel.Name]

			if modelKey == "" {
				modelKey = aliases[packagePath][selector.Sel.Name]
			}

			if modelKey != "" {
				use := filepath.ToSlash(relative) + ":" + strconv.Itoa(fileSet.Position(selector.Sel.Pos()).Line)
				directUses[modelKey] = append(directUses[modelKey], use)
			}

			return true
		})
	}

	parents := generatedModelParents(models)

	for modelKey := range directUses {
		slices.Sort(directUses[modelKey])
		directUses[modelKey] = slices.Compact(directUses[modelKey])
	}

	return directUses, parents
}

func productionGoFiles(t *testing.T, root string, modelDirectories map[string]bool) []string {
	t.Helper()

	var files []string

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "vendor" || name == "node_modules" || name == "dist" {
				return filepath.SkipDir
			}

			return nil
		}

		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".gen.go") {
			return nil
		}

		relative, err := filepath.Rel(root, path)
		if err != nil {
			return &os.PathError{Op: "relative path", Path: path, Err: err}
		}

		if modelDirectories[filepath.ToSlash(filepath.Dir(relative))] {
			return nil
		}

		files = append(files, path)

		return nil
	})
	if err != nil {
		t.Fatalf("walk production Go files: %v", err)
	}

	slices.Sort(files)

	return files
}

func compatibilityModelAliases(
	t *testing.T,
	root string,
	typeIndex map[string]map[string]string,
) map[string]map[string]string {
	t.Helper()

	aliases := make(map[string]map[string]string)

	files := []string{
		"internal/generatedhttp/models_compat.gen.go", "pkg/generatedhttp/models_compat.gen.go",
		"internal/generatedfcm/models_compat.gen.go", "internal/generatedsignaling/models_compat.gen.go",
		"pkg/generatedsignaling/models_compat.gen.go",
	}
	for _, relative := range files {
		packagePath, fileAliases, exists := compatibilityAliasesForFile(t, root, relative, typeIndex)
		if !exists {
			continue
		}

		aliases[packagePath] = fileAliases
	}

	return aliases
}

func compatibilityAliasesForFile(
	t *testing.T,
	root, relative string,
	typeIndex map[string]map[string]string,
) (string, map[string]string, bool) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(relative))

	_, err := os.Stat(path)
	if err != nil {
		return "", nil, false
	}

	fileSet := token.NewFileSet()

	file, err := parser.ParseFile(fileSet, path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse compatibility model aliases %s: %v", relative, err)
	}

	imports := modelImportAliases(file)
	packagePath := wireInventoryModulePath + "/" + filepath.ToSlash(filepath.Dir(relative))
	aliases := make(map[string]string)

	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}

		for _, specification := range general.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if !ok {
				continue
			}

			selector, ok := typeSpec.Type.(*ast.SelectorExpr)
			if !ok {
				continue
			}

			packageName, ok := selector.X.(*ast.Ident)
			if !ok {
				continue
			}

			origin := typeIndex[imports[packageName.Name]][selector.Sel.Name]
			if origin != "" {
				aliases[typeSpec.Name.Name] = origin
			}
		}
	}

	return packagePath, aliases, true
}

func modelImportAliases(file *ast.File) map[string]string {
	imports := make(map[string]string, len(file.Imports))

	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			continue
		}

		name := filepath.Base(path)
		if imported.Name != nil {
			name = imported.Name.Name
		}

		if name != "_" && name != "." {
			imports[name] = path
		}
	}

	return imports
}

func generatedModelParents(models []documentedWireModel) map[string][]modelParentUse {
	typeIndex := make(map[string]map[string]string)
	for _, model := range models {
		if typeIndex[model.packagePath] == nil {
			typeIndex[model.packagePath] = make(map[string]string)
		}

		name := strings.TrimPrefix(model.goType, packageNameFromType(model.goType)+".")
		typeIndex[model.packagePath][name] = model.family + "/" + model.goType
	}

	parents := make(map[string][]modelParentUse)

	for _, model := range models {
		structure, ok := model.typeSpec.Type.(*ast.StructType)
		if !ok {
			continue
		}

		for _, field := range structure.Fields.List {
			fieldName := fieldWireName(field)
			ast.Inspect(field.Type, func(node ast.Node) bool {
				identifier, ok := node.(*ast.Ident)
				if !ok {
					return true
				}

				childKey := typeIndex[model.packagePath][identifier.Name]

				parentKey := model.family + "/" + model.goType

				if childKey != "" && childKey != parentKey {
					parents[childKey] = append(parents[childKey], modelParentUse{
						parent: parentKey, field: model.goType + "." + fieldName,
					})
				}

				return true
			})
		}
	}

	return parents
}

func documentedModelUses(
	model documentedWireModel,
	directUses map[string][]string,
	parents map[string][]modelParentUse,
) []string {
	key := model.family + "/" + model.goType
	uses := append([]string(nil), directUses[key]...)

	type parentWalk struct {
		modelKey string
		fields   []string
	}

	queue := []parentWalk{{modelKey: key, fields: nil}}
	visited := map[string]bool{key: true}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		for _, parent := range parents[current.modelKey] {
			fields := append(slices.Clone(current.fields), parent.field)

			if usesAt := directUses[parent.parent]; len(usesAt) > 0 {
				for _, source := range usesAt {
					uses = append(uses, "field "+strings.Join(fields, " → ")+" via "+source)
				}
			}

			if !visited[parent.parent] {
				visited[parent.parent] = true

				queue = append(queue, parentWalk{modelKey: parent.parent, fields: fields})
			}
		}
	}

	if len(uses) == 0 {
		return []string{"No direct production selector or used parent model found"}
	}

	slices.Sort(uses)

	return slices.Compact(uses)
}
