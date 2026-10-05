package main

import (
	"bytes"
	"errors"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

var modelTargetComment = regexp.MustCompile(`defines model for ([^.]+)(?:\.|$)`)

type modelOwnership struct {
	groups         map[string]struct{}
	schemas        map[string]string
	parameters     map[string]string
	responses      map[string]string
	requestBodies  map[string]string
	operations     map[string]string
	opPrefixes     []namedOwner
	schemaPrefixes []namedOwner
}

type namedOwner struct {
	name  string
	group string
}

func splitModels(openAPIPath, modelPath, outputDir string) error {
	ownership, err := readModelOwnership(openAPIPath)
	if err != nil {
		return err
	}

	source, err := readLocalFile(modelPath)
	if err != nil {
		return errorf("read generated REST model source %s: %w", modelPath, err)
	}

	marker, err := generatedMarker(source)
	if err != nil {
		return errorf("refuse to split %s: %w", modelPath, err)
	}

	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, modelPath, source, parser.ParseComments|parser.AllErrors)
	if err != nil {
		return errorf("parse generated REST model source %s: %w", modelPath, err)
	}

	typeGroups, err := collectGeneratedTypeGroups(file, ownership)
	if err != nil {
		return err
	}

	declarations, err := collectGeneratedDeclarations(file, typeGroups, ownership)
	if err != nil {
		return err
	}

	if len(declarations) == 0 {
		return errorf("generated REST model source %s contains no grouped declarations", modelPath)
	}

	groupNames := make([]string, 0, len(declarations))

	for group, decls := range declarations {
		if len(decls) == 0 {
			continue
		}

		groupNames = append(groupNames, group)
	}

	sort.Strings(groupNames)

	generated := make(map[string][]byte, len(groupNames))

	for _, group := range groupNames {
		content, err := renderModelGroup(file, fset, marker, group, declarations[group])
		if err != nil {
			return errorf("render generated REST model group %s: %w", group, err)
		}

		generated[group] = content
	}

	const outputDirectoryMode = 0o750

	err = os.MkdirAll(outputDir, outputDirectoryMode)
	if err != nil {
		return errorf("create REST model output directory %s: %w", outputDir, err)
	}

	for _, group := range groupNames {
		path := filepath.Join(outputDir, group+".gen.go")

		err := ensureGeneratedTarget(path)
		if err != nil {
			return err
		}
	}

	for _, group := range groupNames {
		path := filepath.Join(outputDir, group+".gen.go")

		err := os.WriteFile(path, generated[group], 0o600)
		if err != nil {
			return errorf("write generated REST model group %s: %w", group, err)
		}
	}

	err = os.Remove(modelPath)
	if err != nil {
		return errorf("remove superseded generated model file %s: %w", modelPath, err)
	}

	return nil
}

func readModelOwnership(openAPIPath string) (*modelOwnership, error) {
	fragmentDir := filepath.Join(filepath.Dir(openAPIPath), "schemas")

	ownership, err := readSchemaGroupOwners(fragmentDir)
	if err != nil {
		return nil, err
	}

	data, err := readLocalFile(openAPIPath)
	if err != nil {
		return nil, errorf("read bundled OpenAPI document %s: %w", openAPIPath, err)
	}

	doc, err := parseYAML(data, openAPIPath)
	if err != nil {
		return nil, err
	}

	pathsNode := mappingValue(documentRoot(doc), "paths")
	if pathsNode == nil || pathsNode.Kind != yaml.MappingNode {
		return nil, errorf("%s has no OpenAPI paths mapping", openAPIPath)
	}

	err = readOperationOwners(pathsNode, ownership)
	if err != nil {
		return nil, err
	}

	buildOwnerPrefixes(ownership)

	return ownership, nil
}

func readSchemaGroupOwners(fragmentDir string) (*modelOwnership, error) {
	paths, err := filepath.Glob(filepath.Join(fragmentDir, "*.yaml"))
	if err != nil {
		return nil, errorf("list REST schema groups: %w", err)
	}

	sort.Strings(paths)

	if len(paths) == 0 {
		return nil, errorf("no REST schema groups found in %s", fragmentDir)
	}

	ownership := &modelOwnership{
		groups:         make(map[string]struct{}),
		schemas:        make(map[string]string),
		parameters:     make(map[string]string),
		responses:      make(map[string]string),
		requestBodies:  make(map[string]string),
		operations:     make(map[string]string),
		opPrefixes:     nil,
		schemaPrefixes: nil,
	}

	for _, path := range paths {
		fragment, err := readFragment(path)
		if err != nil {
			return nil, err
		}

		ownership.groups[fragment.group] = struct{}{}

		err = registerFragmentOwners(fragment, ownership)
		if err != nil {
			return nil, err
		}
	}

	return ownership, nil
}

