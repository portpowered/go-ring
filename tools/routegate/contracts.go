package routegate

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type SignalingRoute struct {
	OperationID string
	Channel     string
	Address     string
	Server      string
	Frame       string
	Body        string
	Method      string
}

type SignalingChannel struct {
	Address  string
	Server   string
	Query    AsyncQuery
	HasQuery bool
}

type AsyncQuery struct {
	Required             []string
	Properties           map[string]AsyncQueryProperty
	AdditionalProperties bool
}

type AsyncQueryProperty struct {
	Type      string
	Const     string
	Enum      []string
	Pattern   string
	MinLength int
}

type Contracts struct {
	HTTPRoutes             map[string]HTTPRoute
	HTTPServers            map[string]struct{}
	GeneratedHTTP          map[string]map[string]HTTPRoute
	GeneratedHTTPRequests  map[string]map[string]HTTPRoute
	GeneratedHTTPPaths     map[string]struct{}
	GeneratedFrames        map[string]string
	GeneratedGoNames       map[string]string
	GeneratedFramePaths    map[string]struct{}
	SignalingRoutes        map[string][]SignalingRoute
	SignalingInboundRoutes map[string][]SignalingRoute
	SignalingChannels      map[string]SignalingChannel
	SignalingServers       map[string]string
	Diagnostics            []Finding
}

type Finding struct {
	Path    string
	Line    int
	Rule    string
	Message string
}

func (finding Finding) String() string {
	if finding.Line > 0 {
		return fmt.Sprintf("%s:%d: %s: %s", finding.Path, finding.Line, finding.Rule, finding.Message)
	}

	return fmt.Sprintf("%s: %s: %s", finding.Path, finding.Rule, finding.Message)
}

type asyncModel struct {
	channels   map[string]asyncChannel
	servers    map[string]asyncServer
	operations map[string]asyncOperation
	messages   map[string]asyncMessage
	schemas    map[string]asyncSchema
}

type asyncChannel struct {
	Address  string
	Server   string
	Messages map[string]string
	Query    AsyncQuery
	HasQuery bool
}

type asyncServer struct {
	Host     string
	Protocol string
}

type asyncOperation struct {
	Action  string
	Channel string
}

type asyncMessage struct {
	Frame string
}

type asyncSchema struct {
	Method string
	Body   string
}

const generatedRouteMatchParts = 4

var generatedRoutePattern = regexp.MustCompile(
	"Corresponds with ([A-Z]+) ([^\\s]+) \\(the `([A-Za-z0-9_]+)` operationId\\)",
)

func LoadContracts(root string) (Contracts, error) {
	modulePath, err := readModulePath(filepath.Join(root, "go.mod"))
	if err != nil {
		return Contracts{}, err
	}

	openAPIRoutes, err := readOpenAPIRoutes(filepath.Join(root, "api", "openapi.yaml"))
	if err != nil {
		return Contracts{}, err
	}

	openAPIServers, err := readOpenAPIServers(filepath.Join(root, "api", "openapi.yaml"))
	if err != nil {
		return Contracts{}, err
	}

	async, err := readAsyncModel(filepath.Join(root, "api", "asyncapi.yaml"))
	if err != nil {
		return Contracts{}, err
	}

	contracts := Contracts{
		HTTPRoutes:             openAPIRoutes,
		HTTPServers:            openAPIServers,
		GeneratedHTTP:          make(map[string]map[string]HTTPRoute),
		GeneratedHTTPRequests:  make(map[string]map[string]HTTPRoute),
		GeneratedHTTPPaths:     make(map[string]struct{}),
		GeneratedFrames:        make(map[string]string),
		GeneratedGoNames:       make(map[string]string),
		GeneratedFramePaths:    make(map[string]struct{}),
		SignalingRoutes:        make(map[string][]SignalingRoute),
		SignalingInboundRoutes: make(map[string][]SignalingRoute),
		SignalingChannels:      make(map[string]SignalingChannel),
		SignalingServers:       make(map[string]string),
		Diagnostics:            nil,
	}

	err = addAsyncContractRoutes(async, &contracts)
	if err != nil {
		return Contracts{}, err
	}

	err = discoverGeneratedHTTP(root, modulePath, openAPIRoutes, &contracts)
	if err != nil {
		return Contracts{}, err
	}

	err = discoverGeneratedFrames(root, modulePath, async, &contracts)
	if err != nil {
		return Contracts{}, err
	}

	return contracts, nil
}

