package routegate

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"
)

const (
	generatedSignalingPackageName    = "generatedsignaling"
	jsonUnmarshalMethodName          = "Unmarshal"
	jsonPackageImportPath            = "encoding/json"
	validateTypedInboundFunctionName = "validateTypedInbound"
)

func generatedSelectorType(expression ast.Expr, imports map[string]string, contracts Contracts) string {
	selector, ok := unparen(expression).(*ast.SelectorExpr)
	if !ok {
		if pointer, pointerOK := unparen(expression).(*ast.StarExpr); pointerOK {
			selector, ok = unparen(pointer.X).(*ast.SelectorExpr)
		}
	}

	if !ok {
		return ""
	}

	packagePath, imported, shadowed := selectorImport(selector, imports)
	if !imported || shadowed || !hasGeneratedFramePackage(contracts, packagePath) {
		return ""
	}

	return selector.Sel.Name
}

func schemaFrameName(goName string, contracts Contracts) string {
	for schemaName, candidate := range contracts.GeneratedGoNames {
		if strings.EqualFold(candidate, goName) || strings.EqualFold(schemaName, goName) {
			return schemaName
		}
	}

	return goName
}

func collectInboundFramePairs(
	clause *ast.CaseClause,
	imports map[string]string,
	contracts Contracts,
) map[signalingFramePair]bool {
	pairs := make(map[signalingFramePair]bool)

	ast.Inspect(clause, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		frameName := inboundGenericType(call, imports, contracts)
		if frameName != "" {
			pairs[signalingFramePair{frame: schemaFrameName(frameName, contracts), body: ""}] = true
		}

		return true
	})

	return pairs
}

func inboundGenericType(call *ast.CallExpr, imports map[string]string, contracts Contracts) string {
	var typeExpression ast.Expr

	switch function := call.Fun.(type) {
	case *ast.IndexExpr:
		identifier, ok := function.X.(*ast.Ident)
		if !ok || identifier.Name != validateTypedInboundFunctionName {
			return ""
		}

		typeExpression = function.Index
	case *ast.IndexListExpr:
		identifier, ok := function.X.(*ast.Ident)
		if !ok || identifier.Name != validateTypedInboundFunctionName || len(function.Indices) != 1 {
			return ""
		}

		typeExpression = function.Indices[0]
	default:
		return ""
	}

	typeName := generatedSelectorType(typeExpression, imports, contracts)
	if typeName == "" || !strings.HasSuffix(typeName, "Frame") {
		return ""
	}

	return typeName
}

func compareFramePairs(
	expected, actual map[string]map[signalingFramePair]bool,
	add func(ast.Node, string),
) {
	for method, expectedPairs := range expected {
		actualPairs := actual[method]
		for pair := range expectedPairs {
			if !actualPairs[pair] {
				message := fmt.Sprintf(
					"method %q does not use generated frame/body pair %s/%s from AsyncAPI",
					method,
					pair.frame,
					pair.body,
				)
				add(nil, message)
			}
		}
	}

	for method, actualPairs := range actual {
		expectedPairs := expected[method]

		for pair := range actualPairs {
			if len(pair.body) == 0 {
				if !expectedFrameName(expectedPairs, pair.frame) {
					message := fmt.Sprintf(
						"method %q decodes generated frame %s absent from AsyncAPI receive operations",
						method,
						pair.frame,
					)
					add(nil, message)
				}

				continue
			}

			if !expectedPairs[pair] {
				message := fmt.Sprintf(
					"method %q uses generated frame/body pair %s/%s absent from AsyncAPI",
					method,
					pair.frame,
					pair.body,
				)
				add(nil, message)
			}
		}
	}
}

func compareInboundFramePairs(
	expected, actual map[string]map[signalingFramePair]bool,
	add func(ast.Node, string),
) {
	for method, expectedPairs := range expected {
		actualPairs := actual[method]
		for pair := range expectedPairs {
			if !expectedFrameName(actualPairs, pair.frame) {
				add(nil, fmt.Sprintf("inbound method %q does not validate generated frame %s from AsyncAPI", method, pair.frame))
			}
		}
	}

	for method, actualPairs := range actual {
		expectedPairs := expected[method]
		for pair := range actualPairs {
			if !expectedFrameName(expectedPairs, pair.frame) {
				add(nil, fmt.Sprintf("inbound method %q validates generated frame %s absent from AsyncAPI", method, pair.frame))
			}
		}
	}
}