func registerFragmentOwners(fragment schemaFragment, ownership *modelOwnership) error {
	for section, components := range fragment.components {
		for name := range components {
			owners := ownership.sectionOwners(section)
			if previous, exists := owners[name]; exists {
				return errorf(
					"generated model component %s/%s has duplicate owners %s and %s",
					section,
					name,
					previous,
					fragment.group,
				)
			}

			owners[name] = fragment.group
		}
	}

	return nil
}

func readOperationOwners(pathsNode *yaml.Node, ownership *modelOwnership) error {
	for pathEntryIndex := 0; pathEntryIndex+1 < len(pathsNode.Content); pathEntryIndex += 2 {
		path := pathsNode.Content[pathEntryIndex].Value

		pathItem := pathsNode.Content[pathEntryIndex+1]
		if pathItem.Kind != yaml.MappingNode {
			continue
		}

		for methodEntryIndex := 0; methodEntryIndex+1 < len(pathItem.Content); methodEntryIndex += 2 {
			method := strings.ToLower(pathItem.Content[methodEntryIndex].Value)
			if !httpMethod(method) {
				continue
			}

			err := registerOperationOwner(path, method, pathItem.Content[methodEntryIndex+1], ownership)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func registerOperationOwner(path, method string, operation *yaml.Node, ownership *modelOwnership) error {
	operationID := scalarValue(mappingValue(operation, "operationId"))
	if operationID == "" {
		return errorf("OpenAPI operation %s %s has no operationId", method, path)
	}

	tags := mappingValue(operation, "tags")
	if tags == nil || tags.Kind != yaml.SequenceNode || len(tags.Content) != 1 {
		return errorf("OpenAPI operation %s %s must have exactly one API responsibility tag", method, path)
	}

	group := responsibilitySlug(tags.Content[0].Value)
	if _, exists := ownership.groups[group]; !exists {
		return errorf(
			"OpenAPI operation %s %s tag %q has no canonical api/schemas/%s.yaml owner",
			method,
			path,
			tags.Content[0].Value,
			group,
		)
	}

	goName := exportedIdentifier(operationID)
	if previous, exists := ownership.operations[goName]; exists {
		return errorf("operation model prefix %s is shared by responsibility groups %s and %s", goName, previous, group)
	}

	ownership.operations[goName] = group

	return nil
}

func buildOwnerPrefixes(ownership *modelOwnership) {
	for name, group := range ownership.operations {
		ownership.opPrefixes = append(ownership.opPrefixes, namedOwner{name: name, group: group})
	}

	for name, group := range ownership.schemas {
		ownership.schemaPrefixes = append(ownership.schemaPrefixes, namedOwner{name: name, group: group})
	}

	sort.Slice(ownership.opPrefixes, func(i, j int) bool {
		return namedOwnerLess(ownership.opPrefixes[i], ownership.opPrefixes[j])
	})
	sort.Slice(ownership.schemaPrefixes, func(i, j int) bool {
		return namedOwnerLess(ownership.schemaPrefixes[i], ownership.schemaPrefixes[j])
	})
}

func namedOwnerLess(left, right namedOwner) bool {
	if len(left.name) != len(right.name) {
		return len(left.name) > len(right.name)
	}

	if left.name != right.name {
		return left.name < right.name
	}

	return left.group < right.group
}

func (o *modelOwnership) sectionOwners(section string) map[string]string {
	switch section {
	case "schemas":
		return o.schemas
	case "parameters":
		return o.parameters
	case "responses":
		return o.responses
	case "requestBodies":
		return o.requestBodies
	default:
		return nil
	}
}

func collectGeneratedTypeGroups(file *ast.File, ownership *modelOwnership) (map[string]string, error) {
	groups := make(map[string]string)

	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}

		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				return nil, errorf("unexpected generated type declaration %T", spec)
			}

			comment := declarationComment(typeSpec.Doc, genDecl.Doc)

			group, err := ownership.groupForType(typeSpec.Name.Name, comment)
			if err != nil {
				return nil, errorf("generated type %s: %w", typeSpec.Name.Name, err)
			}

			groups[typeSpec.Name.Name] = group
		}
	}

	return groups, nil
}

