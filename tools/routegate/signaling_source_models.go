package routegate

import (
	"go/ast"
	"reflect"
	"strings"
)

const handwrittenSignalingKeyRule = "handwritten-signaling-wire-key"

func signalingSourcePackage(path string) bool {
	path = "/" + strings.ReplaceAll(path, "\\", "/")

	return strings.Contains(path, "/pkg/dependencies/websocket/") ||
		strings.Contains(path, "/internal/signaling/")
}

func auditSignalingSourceModels(root string, files []*parsedGoFile) []Finding {
	var findings []Finding

	for _, file := range files {
		if !signalingSourcePackage(file.path) {
			continue
		}

		ast.Inspect(file.file, func(node ast.Node) bool {
			rule := signalingSourceModelRule(node)
			if rule != "" {
				findings = append(findings, Finding{
					Path: relativePath(root, file.path), Line: file.fileSet.Position(node.Pos()).Line,
					Rule: rule, Message: "signaling wire definitions and library-defined keys must be schema generated",
				})
			}

			return true
		})
	}

	return findings
}

func signalingSourceModelRule(node ast.Node) string {
	value, ok := node.(*ast.StructType)
	if !ok {
		return ""
	}

	for _, field := range value.Fields.List {
		if field.Tag == nil {
			continue
		}

		tag, ok := stringConstant(field.Tag)
		if !ok {
			continue
		}

		name, tagged := reflect.StructTag(tag).Lookup("json")

		name, _, _ = strings.Cut(name, ",")
		if tagged && name != "-" {
			return "handwritten-signaling-wire-model"
		}
	}

	return ""
}
