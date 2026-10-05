package routegate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

type parsedGoFile struct {
	path    string
	fileSet *token.FileSet
	file    *ast.File
}

// Audit loads the checked-in contracts, verifies generated HTTP operations,
// and rejects production network edges that are not tied to those contracts.
// The audit is intentionally conservative: a source pattern it cannot resolve
// statically is a finding, not an implicit exemption.
func Audit(root string) ([]Finding, error) {
	contracts, err := LoadContracts(root)
	if err != nil {
		return nil, err
	}

	findings := append([]Finding(nil), contracts.Diagnostics...)
	findings = append(findings, auditProtocolEndpoints(root, contracts)...)
	adapterVerified := false

	if len(contracts.SignalingRoutes) > 0 || len(contracts.SignalingInboundRoutes) > 0 {
		adapterFindings, valid, err := auditSignalingAdapters(root, contracts)
		if err != nil {
			return nil, err
		}

		findings = append(findings, adapterFindings...)
		adapterVerified = valid
	}

	files, err := goFiles(root)
	if err != nil {
		return nil, err
	}

	parsed := make([]*parsedGoFile, 0, len(files))
	modelSources := make([]*parsedGoFile, 0, len(files))
	byDirectory := make(map[string][]*parsedGoFile)

	for _, path := range files {
		raw, err := fs.ReadFile(os.DirFS(filepath.Dir(path)), filepath.Base(path))
		if err != nil {
			return nil, wrapRouteGateError(err, "read %s", path)
		}

		fileSet := token.NewFileSet()

		file, err := parser.ParseFile(fileSet, path, raw, parser.ParseComments)
		if err != nil {
			return nil, wrapRouteGateError(err, "parse Go file %s", path)
		}

		item := &parsedGoFile{path: path, fileSet: fileSet, file: file}
		modelSources = append(modelSources, item)

		if !isGeneratedGo(raw) || signalingSourcePackage(path) {
			parsed = append(parsed, item)
			byDirectory[filepath.Dir(path)] = append(byDirectory[filepath.Dir(path)], item)
		}
	}

	protocolValues := protocolStringConstants(root)
	findings = append(findings, auditSignalingSourceModels(root, parsed)...)
	findings = append(findings, auditWireModelSource(root, modelSources)...)

	primitiveFindings, err := auditSchemaPrimitiveProvenance(root, parsed, protocolScalarConstants(root))
	if err != nil {
		return nil, err
	}

	findings = append(findings, primitiveFindings...)

	verifiedFCMCalls, fcmFindings := auditExternalFCM(root, parsed)
	findings = append(findings, fcmFindings...)
	verifiedMCSCalls, mcsFindings := auditMCSSocket(root, parsed)
	findings = append(findings, mcsFindings...)

	if verifiedFCMCalls == nil && len(verifiedMCSCalls) > 0 {
		verifiedFCMCalls = make(map[*ast.CallExpr]bool, len(verifiedMCSCalls))
	}

	for call := range verifiedMCSCalls {
		verifiedFCMCalls[call] = true
	}

	for _, item := range parsed {
		packageFiles := byDirectory[filepath.Dir(item.path)]
		findings = append(
			findings,
			auditFile(
				root,
				item.path,
				item.fileSet,
				item.file,
				contracts,
				packageFiles,
				adapterVerified,
				verifiedFCMCalls,
				protocolValues,
			)...,
		)
	}

	findings = uniqueFindings(findings)
	sort.Slice(findings, func(left, right int) bool {
		if findings[left].Path != findings[right].Path {
			return findings[left].Path < findings[right].Path
		}

		if findings[left].Line != findings[right].Line {
			return findings[left].Line < findings[right].Line
		}

		if findings[left].Rule != findings[right].Rule {
			return findings[left].Rule < findings[right].Rule
		}

		return findings[left].Message < findings[right].Message
	})

	return findings, nil
}

func auditFile(
	root, path string,
	fileSet *token.FileSet,
	file *ast.File,
	contracts Contracts,
	packageFiles []*parsedGoFile,
	adapterVerified bool,
	verifiedFCMCalls map[*ast.CallExpr]bool,
	protocolValues map[string]string,
) []Finding {
	imports := importAliases(file)
	parents := parentNodes(file)

	verifiedDoCalls, requestFindings := auditGeneratedRequests(
		root,
		path,
		fileSet,
		file,
		imports,
		contracts,
		packageFiles,
	)
	for call := range verifiedHTTPTransportCalls(packageFiles) {
		verifiedDoCalls[call] = true
	}

	generatedClients, generatedCandidates := generatedClientObjects(file, imports, contracts)
	if isClientConsumerPath(relativePath(root, path)) {
		generatedClients = make(map[*ast.Object]string)
		generatedCandidates = make(map[*ast.Object]string)
	}

	operationNames := generatedOperationNames(contracts)
	findings := make([]Finding, 0, len(requestFindings))
	findings = append(findings, requestFindings...)

	add := func(node ast.Node, rule, message string) {
		if node == nil {
			return
		}

		findings = append(findings, Finding{
			Path:    relativePath(root, path),
			Line:    fileSet.Position(node.Pos()).Line,
			Rule:    rule,
			Message: message,
		})
	}

	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.CallExpr:
			selector, ok := value.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			verifiedSignalWriter := adapterVerified &&
				relativePath(root, path) == "pkg/dependencies/websocket/signaling.go"
			auditNetworkCall(
				value,
				selector,
				imports,
				verifiedDoCalls,
				verifiedFCMCalls,
				parents,
				packageFiles,
				verifiedSignalWriter,
				add,
			)
			auditGenericHTTPHelper(value, selector, parents, add)
			auditGeneratedOperationCall(
				value,
				selector,
				operationNames,
				generatedClients,
				generatedCandidates,
				imports,
				add,
			)
			auditGeneratedRequestCall(value, selector, contracts, imports, add)
			auditGeneratedConstructorCall(value, selector, contracts, imports, add)
			auditWebSocketHelperCall(value, selector, imports, contracts, parents, adapterVerified, add)

			if !adapterVerified {
				auditSignalingSend(value, selector, imports, contracts, protocolValues, add)
			}
		case *ast.SelectorExpr:
			if _, isCall := parents[value].(*ast.CallExpr); isCall {
				return true
			}

			auditPrimitiveMethodValue(value, imports, add)
			auditGeneratedOperationValue(
				value,
				operationNames,
				generatedClients,
				generatedCandidates,
				imports,
				add,
			)
			auditGeneratedRequestValue(value, contracts, imports, parents, add)
			auditWebSocketHelperValue(value, imports, contracts, adapterVerified, add)

			if !adapterVerified {
				auditSignalingMethodValue(value, imports, parents, add)
			}
		}

		return true
	})

	return findings
}