func collectGeneratedDeclarations(
	file *ast.File,
	typeGroups map[string]string,
	ownership *modelOwnership,
) (map[string][]ast.Decl, error) {
	declarations := make(map[string][]ast.Decl)

	for _, decl := range file.Decls {
		switch typed := decl.(type) {
		case *ast.GenDecl:
			if typed.Tok == token.IMPORT {
				continue
			}

			groups, err := declarationGroups(typed, typeGroups, ownership)
			if err != nil {
				return nil, err
			}

			for group, specs := range groups {
				copyDecl := *typed
				copyDecl.Specs = specs
				declarations[group] = append(declarations[group], &copyDecl)
			}
		case *ast.FuncDecl:
			var group string

			switch {
			case typed.Recv != nil && len(typed.Recv.List) > 0:
				receiver := receiverTypeName(typed.Recv.List[0].Type)

				var exists bool

				group, exists = typeGroups[receiver]
				if !exists {
					return nil, errorf("generated method %s has an unowned receiver type %s", typed.Name.Name, receiver)
				}
			case typed.Name.Name == "init":
				group = "common"
			default:
				var err error

				group, err = ownership.groupForType(typed.Name.Name, declarationComment(typed.Doc, nil))
				if err != nil {
					return nil, errorf("generated function %s: %w", typed.Name.Name, err)
				}
			}

			declarations[group] = append(declarations[group], typed)
		default:
			return nil, errorf("unsupported generated REST declaration %T", decl)
		}
	}

	for group := range declarations {
		if _, exists := ownership.groups[group]; !exists {
			return nil, errorf("generated declarations resolve to unknown responsibility group %q", group)
		}
	}

	return declarations, nil
}

func declarationGroups(
	decl *ast.GenDecl,
	typeGroups map[string]string,
	ownership *modelOwnership,
) (map[string][]ast.Spec, error) {
	grouped := make(map[string][]ast.Spec)
	previousGroup := ""

	for _, spec := range decl.Specs {
		var (
			group string
			err   error
		)

		switch typed := spec.(type) {
		case *ast.TypeSpec:
			group = typeGroups[typed.Name.Name]
			if group == "" {
				err = errorf("type %s has no responsibility owner", typed.Name.Name)
			}
		case *ast.ValueSpec:
			switch {
			case typed.Type != nil:
				typeName := expressionTypeName(typed.Type)

				group = typeGroups[typeName]
				if group == "" {
					group, err = ownership.groupForType(typeName, "")
				}
			case previousGroup != "":
				group = previousGroup
			default:
				err = errorf("generated value declaration has no schema-owned type")
			}
		default:
			err = errorf("unsupported generated specification %T", spec)
		}

		if err != nil {
			return nil, err
		}

		if group == "" {
			return nil, errorf("generated specification %T has no responsibility owner", spec)
		}

		grouped[group] = append(grouped[group], spec)
		previousGroup = group
	}

	return grouped, nil
}

func (o *modelOwnership) groupForType(name, comment string) (string, error) {
	for _, candidate := range o.opPrefixes {
		if strings.HasPrefix(name, candidate.name) {
			return candidate.group, nil
		}
	}

	for _, candidate := range o.schemaPrefixes {
		if name == candidate.name || strings.HasPrefix(name, candidate.name) {
			return candidate.group, nil
		}
	}

	if group, exists := o.responses[name]; exists {
		return group, nil
	}

	if group, exists := o.requestBodies[name]; exists {
		return group, nil
	}

	if match := modelTargetComment.FindStringSubmatch(comment); len(match) == 2 {
		target := strings.TrimSpace(match[1])
		if group, exists := o.parameters[target]; exists {
			return group, nil
		}

		if group, exists := o.schemas[target]; exists {
			return group, nil
		}
	}

	return "", errorf("no owning component or tagged operation for generated type (comment %q)", comment)
}