func addAsyncContractRoutes(async asyncModel, contracts *Contracts) error {
	for name, channel := range async.channels {
		contracts.SignalingChannels[name] = SignalingChannel{
			Address: channel.Address, Server: channel.Server, Query: channel.Query, HasQuery: channel.HasQuery,
		}
		if channel.Server == "" {
			contracts.Diagnostics = append(contracts.Diagnostics, Finding{
				Path:    "api/asyncapi.yaml",
				Line:    0,
				Rule:    "async-channel-server",
				Message: fmt.Sprintf("channel %s does not bind an explicit server", name),
			})
		} else if _, exists := async.servers[channel.Server]; !exists {
			contracts.Diagnostics = append(contracts.Diagnostics, Finding{
				Path:    "api/asyncapi.yaml",
				Line:    0,
				Rule:    "async-channel-server",
				Message: fmt.Sprintf("channel %s references missing server %s", name, channel.Server),
			})
		}
	}

	for name, server := range async.servers {
		contracts.SignalingServers[name] = server.Protocol + "://" + server.Host
	}

	for operationName, operation := range async.operations {
		if operation.Action != "send" && operation.Action != "receive" {
			continue
		}

		channel, exists := async.channels[operation.Channel]
		if !exists {
			return newRouteGateError("AsyncAPI send operation references missing channel %q", operation.Channel)
		}

		for _, messageName := range channel.Messages {
			message, exists := async.messages[messageName]
			if !exists {
				return newRouteGateError("AsyncAPI channel %q references missing message %q", operation.Channel, messageName)
			}

			schema, exists := async.schemas[message.Frame]
			if !exists {
				return newRouteGateError("AsyncAPI message %q references missing frame schema %q", messageName, message.Frame)
			}

			if schema.Method == "" {
				continue
			}

			route := SignalingRoute{
				OperationID: operationName,
				Channel:     operation.Channel,
				Address:     channel.Address,
				Server:      channel.Server,
				Frame:       message.Frame,
				Body:        schema.Body,
				Method:      schema.Method,
			}
			if operation.Action == "send" {
				contracts.SignalingRoutes[schema.Method] = append(contracts.SignalingRoutes[schema.Method], route)
			} else {
				contracts.SignalingInboundRoutes[schema.Method] = append(contracts.SignalingInboundRoutes[schema.Method], route)
			}
		}
	}

	return nil
}

func readOpenAPIServers(path string) (map[string]struct{}, error) {
	var rootDocument map[string]any

	err := readYAML(path, &rootDocument)
	if err != nil {
		return nil, err
	}

	servers := make(map[string]struct{})

	for _, value := range anySlice(rootDocument["servers"]) {
		serverURL := stringValue(stringMap(value)["url"])
		if serverURL != "" {
			servers[serverURL] = struct{}{}
		}
	}

	for _, pathItem := range stringMap(rootDocument["paths"]) {
		for _, operation := range stringMap(pathItem) {
			for _, value := range anySlice(stringMap(operation)["servers"]) {
				serverURL := stringValue(stringMap(value)["url"])
				if serverURL != "" {
					servers[serverURL] = struct{}{}
				}
			}
		}
	}

	if len(servers) == 0 {
		return nil, newRouteGateError("OpenAPI document %s contains no server URLs", path)
	}

	return servers, nil
}

