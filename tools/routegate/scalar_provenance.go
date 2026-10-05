package routegate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
)

const maxScalarResolutionDepth = 64

type scalarSymbol struct {
	name        string
	scope       ast.Node
	scopeStart  token.Pos
	scopeEnd    token.Pos
	visibleFrom token.Pos
}

func unknownScalarOrigin() scalarOrigin {
	return scalarOrigin{
		hardcoded: false, generated: false, generatedValues: nil, generatedTypes: nil,
		caller: false, unknown: true, emptyOnly: false,
	}
}

func emptyScalarOrigin() scalarOrigin {
	return scalarOrigin{
		hardcoded: false, generated: false, generatedValues: nil, generatedTypes: nil,
		caller: false, unknown: false, emptyOnly: false,
	}
}

func hardcodedScalarOrigin(emptyOnly bool) scalarOrigin {
	return scalarOrigin{
		hardcoded: true, generated: false, generatedValues: nil, generatedTypes: nil,
		caller: false, unknown: false, emptyOnly: emptyOnly,
	}
}

func callerScalarOrigin() scalarOrigin {
	return scalarOrigin{
		hardcoded: false, generated: false, generatedValues: nil, generatedTypes: nil,
		caller: true, unknown: false, emptyOnly: false,
	}
}

func hardcodedUnknownScalarOrigin(hardcoded bool) scalarOrigin {
	return scalarOrigin{
		hardcoded: hardcoded, generated: false, generatedValues: nil, generatedTypes: nil,
		caller: false, unknown: true, emptyOnly: false,
	}
}

func functionScalarBinding(function ast.Node, source *scalarContext, file *parsedGoFile) scalarBinding {
	return scalarBinding{
		value: scalarOrigin{
			hardcoded: false, generated: false, generatedValues: nil, generatedTypes: nil,
			caller: false, unknown: false, emptyOnly: false,
		},
		hasValue: false, function: function, functionFile: file, source: source,
	}
}

func valueScalarBinding(value scalarOrigin) scalarBinding {
	return scalarBinding{
		value: value, hasValue: true, function: nil, functionFile: nil, source: nil,
	}
}

func globalScalarSymbol(name string) *scalarSymbol {
	return &scalarSymbol{
		name: name, scope: nil, scopeStart: token.NoPos, scopeEnd: token.NoPos, visibleFrom: token.NoPos,
	}
}

func emptyScalarBinding() scalarBinding {
	return scalarBinding{
		value: scalarOrigin{
			hardcoded: false, generated: false, generatedValues: nil, generatedTypes: nil,
			caller: false, unknown: false, emptyOnly: false,
		},
		hasValue: false, function: nil, functionFile: nil, source: nil,
	}
}

type scalarOrigin struct {
	hardcoded       bool
	generated       bool
	generatedValues map[string]bool
	generatedTypes  map[string]bool
	caller          bool
	unknown         bool
	emptyOnly       bool
}

type schemaPrimitiveField struct {
	omitEmpty     bool
	pointer       bool
	openValues    bool
	knownValues   map[string]bool
	generatedType string
}

type scalarDefinition struct {
	file        *parsedGoFile
	expression  ast.Expr
	resultIndex int
}

type scalarFunction struct {
	file     *parsedGoFile
	function *ast.FuncDecl
}

type scalarBinding struct {
	value        scalarOrigin
	hasValue     bool
	function     ast.Node
	functionFile *parsedGoFile
	source       *scalarContext
}

type scalarContext struct {
	file         *parsedGoFile
	parent       *scalarContext
	body         *ast.BlockStmt
	symbols      []*scalarSymbol
	declarations map[*ast.Ident]*scalarSymbol
	definitions  map[*scalarSymbol][]scalarDefinition
	bindings     map[*scalarSymbol]scalarBinding
	modelTypes   map[*scalarSymbol]string
	stringMaps   map[*scalarSymbol]bool
	rangeSources map[*scalarSymbol]scalarRangeSource
	namedResults map[string]*scalarSymbol
}

type scalarRangeSource struct {
	expression ast.Expr
	key        bool
}

type scalarResolver struct {
	modulePath     string
	packagePath    string
	protocolValues map[string]string
	imports        map[*ast.File]map[string]string
	globals        map[string][]scalarDefinition
	globalSymbols  map[string]*scalarSymbol
	functions      map[string][]scalarFunction
	functionDecls  []scalarFunction
	modelFields    map[string]map[string]schemaPrimitiveField
	generatedTypes map[string]bool
	structTypes    map[string]bool
	active         map[ast.Node]bool
	callContexts   map[*ast.FuncDecl][]*scalarContext
	contextsReady  bool
}

func newScalarResolver(
	root string,
	files []*parsedGoFile,
	protocolValues map[string]string,
	modelFields map[string]map[string]schemaPrimitiveField,
) (*scalarResolver, error) {
	modulePath, err := readModulePath(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}

	packagePath, err := scalarPackagePath(root, modulePath, files)
	if err != nil {
		return nil, err
	}

	resolver := newScalarResolverState(modulePath, packagePath, files, protocolValues, modelFields)
	for _, file := range files {
		resolver.indexFile(file)
	}

	return resolver, nil
}

