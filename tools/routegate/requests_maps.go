package routegate

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

type wireMapKind uint8

const (
	wireMapUnknown wireMapKind = iota
	wireMapQuery
	wireMapHeader
)

type wireOperationKeys struct {
	query  map[string]bool
	header map[string]bool
}

type generatedWireKeyRegistry struct {
	constants     map[string]map[string]string
	operations    map[string]wireOperationKeys
	globalHeaders map[string]bool
	err           error
}

type generatedConstSource struct {
	packagePath string
	file        *ast.File
}

type wireMapProvenance struct {
	kind           wireMapKind
	valid          bool
	fromURLQuery   bool
	requestHeaders *ast.Object
	keys           map[string]bool
	literal        *ast.CompositeLit
}

type requestWireMapState struct {
	function       *ast.FuncDecl
	requests       generatedRequestState
	context        generatedRequestAuditContext
	registry       *generatedWireKeyRegistry
	allowed        wireOperationKeys
	maps           map[*ast.Object]wireMapProvenance
	packageObjects map[*ast.Object]bool
	packageNames   map[string]bool
	queryKeys      map[*ast.Object]string
	headerKeys     map[*ast.Object]string
	rangeKeys      map[*ast.Object]wireMapProvenance
}

var generatedWireRegistryCache sync.Map

func cachedGeneratedWireKeyRegistry(root string, contracts Contracts) *generatedWireKeyRegistry {
	if cached, ok := generatedWireRegistryCache.Load(root); ok {
		registry, _ := cached.(*generatedWireKeyRegistry)
		if registry != nil {
			return registry
		}
	}

	registry := loadGeneratedWireKeyRegistry(root, contracts)
	cached, _ := generatedWireRegistryCache.LoadOrStore(root, registry)

	resolved, _ := cached.(*generatedWireKeyRegistry)
	if resolved != nil {
		return resolved
	}

	return registry
}

func loadGeneratedWireKeyRegistry(root string, contracts Contracts) *generatedWireKeyRegistry {
	registry := &generatedWireKeyRegistry{
		constants:     make(map[string]map[string]string),
		operations:    make(map[string]wireOperationKeys),
		globalHeaders: make(map[string]bool),
	}

	modulePath, err := readModulePath(filepath.Join(root, "go.mod"))
	if err != nil {
		registry.err = err

		return registry
	}

	registry.loadOpenAPIWireKeys(filepath.Join(root, "api", "openapi.yaml"))

	if registry.err != nil {
		return registry
	}

	files, err := goFiles(root)
	if err != nil {
		registry.err = err

		return registry
	}

	modelPackagePath := modulePath + "/pkg/dependencymodels/rest"
	sources := make([]generatedConstSource, 0)

	for _, path := range files {
		packagePath, pathErr := importPath(root, modulePath, filepath.Dir(path))
		if pathErr != nil {
			registry.err = pathErr

			return registry
		}

		if !hasGeneratedHTTPPath(contracts, packagePath) && packagePath != modelPackagePath {
			continue
		}

		raw, readErr := fs.ReadFile(os.DirFS(filepath.Dir(path)), filepath.Base(path))
		if readErr != nil {
			registry.err = wrapRouteGateError(readErr, "read generated HTTP keys from %s", path)

			return registry
		}

		file, parseErr := parser.ParseFile(token.NewFileSet(), path, raw, parser.ParseComments)
		if parseErr != nil {
			registry.err = wrapRouteGateError(parseErr, "parse generated HTTP keys from %s", path)

			return registry
		}

		sources = append(sources, generatedConstSource{packagePath: packagePath, file: file})
	}

	// Model-only OpenAPI packages own literal values; generated HTTP packages
	// re-export those values as selector aliases for API compatibility. Register
	// literals first, then resolve the aliases against the same inventory.
	for _, source := range sources {
		registry.addGeneratedConstants(source.packagePath, source.file, false)
	}

	for _, source := range sources {
		registry.addGeneratedConstants(source.packagePath, source.file, true)
	}

	return registry
}