func readModulePath(path string) (string, error) {
	raw, err := readContractFile(path)
	if err != nil {
		return "", err
	}

	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[0] == "module" {
			return strings.Trim(fields[1], "\""), nil
		}
	}

	err = scanner.Err()
	if err != nil {
		return "", wrapRouteGateError(err, "read module file")
	}

	return "", newRouteGateError("module directive not found in %s", path)
}

func readOpenAPIRoutes(path string) (map[string]HTTPRoute, error) {
	var rootDocument map[string]any

	err := readYAML(path, &rootDocument)
	if err != nil {
		return nil, err
	}

	paths := stringMap(rootDocument["paths"])
	routes := make(map[string]HTTPRoute)

	for routePath, pathValue := range paths {
		for method, operationValue := range stringMap(pathValue) {
			if !isHTTPMethod(method) {
				continue
			}

			operationID := stringValue(stringMap(operationValue)["operationId"])
			if operationID == "" {
				return nil, newRouteGateError("OpenAPI operation %s %s has no operationId", strings.ToUpper(method), routePath)
			}

			_, hasBody := stringMap(operationValue)["requestBody"]
			route := HTTPRoute{
				OperationID: operationID,
				Method:      strings.ToUpper(method),
				Path:        routePath,
				HasBody:     hasBody,
			}

			key := routeKey(route.Method, route.Path)
			if _, exists := routes[key]; exists {
				return nil, newRouteGateError("duplicate OpenAPI route %s", key)
			}

			routes[key] = route
		}
	}

	if len(routes) == 0 {
		return nil, newRouteGateError("OpenAPI document %s contains no operations", path)
	}

	return routes, nil
}

func discoverGeneratedHTTP(
	root, modulePath string,
	schemaRoutes map[string]HTTPRoute,
	contracts *Contracts,
) error {
	files, err := goFiles(root)
	if err != nil {
		return err
	}

	seenOperation := make(map[string]bool)

	for _, path := range files {
		raw, err := readContractFile(path)
		if err != nil {
			return err
		}

		if !isGeneratedGo(raw) {
			continue
		}

		fset := token.NewFileSet()

		file, err := parser.ParseFile(fset, path, raw, parser.ParseComments)
		if err != nil {
			return wrapRouteGateError(err, "parse generated Go file %s", path)
		}

		packagePath, err := importPath(root, modulePath, filepath.Dir(path))
		if err != nil {
			return err
		}

		if packagePath == modulePath+"/internal/generatedfcm" ||
			packagePath == modulePath+"/pkg/generatedfcm" {
			continue
		}

		collectGeneratedHTTPFunctions(root, path, file, fset, packagePath, schemaRoutes, contracts, seenOperation)

		if len(contracts.GeneratedHTTP[packagePath]) > 0 {
			contracts.GeneratedHTTPPaths[packagePath] = struct{}{}
		}
	}

	for _, route := range schemaRoutes {
		if !seenOperation[route.OperationID] {
			contracts.Diagnostics = append(contracts.Diagnostics, Finding{
				Path: "api/openapi.yaml",
				Line: 0,
				Rule: "generated-http-operation",
				Message: fmt.Sprintf(
					"operation %s %s (%s) has no matching generated request method",
					route.Method, route.Path, route.OperationID,
				),
			})
		}
	}

	return nil
}

func collectGeneratedHTTPFunctions(
	root, path string,
	file *ast.File,
	fset *token.FileSet,
	packagePath string,
	schemaRoutes map[string]HTTPRoute,
	contracts *Contracts,
	seenOperation map[string]bool,
) {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}

		route, matched := resolveGeneratedFunctionRoute(root, path, function, fset, schemaRoutes, contracts)
		if !matched {
			continue
		}

		if function.Recv == nil {
			if contracts.GeneratedHTTPRequests[packagePath] == nil {
				contracts.GeneratedHTTPRequests[packagePath] = make(map[string]HTTPRoute)
			}

			contracts.GeneratedHTTPRequests[packagePath][function.Name.Name] = route
		} else {
			if contracts.GeneratedHTTP[packagePath] == nil {
				contracts.GeneratedHTTP[packagePath] = make(map[string]HTTPRoute)
			}

			contracts.GeneratedHTTP[packagePath][function.Name.Name] = route
			seenOperation[route.OperationID] = true
		}
	}
}