func expectedFrameName(expected map[signalingFramePair]bool, name string) bool {
	for pair := range expected {
		if strings.EqualFold(pair.frame, name) {
			return true
		}
	}

	return false
}

func clauseReturnsError(clause *ast.CaseClause) bool {
	for _, statement := range clause.Body {
		returned, ok := statement.(*ast.ReturnStmt)
		if !ok || len(returned.Results) == 0 {
			continue
		}

		if expressionContainsError(returned.Results[len(returned.Results)-1]) {
			return true
		}
	}

	return false
}

func expressionContainsError(expression ast.Expr) bool {
	found := false

	ast.Inspect(expression, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		switch function := call.Fun.(type) {
		case *ast.Ident:
			found = function.Name == "Errorf" || function.Name == "New" ||
				function.Name == "signalingWireError" || function.Name == "wrapSignalingWireError"
		case *ast.SelectorExpr:
			found = function.Sel.Name == "Errorf" || function.Sel.Name == "New" ||
				function.Sel.Name == "NewBadRequestError"
		}

		return !found
	})

	return found
}

func usesGeneratedDiscriminator(function *ast.FuncDecl, imports map[string]string, contracts Contracts) bool {
	return declaresGeneratedDiscriminator(function, imports, contracts) &&
		decodesGeneratedDiscriminator(function, imports) &&
		comparesRawDiscriminator(function)
}

func declaresGeneratedDiscriminator(function *ast.FuncDecl, imports map[string]string, contracts Contracts) bool {
	for _, declaration := range function.Body.List {
		declaration, ok := declaration.(*ast.DeclStmt)
		if !ok {
			continue
		}

		general, ok := declaration.Decl.(*ast.GenDecl)
		if !ok {
			continue
		}

		if generatedDiscriminatorSpec(general, imports, contracts) {
			return true
		}
	}

	return false
}

func generatedDiscriminatorSpec(general *ast.GenDecl, imports map[string]string, contracts Contracts) bool {
	for _, spec := range general.Specs {
		value, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}

		selector, ok := value.Type.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "SignalingInboundDiscriminator" {
			continue
		}

		if !hasDiscriminatorName(value.Names) {
			continue
		}

		packagePath, imported, shadowed := selectorImport(selector, imports)
		if imported && !shadowed && hasGeneratedFramePackage(contracts, packagePath) {
			return true
		}
	}

	return false
}

func hasDiscriminatorName(names []*ast.Ident) bool {
	for _, name := range names {
		if name.Name == "discriminator" {
			return true
		}
	}

	return false
}

func decodesGeneratedDiscriminator(function *ast.FuncDecl, imports map[string]string) bool {
	found := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !isGeneratedDiscriminatorUnmarshal(call, imports) {
			return true
		}

		found = true

		return false
	})

	return found
}

func isGeneratedDiscriminatorUnmarshal(call *ast.CallExpr, imports map[string]string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != jsonUnmarshalMethodName || len(call.Args) != 2 {
		return false
	}

	identifier, ok := call.Args[0].(*ast.Ident)
	if !ok || identifier.Name != "encoded" || !destinationIs(call.Args[1], "discriminator") {
		return false
	}

	packagePath, imported, shadowed := selectorImport(selector, imports)

	return imported && !shadowed && packagePath == jsonPackageImportPath
}

func comparesRawDiscriminator(function *ast.FuncDecl) bool {
	found := false

	ast.Inspect(function.Body, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "Value" {
			found = true

			return false
		}

		return true
	})

	return found
}