func (registry *generatedWireKeyRegistry) loadOpenAPIWireKeys(path string) {
	var document map[string]any

	err := readYAML(path, &document)
	if err != nil {
		registry.err = err

		return
	}

	components := stringMap(document["components"])
	parameters := stringMap(components["parameters"])
	registry.readGlobalHeaderEnum(stringMap(components["schemas"]))

	for routePath, pathValue := range stringMap(document["paths"]) {
		pathItem := stringMap(pathValue)
		pathParameters := registry.resolveParameters(anySlice(pathItem["parameters"]), parameters)

		for method, operationValue := range pathItem {
			if !isHTTPMethod(method) {
				continue
			}

			operation := stringMap(operationValue)

			operationID := stringValue(operation["operationId"])
			if operationID == "" {
				continue
			}

			keys := wireOperationKeys{query: make(map[string]bool), header: make(map[string]bool)}
			mergeWireParameters(keys, pathParameters)
			mergeWireParameters(keys, registry.resolveParameters(anySlice(operation["parameters"]), parameters))
			registry.operations[operationID] = keys
			_ = routePath
		}
	}
}

func (registry *generatedWireKeyRegistry) readGlobalHeaderEnum(schemas map[string]any) {
	for _, schemaValue := range schemas {
		schema := stringMap(schemaValue)
		if stringValue(schema["type"]) != "string" {
			continue
		}

		name := stringValue(schema["title"])
		if name == "" {
			name = stringValue(schema["x-go-name"])
		}

		if name != "RingHeaderName" {
			continue
		}

		for _, value := range anySlice(schema["enum"]) {
			if key := stringValue(value); key != "" {
				registry.globalHeaders[http.CanonicalHeaderKey(key)] = true
			}
		}
	}

	if len(registry.globalHeaders) == 0 {
		if schema := stringMap(schemas["RingHeaderName"]); schema != nil {
			for _, value := range anySlice(schema["enum"]) {
				if key := stringValue(value); key != "" {
					registry.globalHeaders[http.CanonicalHeaderKey(key)] = true
				}
			}
		}
	}
}

func (registry *generatedWireKeyRegistry) resolveParameters(values []any, components map[string]any) []map[string]any {
	result := make([]map[string]any, 0, len(values))

	for _, value := range values {
		parameter := stringMap(value)
		if reference := stringValue(parameter["$ref"]); reference != "" {
			resolved := components[refName(reference)]
			parameter = stringMap(resolved)
		}

		if parameter != nil {
			result = append(result, parameter)
		}
	}

	return result
}

func mergeWireParameters(destination wireOperationKeys, parameters []map[string]any) {
	for _, parameter := range parameters {
		name := stringValue(parameter["name"])

		switch stringValue(parameter["in"]) {
		case "query":
			if name != "" {
				destination.query[name] = true
			}
		case "header":
			if name != "" {
				destination.header[http.CanonicalHeaderKey(name)] = true
			}
		}
	}
}

func (registry *generatedWireKeyRegistry) addGeneratedConstants(packagePath string, file *ast.File, aliases bool) {
	if registry.constants[packagePath] == nil {
		registry.constants[packagePath] = make(map[string]string)
	}

	specifications := generatedConstantSpecifications(file)
	if aliases {
		registry.addConstantAliases(packagePath, specifications, importAliases(file))

		return
	}

	registry.addConstantStrings(packagePath, specifications)
}

func generatedConstantSpecifications(file *ast.File) []*ast.ValueSpec {
	specifications := make([]*ast.ValueSpec, 0)

	for _, declaration := range file.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}

		for _, specification := range group.Specs {
			value, isValue := specification.(*ast.ValueSpec)
			if isValue {
				specifications = append(specifications, value)
			}
		}
	}

	return specifications
}