func resolveGeneratedFunctionRoute(
	root, path string,
	function *ast.FuncDecl,
	fset *token.FileSet,
	schemaRoutes map[string]HTTPRoute,
	contracts *Contracts,
) (HTTPRoute, bool) {
	if function.Doc != nil {
		match := generatedRoutePattern.FindStringSubmatch(function.Doc.Text())
		if len(match) == generatedRouteMatchParts {
			route := HTTPRoute{
				OperationID: lowerFirst(match[3]),
				Method:      match[1],
				Path:        match[2],
				HasBody:     false,
			}
			key := routeKey(route.Method, route.Path)

			schemaRoute, exists := schemaRoutes[key]
			if !exists || schemaRoute.OperationID != route.OperationID {
				contracts.Diagnostics = append(contracts.Diagnostics, Finding{
					Path: relativePath(root, path),
					Line: fset.Position(function.Pos()).Line,
					Rule: "generated-http-contract",
					Message: fmt.Sprintf(
						"generated operation %s %s (%s) does not match api/openapi.yaml",
						route.Method, route.Path, route.OperationID,
					),
				})

				return route, false
			}

			return route, true
		}
	}

	if function.Recv == nil {
		operationID := generatedRequestOperationID(function.Name.Name)
		for _, candidate := range schemaRoutes {
			if candidate.OperationID == operationID {
				return candidate, true
			}
		}
	}

	var missing HTTPRoute

	return missing, false
}

func generatedRequestOperationID(name string) string {
	if !strings.HasPrefix(name, "New") {
		return ""
	}

	name = strings.TrimPrefix(name, "New")
	name = strings.TrimSuffix(name, "WithBody")

	name = strings.TrimSuffix(name, "Request")

	if name == "" {
		return ""
	}

	return lowerFirst(name)
}

func discoverGeneratedFrames(root, modulePath string, async asyncModel, contracts *Contracts) error {
	declaredTypes, err := declaredGeneratedTypes(root, modulePath)
	if err != nil {
		return err
	}

	for typeName := range referencedFrameNames(async) {
		bindGeneratedFrame(typeName, modulePath, declaredTypes, contracts)
	}

	return nil
}

func referencedFrameNames(async asyncModel) map[string]bool {
	frameNames := make(map[string]bool)

	for _, channel := range async.channels {
		for _, messageName := range channel.Messages {
			message, exists := async.messages[messageName]
			if !exists {
				continue
			}

			frameNames[message.Frame] = true

			schema, found := async.schemas[message.Frame]
			if found && schema.Body != "" {
				frameNames[schema.Body] = true
			}
		}
	}

	return frameNames
}

func declaredGeneratedTypes(root, modulePath string) (map[string]map[string]string, error) {
	files, err := goFiles(root)
	if err != nil {
		return nil, err
	}

	declaredTypes := make(map[string]map[string]string)

	for _, path := range files {
		raw, err := readContractFile(path)
		if err != nil {
			return nil, err
		}

		fset := token.NewFileSet()

		file, err := parser.ParseFile(fset, path, raw, 0)
		if err != nil {
			return nil, wrapRouteGateError(err, "parse Go file %s", path)
		}

		packagePath, err := importPath(root, modulePath, filepath.Dir(path))
		if err != nil {
			return nil, err
		}

		collectDeclaredTypes(file, packagePath, declaredTypes)
	}

	return declaredTypes, nil
}

func collectDeclaredTypes(file *ast.File, packagePath string, declaredTypes map[string]map[string]string) {
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}

		for _, spec := range general.Specs {
			value, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}

			if declaredTypes[value.Name.Name] == nil {
				declaredTypes[value.Name.Name] = make(map[string]string)
			}

			declaredTypes[value.Name.Name][packagePath] = value.Name.Name
		}
	}
}

