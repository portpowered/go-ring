package routegate

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
)

type signalingFramePair struct {
	frame string
	body  string
}

func auditSignalingAdapters(root string, contracts Contracts) ([]Finding, bool, error) {
	wirePath := filepath.Join(root, "pkg", "dependencies", "websocket", "wire.go")
	writerPath := filepath.Join(root, "pkg", "dependencies", "websocket", "signaling.go")

	wire, err := parseRouteGateFile(wirePath)
	if err != nil {
		return nil, false, err
	}

	writer, err := parseRouteGateFile(writerPath)
	if err != nil {
		return nil, false, err
	}

	var findings []Finding

	add := func(file *parsedGoFile, node ast.Node, rule, message string) {
		line := 0
		if node != nil {
			line = file.fileSet.Position(node.Pos()).Line
		}

		findings = append(findings, Finding{
			Path:    filepath.ToSlash(relativePath(root, file.path)),
			Line:    line,
			Rule:    rule,
			Message: message,
		})
	}

	methods := protocolStringConstants(root)
	wireImports := importAliases(wire.file)
	wireFunctions := namedFunctions(wire.file)
	writerFunctions := namedFunctions(writer.file)
	outboundFunction := wireFunctions["marshalSignalingFrame"]
	inboundFunction := wireFunctions["validateInboundFrame"]
	decodeFunction := wireFunctions["unmarshalSignalingFrame"]
	writeFunction := writerFunctions["WriteSignaling"]

	if outboundFunction == nil {
		add(wire, nil, "missing-outbound-signaling-adapter", "generated signaling wire encoder is missing")
	}

	frameMarshaller := wireFunctions["marshalGeneratedFrame"]

	if !generatedFrameMarshallerSafe(frameMarshaller, wireImports) {
		add(
			wire,
			frameMarshaller,
			"signaling-adapter-contract",
			"generated frame marshaller must decode the schema-bound body and marshal only its generated frame",
		)
	}

	if inboundFunction == nil || decodeFunction == nil {
		add(
			wire,
			nil,
			"missing-inbound-signaling-adapter",
			"generated signaling discriminator and frame decoder are required",
		)
	}

	if writeFunction == nil || !writeUsesGeneratedEncoder(writeFunction) {
		add(
			writer,
			writeFunction,
			"handwritten-signaling-envelope",
			"WriteSignaling must marshal only the verified generated-frame encoder output",
		)
	}

	if outboundFunction != nil {
		adapterFindings := checkOutboundAdapter(
			wire,
			outboundFunction,
			wireFunctions,
			wireImports,
			methods,
			contracts,
		)
		findings = append(findings, adapterFindings...)
	}

	if inboundFunction != nil && decodeFunction != nil {
		adapterFindings := checkInboundAdapter(
			wire,
			decodeFunction,
			inboundFunction,
			wireImports,
			methods,
			contracts,
		)
		findings = append(findings, adapterFindings...)
	}

	return findings, len(findings) == 0, nil
}

func parseRouteGateFile(path string) (*parsedGoFile, error) {
	raw, err := fs.ReadFile(os.DirFS(filepath.Dir(path)), filepath.Base(path))
	if err != nil {
		return nil, wrapRouteGateError(err, "read signaling adapter source %s", path)
	}

	fileSet := token.NewFileSet()

	file, err := parser.ParseFile(fileSet, path, raw, parser.ParseComments)
	if err != nil {
		return nil, wrapRouteGateError(err, "parse signaling adapter source %s", path)
	}

	return &parsedGoFile{path: path, fileSet: fileSet, file: file}, nil
}

func namedFunctions(file *ast.File) map[string]*ast.FuncDecl {
	functions := make(map[string]*ast.FuncDecl)

	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Body != nil {
			functions[function.Name.Name] = function
		}
	}

	return functions
}

