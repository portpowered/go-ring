package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func (v *validator) loadReferencedDocuments() {
	seen := make(map[string]bool)

	for {
		var pending []*document

		for key, doc := range v.docs {
			if !seen[key] {
				pending = append(pending, doc)
			}
		}

		if len(pending) == 0 {
			return
		}

		sort.Slice(pending, func(i, j int) bool { return pending[i].path < pending[j].path })

		for _, doc := range pending {
			key := canonicalPath(doc.path)
			seen[key] = true

			v.visitReferences(doc, doc.data, "#")
		}
	}
}

func (v *validator) visitReferences(doc *document, value any, pointer string) {
	switch current := value.(type) {
	case map[string]any:
		v.visitObjectReferences(doc, current, pointer)
	case []any:
		v.visitArrayReferences(doc, current, pointer)
	}
}

func (v *validator) visitObjectReferences(doc *document, object map[string]any, pointer string) {
	if ref, ok := object["$ref"].(string); ok {
		_, _, _, err := v.resolveReference(doc, ref)
		if err != nil {
			v.addIssue(doc.path, pointer, "unresolved reference")
		}
	}

	if isSchemaObject(object) {
		v.visitSchemaObjectReferences(doc, object, pointer)

		return
	}

	if isOpenAPIExampleObject(object) {
		return
	}

	for key, child := range object {
		v.visitObjectChildReferences(doc, key, child, joinPointer(pointer, key))
	}
}

func (v *validator) visitSchemaObjectReferences(doc *document, object map[string]any, pointer string) {
	for key, child := range object {
		if !isSchemaChildKey(key) {
			continue
		}

		v.visitSchemaReferences(doc, key, child, joinPointer(pointer, key))
	}
}

func (v *validator) visitObjectChildReferences(doc *document, key string, child any, pointer string) {
	switch key {
	case exampleKey, valueKey, "default", constKey, enumKey:
		return
	case examplesKey:
		v.visitExampleReferences(doc, child, pointer)
	case "schemas":
		if schemas, ok := asMap(child); ok {
			for name, schema := range schemas {
				v.visitReferences(doc, schema, joinPointer(pointer, name))
			}

			return
		}
	}

	v.visitReferences(doc, child, pointer)
}

func (v *validator) visitArrayReferences(doc *document, values []any, pointer string) {
	for index, child := range values {
		v.visitReferences(doc, child, joinPointer(pointer, strconv.Itoa(index)))
	}
}

func (v *validator) visitSchemaReferences(doc *document, key string, value any, pointer string) {
	switch child := value.(type) {
	case map[string]any:
		if isSchemaMapContainer(key) {
			for name, schema := range child {
				v.visitReferences(doc, schema, joinPointer(pointer, name))
			}

			return
		}

		if key == dependenciesKey {
			for name, schema := range child {
				if _, ok := asMap(schema); ok {
					v.visitReferences(doc, schema, joinPointer(pointer, name))
				}
			}

			return
		}

		v.visitReferences(doc, child, pointer)
	case []any:
		for index, schema := range child {
			v.visitReferences(doc, schema, joinPointer(pointer, strconv.Itoa(index)))
		}
	}
}

func (v *validator) visitExampleReferences(doc *document, examples any, pointer string) {
	visit := func(value any, valuePointer string) {
		if example, ok := asMap(value); ok {
			if ref, ok := example["$ref"].(string); ok {
				_, _, _, err := v.resolveReference(doc, ref)
				if err != nil {
					v.addIssue(doc.path, valuePointer, "unresolved example reference")
				}
			}
		}
	}

	switch values := examples.(type) {
	case map[string]any:
		for name, value := range values {
			visit(value, joinPointer(pointer, name))
		}
	case []any:
		for index, value := range values {
			visit(value, joinPointer(pointer, strconv.Itoa(index)))
		}
	}
}

func (v *validator) buildCompiler() {
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	compiler.AssertContent()
	compiler.UseLoader(rejectRemoteLoader{})

	keys := make([]string, 0, len(v.docs))
	for key := range v.docs {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	for _, key := range keys {
		doc := v.docs[key]

		resource := doc.data
		if scope := v.groupScopes[key]; scope != "" {
			if root, ok := asMap(resource); ok {
				resourceCopy := make(map[string]any, len(root))
				maps.Copy(resourceCopy, root)
				resourceCopy["components"] = v.groupComponents[scope]
				resource = resourceCopy
			}
		}

		doc.resource = normalizeSchemaResource(resource)

		err := compiler.AddResource(doc.uri, doc.resource)
		if err != nil {
			v.addIssue(doc.path, "#", "cannot register schema resource")
		}
	}

	v.compiler = compiler
}

type rejectRemoteLoader struct{}

func (rejectRemoteLoader) Load(string) (any, error) {
	return nil, errRemoteLoadingDisabled
}

func normalizeSchemaResource(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}

	var clone any

	err = json.Unmarshal(encoded, &clone)
	if err != nil {
		return value
	}

	normalizeSchemaKeywords(clone)

	return clone
}

func normalizeSchemaKeywords(value any) {
	switch current := value.(type) {
	case map[string]any:
		normalizeSchemaMap(current)
	case []any:
		for _, child := range current {
			normalizeSchemaKeywords(child)
		}
	}
}