func matchingDeclaredTypes(typeName string, declaredTypes map[string]map[string]string) ([]string, string) {
	var (
		matches []string
		goName  string
	)

	for declaredName, packages := range declaredTypes {
		if !strings.EqualFold(declaredName, typeName) {
			continue
		}

		goName = declaredName

		for packagePath := range packages {
			matches = append(matches, packagePath)
		}
	}

	sort.Strings(matches)

	return matches, goName
}

func bindGeneratedFrame(
	typeName, modulePath string,
	declaredTypes map[string]map[string]string,
	contracts *Contracts,
) {
	matches, goName := matchingDeclaredTypes(typeName, declaredTypes)
	canonicalPackage := modulePath + "/pkg/dependencymodels/signaling"

	if _, exists := declaredTypes[goName][canonicalPackage]; exists {
		contracts.GeneratedFrames[typeName] = canonicalPackage
		contracts.GeneratedFramePaths[canonicalPackage] = struct{}{}
		contracts.GeneratedGoNames[typeName] = goName

		return
	}

	if len(matches) == 1 {
		contracts.GeneratedFrames[typeName] = matches[0]
		contracts.GeneratedFramePaths[matches[0]] = struct{}{}
		contracts.GeneratedGoNames[typeName] = goName

		return
	}

	if len(matches) == 0 {
		contracts.Diagnostics = append(contracts.Diagnostics, Finding{
			Path:    "api/asyncapi.yaml",
			Line:    0,
			Rule:    "generated-signaling-model",
			Message: fmt.Sprintf("AsyncAPI frame or body %s has no matching Go model type", typeName),
		})

		return
	}

	contracts.Diagnostics = append(contracts.Diagnostics, Finding{
		Path: "api/asyncapi.yaml",
		Line: 0,
		Rule: "ambiguous-generated-signaling-model",
		Message: fmt.Sprintf(
			"AsyncAPI frame or body %s matches Go types in multiple packages: %s",
			typeName, strings.Join(matches, ", "),
		),
	})
}

func goFiles(root string) ([]string, error) {
	var files []string

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "vendor" || name == "node_modules" || name == "dist" {
				return filepath.SkipDir
			}

			if path != root && (pathWithin(path, filepath.Join(root, "tools", "routegate")) ||
				pathWithin(path, filepath.Join(root, "internal", "testkit"))) {
				return filepath.SkipDir
			}

			return nil
		}

		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}

		return nil
	})
	if err != nil {
		return nil, wrapRouteGateError(err, "walk Go sources")
	}

	sort.Strings(files)

	return files, nil
}

func importPath(root, modulePath, directory string) (string, error) {
	relative, err := filepath.Rel(root, directory)
	if err != nil {
		return "", wrapRouteGateError(err, "make package path for %s", directory)
	}

	if relative == "." {
		return modulePath, nil
	}

	return modulePath + "/" + filepath.ToSlash(relative), nil
}

func readYAML(path string, destination any) error {
	data, err := readContractFile(path)
	if err != nil {
		return err
	}

	err = yaml.Unmarshal(data, destination)
	if err != nil {
		return wrapRouteGateError(err, "parse %s", path)
	}

	return nil
}

func readContractFile(path string) ([]byte, error) {
	// #nosec G304 -- The route gate reads schema and source paths under the repository root supplied by its caller.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, wrapRouteGateError(err, "read %s", path)
	}

	return data, nil
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}

	return ""
}

func refName(value string) string {
	if value == "" {
		return ""
	}

	parts := strings.Split(value, "/")

	return parts[len(parts)-1]
}

func lowerFirst(value string) string {
	if value == "" {
		return value
	}

	runes := []rune(value)
	runes[0] = []rune(strings.ToLower(string(runes[0])))[0]

	return string(runes)
}

func isHTTPMethod(method string) bool {
	switch strings.ToLower(method) {
	case "get", "put", "post", "delete", "options", "head", "patch", "trace":
		return true
	default:
		return false
	}
}