func scalarPackagePath(root, modulePath string, files []*parsedGoFile) (string, error) {
	if len(files) == 0 {
		return modulePath, nil
	}

	relative, err := filepath.Rel(root, filepath.Dir(files[0].path))
	if err != nil {
		return "", wrapRouteGateError(err, "resolve scalar package path")
	}

	if relative == "." {
		return modulePath, nil
	}

	return modulePath + "/" + filepath.ToSlash(relative), nil
}

func newScalarResolverState(
	modulePath string,
	packagePath string,
	files []*parsedGoFile,
	protocolValues map[string]string,
	modelFields map[string]map[string]schemaPrimitiveField,
) *scalarResolver {
	return &scalarResolver{
		modulePath:     modulePath,
		packagePath:    packagePath,
		protocolValues: protocolValues,
		imports:        make(map[*ast.File]map[string]string, len(files)),
		globals:        make(map[string][]scalarDefinition),
		globalSymbols:  make(map[string]*scalarSymbol),
		functions:      make(map[string][]scalarFunction),
		functionDecls:  nil,
		modelFields:    modelFields,
		generatedTypes: generatedPrimitiveTypes(modelFields),
		structTypes:    make(map[string]bool),
		active:         make(map[ast.Node]bool),
		callContexts:   make(map[*ast.FuncDecl][]*scalarContext),
		contextsReady:  false,
	}
}

func (resolver *scalarResolver) indexFile(file *parsedGoFile) {
	resolver.imports[file.file] = importAliases(file.file)

	for _, declaration := range file.file.Decls {
		switch value := declaration.(type) {
		case *ast.GenDecl:
			resolver.indexGlobalDeclaration(file, value)
		case *ast.FuncDecl:
			resolver.functionDecls = append(resolver.functionDecls, scalarFunction{file: file, function: value})
			if value.Recv == nil {
				resolver.functions[value.Name.Name] = append(resolver.functions[value.Name.Name], scalarFunction{
					file: file, function: value,
				})
			}
		}
	}
}

func (resolver *scalarResolver) indexGlobalDeclaration(file *parsedGoFile, declaration *ast.GenDecl) {
	if declaration.Tok == token.TYPE {
		resolver.indexScalarTypeDeclarations(declaration)

		return
	}

	if declaration.Tok != token.CONST && declaration.Tok != token.VAR {
		return
	}

	for _, rawSpec := range declaration.Specs {
		specification, ok := rawSpec.(*ast.ValueSpec)
		if !ok {
			continue
		}

		resolver.indexGlobalValueSpec(file, specification)
	}
}

func (resolver *scalarResolver) indexScalarTypeDeclarations(declaration *ast.GenDecl) {
	for _, rawSpec := range declaration.Specs {
		specification, ok := rawSpec.(*ast.TypeSpec)
		if !ok {
			continue
		}

		if _, ok := unparen(specification.Type).(*ast.StructType); ok {
			resolver.structTypes[specification.Name.Name] = true
		}
	}
}

func (resolver *scalarResolver) indexGlobalValueSpec(file *parsedGoFile, specification *ast.ValueSpec) {
	for index, name := range specification.Names {
		resolver.globalSymbols[name.Name] = globalScalarSymbol(name.Name)

		if index < len(specification.Values) {
			resolver.globals[name.Name] = append(resolver.globals[name.Name], scalarDefinition{
				file: file, expression: specification.Values[index], resultIndex: 0,
			})
		}
	}
}

func protocolScalarConstants(root string) map[string]string {
	values := protocolStringConstants(root)

	paths, err := filepath.Glob(filepath.Join(root, "internal", "protocol", "*.go"))
	if err != nil {
		return values
	}

	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}

		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			continue
		}

		addProtocolFileConstants(values, file)
	}

	return values
}

func addProtocolFileConstants(values map[string]string, file *ast.File) {
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}

		for _, rawSpec := range general.Specs {
			specification, ok := rawSpec.(*ast.ValueSpec)
			if !ok {
				continue
			}

			for index, name := range specification.Names {
				if index >= len(specification.Values) {
					continue
				}

				if value, found := scalarConstantValue(specification.Values[index]); found {
					values[name.Name] = value
				}
			}
		}
	}
}

func scalarConstantValue(expression ast.Expr) (string, bool) {
	switch value := unparen(expression).(type) {
	case *ast.BasicLit:
		if value.Kind == token.STRING {
			text, err := strconv.Unquote(value.Value)

			return text, err == nil
		}

		if value.Kind == token.INT {
			integer, err := strconv.ParseInt(value.Value, 0, 64)
			if err == nil {
				return strconv.FormatInt(integer, 10), true
			}
		}
	case *ast.Ident:
		if value.Name == "true" || value.Name == "false" {
			return value.Name, true
		}
	}

	return "", false
}