func checkOutboundAdapter(
	file *parsedGoFile,
	function *ast.FuncDecl,
	functions map[string]*ast.FuncDecl,
	imports map[string]string,
	protocolValues map[string]string,
	contracts Contracts,
) []Finding {
	var findings []Finding

	add := func(node ast.Node, message string) {
		if node == nil {
			node = function
		}

		findings = append(findings, Finding{
			Path: filepath.ToSlash(filepath.Join(
				"pkg", "dependencies", "websocket", "wire.go",
			)),
			Line:    file.fileSet.Position(node.Pos()).Line,
			Rule:    "signaling-adapter-contract",
			Message: message,
		})
	}
	if !singleTopLevelMethodSwitch(function) {
		add(function, "outbound encoder must dispatch directly on one exhaustive method switch")
	}

	cases, defaultClause := protocolMethodCases(function, imports, protocolValues)

	expected := expectedFramePairs(contracts.SignalingRoutes)
	for method := range expected {
		if _, exists := cases[method]; !exists {
			add(function, fmt.Sprintf("outbound encoder has no method branch for schema method %q", method))
		}
	}

	for method, clause := range cases {
		if _, exists := expected[method]; !exists {
			add(clause, fmt.Sprintf("outbound encoder accepts method %q absent from AsyncAPI send operations", method))
		}
	}

	if defaultClause == nil || !clauseReturnsError(defaultClause) {
		add(function, "outbound encoder must reject unknown and unsupported methods in its default branch")
	}

	actual := make(map[string]map[signalingFramePair]bool)
	for method, clause := range cases {
		actual[method] = collectFramePairs(
			clause,
			method,
			functions,
			imports,
			protocolValues,
			contracts,
			make(map[string]bool),
		)
	}

	compareFramePairs(expected, actual, add)

	if !generatedImportBound(imports, contracts) {
		add(function, "generated signaling types do not resolve to the discovered AsyncAPI model package")
	}

	if !protocolImportBound(imports) {
		add(function, "signaling method selectors do not resolve to the internal/protocol import")
	}

	return findings
}

func checkInboundAdapter(
	file *parsedGoFile,
	decode, validate *ast.FuncDecl,
	imports map[string]string,
	protocolValues map[string]string,
	contracts Contracts,
) []Finding {
	var findings []Finding

	add := func(node ast.Node, message string) {
		if node == nil {
			node = validate
		}

		findings = append(findings, Finding{
			Path:    filepath.ToSlash(filepath.Join("pkg", "dependencies", "websocket", "wire.go")),
			Line:    file.fileSet.Position(node.Pos()).Line,
			Rule:    "signaling-adapter-contract",
			Message: message,
		})
	}
	if !singleTopLevelMethodSwitch(validate) {
		add(validate, "inbound decoder must dispatch directly on one exhaustive method switch")
	}

	if !usesGeneratedDiscriminator(decode, imports, contracts) {
		add(
			decode,
			"inbound decoder must decode the generated AsyncAPI discriminator and compare its raw method string before dispatch",
		)
	}

	if !decodeValidatesBeforeAdaptation(decode) {
		add(
			decode,
			"inbound decoder must validate a typed generated frame before adapting it to internal/signaling.Message",
		)
	}

	cases, defaultClause := protocolMethodCases(validate, imports, protocolValues)

	expected := expectedFramePairs(contracts.SignalingInboundRoutes)
	for method := range expected {
		if _, exists := cases[method]; !exists {
			add(validate, fmt.Sprintf("inbound decoder has no method branch for schema method %q", method))
		}
	}

	for method, clause := range cases {
		if _, exists := expected[method]; !exists {
			add(clause, fmt.Sprintf("inbound decoder accepts method %q absent from AsyncAPI receive operations", method))
		}
	}

	if defaultClause == nil || !clauseReturnsError(defaultClause) {
		add(validate, "inbound decoder must reject unknown methods in its default branch")
	}

	actual := make(map[string]map[signalingFramePair]bool)
	for method, clause := range cases {
		actual[method] = collectInboundFramePairs(clause, imports, contracts)
	}

	compareInboundFramePairs(expected, actual, add)

	if !generatedImportBound(imports, contracts) {
		add(validate, "inbound generated frame types do not resolve to the discovered AsyncAPI model package")
	}

	if !protocolImportBound(imports) {
		add(validate, "inbound method selectors do not resolve to the internal/protocol import")
	}

	return findings
}

func verifiedSignalWriteCall(call *ast.CallExpr, parents map[ast.Node]ast.Node, adapterVerified bool) bool {
	if !adapterVerified {
		return false
	}

	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != websocketWriteMessageName {
		return false
	}

	function := enclosingFunction(call, parents)

	return function != nil && function.Name.Name == "WriteSignaling"
}