func normalizeSchemaMap(schema map[string]any) {
	normalizeNullable(schema)
	normalizeExclusiveBound(schema, "exclusiveMinimum", "minimum")
	normalizeExclusiveBound(schema, "exclusiveMaximum", "maximum")

	for _, child := range schema {
		normalizeSchemaKeywords(child)
	}
}

func normalizeNullable(schema map[string]any) {
	if schema["nullable"] != true {
		return
	}

	schemaType, ok := schema["type"].(string)
	if ok && schemaType != "null" {
		schema["type"] = []any{schemaType, "null"}
	}
}

func normalizeExclusiveBound(schema map[string]any, exclusiveKeyword, boundaryKeyword string) {
	exclusive, ok := schema[exclusiveKeyword].(bool)
	if !ok {
		return
	}

	boundary, hasBoundary := schema[boundaryKeyword]
	if exclusive && hasBoundary {
		schema[exclusiveKeyword] = boundary
		delete(schema, boundaryKeyword)

		return
	}

	delete(schema, exclusiveKeyword)
}

func (v *validator) resolveReference(from *document, reference string) (*document, string, any, error) {
	parsed, err := url.Parse(reference)
	if err != nil {
		return nil, "", nil, wrapError("parse local reference", err)
	}

	if !isLocalReference(parsed) {
		return nil, "", nil, errReferenceNotLocal
	}

	base, err := url.Parse(from.uri)
	if err != nil {
		return nil, "", nil, wrapError("parse source document URI", err)
	}

	target := base.ResolveReference(parsed)
	if !isLocalFileURL(target) {
		return nil, "", nil, errReferenceNotLocal
	}

	path := localFilePath(target.Path)

	doc, err := v.loadDocument(path)
	if err != nil {
		return nil, "", nil, wrapError("load referenced schema document", err)
	}

	pointer := "#"
	if target.Fragment != "" {
		pointer += target.Fragment
	}

	resolved, ok := resolvePointer(doc.data, pointer)
	if !ok {
		if parsed.Path == "" {
			return v.resolveGroupedReference(from, pointer)
		}

		return nil, "", nil, errReferenceFragment
	}

	return doc, pointer, resolved, nil
}

func isLocalReference(reference *url.URL) bool {
	return reference.RawQuery == "" && (reference.Scheme == "" || reference.Scheme == fileScheme)
}

func isLocalFileURL(target *url.URL) bool {
	return target.Scheme == fileScheme && target.Host == ""
}

func localFilePath(path string) string {
	if runtime.GOOS == windowsOS {
		path = strings.TrimPrefix(path, "/")
	}

	return filepath.Clean(filepath.FromSlash(path))
}

func (v *validator) resolveGroupedReference(from *document, pointer string) (*document, string, any, error) {
	ownerDoc, ownerPointer, owner, found := v.resolveGroupedPointer(from, pointer)
	if found {
		return ownerDoc, ownerPointer, owner, nil
	}

	return nil, "", nil, errReferenceFragment
}

func (v *validator) resolveGroupedPointer(from *document, pointer string) (*document, string, any, bool) {
	scope := v.groupScopes[canonicalPath(from.path)]
	if scope == "" || !strings.HasPrefix(pointer, "#/components/") {
		return nil, "", nil, false
	}

	var (
		matchDoc *document
		match    any
	)

	for key, candidate := range v.docs {
		if v.groupScopes[key] != scope {
			continue
		}

		value, ok := resolvePointer(candidate.data, pointer)
		if !ok {
			continue
		}

		if matchDoc != nil {
			return nil, "", nil, false
		}

		matchDoc, match = candidate, value
	}

	if matchDoc == nil {
		return nil, "", nil, false
	}

	return matchDoc, pointer, match, true
}

func resolvePointer(root any, pointer string) (any, bool) {
	if pointer == "" || pointer == "#" {
		return root, true
	}

	fragment := strings.TrimPrefix(pointer, "#")
	if !strings.HasPrefix(fragment, "/") {
		return findAnchor(root, fragment, "#")
	}

	return resolvePointerTokens(root, strings.Split(strings.TrimPrefix(fragment, "/"), "/"))
}

func resolvePointerTokens(root any, tokens []string) (any, bool) {
	current := root

	for _, token := range tokens {
		part := strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")

		switch node := current.(type) {
		case map[string]any:
			child, ok := node[part]
			if !ok {
				return nil, false
			}

			current = child
		case []any:
			child, ok := resolveArrayPointer(node, part)
			if !ok {
				return nil, false
			}

			current = child
		default:
			return nil, false
		}
	}

	return current, true
}

func resolveArrayPointer(values []any, part string) (any, bool) {
	index := -1

	_, err := fmt.Sscanf(part, "%d", &index)
	if err != nil || index < 0 || index >= len(values) {
		return nil, false
	}

	return values[index], true
}

func findAnchor(root any, anchor, pointer string) (any, bool) {
	switch node := root.(type) {
	case map[string]any:
		if node["$anchor"] == anchor {
			return node, true
		}

		for key, child := range node {
			if result, ok := findAnchor(child, anchor, joinPointer(pointer, key)); ok {
				return result, true
			}
		}
	case []any:
		for index, child := range node {
			if result, ok := findAnchor(child, anchor, joinPointer(pointer, strconv.Itoa(index))); ok {
				return result, true
			}
		}
	}

	return nil, false
}