func renderModelGroup(file *ast.File, fset *token.FileSet, marker, group string, decls []ast.Decl) ([]byte, error) {
	imports, err := usedImports(file, decls)
	if err != nil {
		return nil, err
	}

	var source bytes.Buffer

	if file.Doc != nil {
		for _, comment := range file.Doc.List {
			source.WriteString(comment.Text)
			source.WriteByte('\n')
		}

		source.WriteByte('\n')
	}

	source.WriteString(marker)
	source.WriteString("\n// Responsibility group: ")
	source.WriteString(group)
	source.WriteString(".\n\npackage ")
	source.WriteString(file.Name.Name)
	source.WriteString("\n\n")

	if len(imports) > 0 {
		source.WriteString("import (\n")

		for _, imported := range imports {
			source.WriteString("\t")

			if imported.alias != "" {
				source.WriteString(imported.alias)
				source.WriteByte(' ')
			}

			source.WriteString(strconv.Quote(imported.path))
			source.WriteByte('\n')
		}

		source.WriteString(")\n\n")
	}

	for _, decl := range decls {
		err := format.Node(&source, fset, decl)
		if err != nil {
			return nil, errorf("format generated declaration: %w", err)
		}

		source.WriteString("\n\n")
	}

	formatted, err := format.Source(source.Bytes())
	if err != nil {
		return nil, errorf("format generated group: %w", err)
	}

	return formatted, nil
}

type generatedImport struct {
	alias string
	path  string
}

func usedImports(file *ast.File, decls []ast.Decl) ([]generatedImport, error) {
	byQualifier := make(map[string]generatedImport)

	for _, decl := range file.Decls {
		importDecl, ok := decl.(*ast.GenDecl)
		if !ok || importDecl.Tok != token.IMPORT {
			continue
		}

		for _, spec := range importDecl.Specs {
			importSpec, ok := spec.(*ast.ImportSpec)
			if !ok {
				return nil, errorf("unexpected generated import %T", spec)
			}

			path, err := strconv.Unquote(importSpec.Path.Value)
			if err != nil {
				return nil, errorf("parse generated import %s: %w", importSpec.Path.Value, err)
			}

			qualifier := filepath.Base(path)
			alias := ""

			if importSpec.Name != nil {
				qualifier = importSpec.Name.Name
				if qualifier != filepath.Base(path) {
					alias = qualifier
				}
			}

			byQualifier[qualifier] = generatedImport{alias: alias, path: path}
		}
	}

	used := make(map[string]generatedImport)

	for _, decl := range decls {
		ast.Inspect(decl, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			qualifier, ok := selector.X.(*ast.Ident)
			if !ok {
				return true
			}

			if imported, exists := byQualifier[qualifier.Name]; exists {
				used[imported.path] = imported
			}

			return true
		})
	}

	imports := make([]generatedImport, 0, len(used))
	for _, imported := range used {
		imports = append(imports, imported)
	}

	sort.Slice(imports, func(i, j int) bool { return imports[i].path < imports[j].path })

	return imports, nil
}

func generatedMarker(source []byte) (string, error) {
	for _, line := range strings.Split(string(source), "\n") {
		line = strings.TrimSuffix(line, "\r")

		isGeneratorMarker := strings.HasPrefix(line, "// Code generated by github.com/oapi-codegen/oapi-codegen/") &&
			strings.Contains(line, "DO NOT EDIT.")

		if isGeneratorMarker {
			return line, nil
		}
	}

	return "", errorf("missing oapi-codegen generated marker")
}

func ensureGeneratedTarget(path string) error {
	data, err := readLocalFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return errorf("inspect generated REST model target %s: %w", path, err)
	}

	_, err = generatedMarker(data)
	if err != nil {
		return errorf("refuse to overwrite %s: %w", path, err)
	}

	return nil
}

func declarationComment(specDoc, declDoc *ast.CommentGroup) string {
	if specDoc != nil {
		return specDoc.Text()
	}

	if declDoc != nil {
		return declDoc.Text()
	}

	return ""
}

func receiverTypeName(expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.StarExpr:
		return receiverTypeName(typed.X)
	case *ast.IndexExpr:
		return receiverTypeName(typed.X)
	case *ast.IndexListExpr:
		return receiverTypeName(typed.X)
	default:
		return ""
	}
}

func expressionTypeName(expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.StarExpr:
		return expressionTypeName(typed.X)
	case *ast.SelectorExpr:
		return typed.Sel.Name
	default:
		return ""
	}
}

func scalarValue(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}

	return node.Value
}

func httpMethod(method string) bool {
	switch method {
	case "get", "put", "post", "delete", "options", "head", "patch", "trace":
		return true
	default:
		return false
	}
}

func responsibilitySlug(tag string) string {
	var out strings.Builder

	previousDash := false

	for _, r := range strings.ToLower(tag) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)

			previousDash = false

			continue
		}

		if !previousDash && out.Len() > 0 {
			out.WriteByte('-')

			previousDash = true
		}
	}

	return strings.Trim(out.String(), "-")
}

func exportedIdentifier(name string) string {
	if name == "" {
		return ""
	}

	return strings.ToUpper(name[:1]) + name[1:]
}