func (registry *generatedWireKeyRegistry) addConstantStrings(packagePath string, specifications []*ast.ValueSpec) {
	for _, specification := range specifications {
		for index, name := range specification.Names {
			if index >= len(specification.Values) {
				continue
			}

			literal, ok := specification.Values[index].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}

			text, err := strconv.Unquote(literal.Value)
			if err == nil {
				registry.constants[packagePath][name.Name] = text
			}
		}
	}
}

func (registry *generatedWireKeyRegistry) addConstantAliases(
	packagePath string,
	specifications []*ast.ValueSpec,
	imports map[string]string,
) {
	for _, specification := range specifications {
		for index, name := range specification.Names {
			if index >= len(specification.Values) {
				continue
			}

			value, exists := registry.constantAliasValue(specification.Values[index], imports)
			if exists {
				registry.constants[packagePath][name.Name] = value
			}
		}
	}
}

func (registry *generatedWireKeyRegistry) constantAliasValue(expression ast.Expr, imports map[string]string) (string, bool) {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}

	qualifier, ok := selector.X.(*ast.Ident)
	if !ok {
		return "", false
	}

	packagePath := imports[qualifier.Name]
	if packagePath == "" {
		return "", false
	}

	value, exists := registry.constants[packagePath][selector.Sel.Name]

	return value, exists
}

func newRequestWireMapState(
	function *ast.FuncDecl,
	requests generatedRequestState,
	context generatedRequestAuditContext,
) *requestWireMapState {
	state := &requestWireMapState{
		function:       function,
		requests:       requests,
		context:        context,
		registry:       cachedGeneratedWireKeyRegistry(context.root, context.contracts),
		allowed:        wireOperationKeys{query: make(map[string]bool), header: make(map[string]bool)},
		maps:           make(map[*ast.Object]wireMapProvenance),
		packageObjects: packageScopeObjects(context.packageFiles),
		packageNames:   packageScopeNames(context.packageFiles),
		queryKeys:      make(map[*ast.Object]string),
		headerKeys:     make(map[*ast.Object]string),
		rangeKeys:      make(map[*ast.Object]wireMapProvenance),
	}

	for _, route := range requests.routes {
		keys := state.registry.operations[route.OperationID]
		mergeWireKeySet(state.allowed.query, keys.query)
		mergeWireKeySet(state.allowed.header, keys.header)
	}

	mergeWireKeySet(state.allowed.header, state.registry.globalHeaders)

	if state.registry.err != nil {
		context.add(function, "generated-wire-key-inventory", fmt.Sprintf("cannot load generated query/header key inventory: %v", state.registry.err))
	}

	return state
}

func packageScopeObjects(packageFiles []*parsedGoFile) map[*ast.Object]bool {
	objects := make(map[*ast.Object]bool)

	for _, source := range packageFiles {
		for _, declaration := range source.file.Decls {
			group, ok := declaration.(*ast.GenDecl)
			if !ok || group.Tok != token.VAR && group.Tok != token.CONST {
				continue
			}

			for _, specification := range group.Specs {
				value, ok := specification.(*ast.ValueSpec)
				if !ok {
					continue
				}

				for _, name := range value.Names {
					if name.Obj != nil {
						objects[name.Obj] = true
					}
				}
			}
		}
	}

	return objects
}

func packageScopeNames(packageFiles []*parsedGoFile) map[string]bool {
	names := make(map[string]bool)

	for _, source := range packageFiles {
		for _, declaration := range source.file.Decls {
			group, ok := declaration.(*ast.GenDecl)
			if !ok || group.Tok != token.VAR && group.Tok != token.CONST {
				continue
			}

			for _, specification := range group.Specs {
				value, ok := specification.(*ast.ValueSpec)
				if !ok {
					continue
				}

				for _, name := range value.Names {
					names[name.Name] = true
				}
			}
		}
	}

	return names
}

func mergeWireKeySet(destination, source map[string]bool) {
	for key := range source {
		destination[key] = true
	}
}