func decodeValidatesBeforeAdaptation(function *ast.FuncDecl) bool {
	validates := false
	adapted := false

	for _, call := range functionCalls(function) {
		identifier, ok := call.Fun.(*ast.Ident)
		if ok && identifier.Name == "validateInboundFrame" {
			validates = true
		}
	}

	ast.Inspect(function.Body, func(node ast.Node) bool {
		composite, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}

		identifier, ok := composite.Type.(*ast.SelectorExpr)
		if ok && isInternalSignalingMessage(identifier) {
			adapted = true
		}

		return true
	})

	return validates && adapted
}

func isInternalSignalingMessage(selector *ast.SelectorExpr) bool {
	if selector.Sel.Name != "Message" {
		return false
	}

	root := rootIdentifier(selector.X)

	return root != nil && root.Name == "signaling"
}

func destinationIs(expression ast.Expr, name string) bool {
	address, ok := expression.(*ast.UnaryExpr)
	if !ok || address.Op != token.AND {
		return false
	}

	identifier, ok := address.X.(*ast.Ident)

	return ok && identifier.Name == name
}

func generatedImportBound(imports map[string]string, contracts Contracts) bool {
	for alias, path := range imports {
		if alias == generatedSignalingPackageName || filepath.Base(path) == generatedSignalingPackageName {
			return hasGeneratedFramePackage(contracts, path)
		}
	}

	return false
}

func hasGeneratedFramePackage(contracts Contracts, packagePath string) bool {
	for _, generatedPath := range contracts.GeneratedFrames {
		if generatedPath == packagePath {
			return true
		}
	}

	return false
}

func protocolImportBound(imports map[string]string) bool {
	return hasImportPath(imports, protocolImportPath())
}

func writeUsesGeneratedEncoder(function *ast.FuncDecl) bool {
	if function == nil {
		return false
	}

	encodedName, encoder := generatedEncoderOutput(function)
	write, writeCount := signalingFrameWrite(function)

	if encoder == nil || write == nil || writeCount != 1 || encodedName == "" || encoder.Pos() >= write.Pos() {
		return false
	}

	if !writeUsesEncoderOutput(write, encodedName) {
		return false
	}

	return !marshalsInternalMessage(function)
}

func generatedEncoderOutput(function *ast.FuncDecl) (string, *ast.CallExpr) {
	parents := parentNodes(function)

	var (
		encodedName string
		encoder     *ast.CallExpr
	)

	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !isGeneratedSignalingEncoderCall(call) {
			return true
		}

		encoder = call
		encodedName = encoderAssignmentName(call, parents)

		return true
	})

	return encodedName, encoder
}

func isGeneratedSignalingEncoderCall(call *ast.CallExpr) bool {
	identifier, ok := call.Fun.(*ast.Ident)
	if !ok || identifier.Name != "marshalSignalingFrame" || len(call.Args) != 1 {
		return false
	}

	message, ok := call.Args[0].(*ast.Ident)

	return ok && message.Name == "message"
}

func encoderAssignmentName(call *ast.CallExpr, parents map[ast.Node]ast.Node) string {
	assignment, ok := parents[call].(*ast.AssignStmt)
	if !ok || len(assignment.Lhs) == 0 {
		return ""
	}

	name, ok := assignment.Lhs[0].(*ast.Ident)
	if !ok {
		return ""
	}

	return name.Name
}

func signalingFrameWrite(function *ast.FuncDecl) (*ast.CallExpr, int) {
	var write *ast.CallExpr

	writeCount := 0

	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && isWriteMessageCall(call) {
			write = call
			writeCount++
		}

		return true
	})

	return write, writeCount
}

func isWriteMessageCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)

	return ok && selector.Sel.Name == "WriteMessage" && len(call.Args) >= 2
}

func writeUsesEncoderOutput(write *ast.CallExpr, encodedName string) bool {
	if len(write.Args) < 2 {
		return false
	}

	encoded, ok := write.Args[1].(*ast.Ident)

	return ok && encoded.Name == encodedName
}

func marshalsInternalMessage(function *ast.FuncDecl) bool {
	found := false

	for _, call := range functionCalls(function) {
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "Marshal" && len(call.Args) == 1 {
			if identifier, ok := call.Args[0].(*ast.Ident); ok && identifier.Name == "message" {
				found = true

				break
			}
		}
	}

	return found
}
