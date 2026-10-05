package main

import (
	"strconv"
	"strings"
)

func (v *validator) validateOpenAPIExamplesAndGroups() {
	docs := v.sortedDocuments()
	for _, doc := range docs {
		root, ok := asMap(doc.data)
		if !ok {
			continue
		}

		_, isOpenAPI := root["openapi"]
		if isOpenAPI || hasMediaComponents(root) {
			v.walkOpenAPIMedia(doc, doc.data, "#")
		}

		if isOpenAPI {
			v.collectOpenAPIGroups(doc, root)
		}
	}
}

func hasMediaComponents(root map[string]any) bool {
	components, componentsAreMap := asMap(root["components"])
	if !componentsAreMap {
		return false
	}

	return hasKey(components, "responses") || hasKey(components, "requestBodies")
}

func (v *validator) walkOpenAPIMedia(doc *document, value any, pointer string) {
	switch current := value.(type) {
	case map[string]any:
		v.walkOpenAPIMediaObject(doc, current, pointer)
	case []any:
		for index, child := range current {
			v.walkOpenAPIMedia(doc, child, joinPointer(pointer, strconv.Itoa(index)))
		}
	}
}

func (v *validator) walkOpenAPIMediaObject(doc *document, object map[string]any, pointer string) {
	if isOpenAPIExampleObject(object) {
		return
	}

	for key, child := range object {
		if key == "schema" || key == exampleKey || key == examplesKey || key == valueKey {
			continue
		}

		if key == "content" {
			v.walkOpenAPIMediaContent(doc, child, joinPointer(pointer, key))

			continue
		}

		v.walkOpenAPIMedia(doc, child, joinPointer(pointer, key))
	}
}

func (v *validator) walkOpenAPIMediaContent(doc *document, value any, pointer string) {
	content, ok := asMap(value)
	if !ok {
		return
	}

	for _, mediaType := range sortedKeys(content) {
		mediaPointer := joinPointer(pointer, mediaType)

		media, ok := asMap(content[mediaType])
		if !ok {
			continue
		}

		v.validateMediaExamples(doc, mediaPointer, media)
		v.walkOpenAPIMedia(doc, media, mediaPointer)
	}
}

func (v *validator) validateMediaExamples(doc *document, pointer string, media map[string]any) bool {
	_, hasSchema := media["schema"]
	schemaPointer := joinPointer(pointer, "schema")

	if !hasSchema {
		if hasKey(media, exampleKey) || nonEmptyExamples(media[examplesKey]) {
			v.addIssue(doc.path, pointer, "media examples have no schema owner")
		}

		return false
	}

	owner := location{doc: doc, pointer: schemaPointer}
	found := false

	if sample, ok := media[exampleKey]; ok {
		found = true

		v.validateValue(owner, sample, joinPointer(pointer, exampleKey), media[exampleEvidenceKey])
	}

	if examples, ok := media[examplesKey]; ok {
		found = v.validateMediaExampleObjects(doc, owner, pointer, examples) || found
	}

	return found
}

func (v *validator) validateMediaExampleObjects(doc *document, owner location, pointer string, rawExamples any) bool {
	entries, ok := asMap(rawExamples)
	if !ok {
		v.addIssue(doc.path, joinPointer(pointer, examplesKey), "media examples must be a named object map")

		return false
	}

	found := false

	for name, rawExample := range entries {
		examplePointer := joinPointer(joinPointer(pointer, examplesKey), name)

		exampleDoc, resolvedPointer, exampleObject, err := v.resolveExampleObject(doc, rawExample, examplePointer)
		if err != nil {
			v.addIssue(doc.path, examplePointer, "unresolved example owner reference")

			found = true

			continue
		}

		exampleMap, ok := asMap(exampleObject)
		if !ok {
			v.addIssue(exampleDoc.path, resolvedPointer, "media example must be an Example Object")

			found = true

			continue
		}

		found = true

		v.validateOpenAPIExampleObject(owner, exampleDoc, resolvedPointer, exampleMap)
	}

	return found
}

func (v *validator) resolveExampleObject(doc *document, raw any, pointer string) (*document, string, any, error) {
	currentDoc := doc
	currentPointer := pointer
	current := raw
	seen := make(map[string]bool)

	for {
		object, ok := asMap(current)
		if !ok {
			return currentDoc, currentPointer, current, nil
		}

		ref, hasRef := object["$ref"].(string)
		if !hasRef {
			return currentDoc, currentPointer, current, nil
		}

		key := currentDoc.uri + currentPointer + ref
		if seen[key] {
			return nil, "", nil, errCyclicExampleReference
		}

		seen[key] = true

		resolvedDoc, resolvedPointer, resolved, err := v.resolveReference(currentDoc, ref)
		if err != nil {
			return nil, "", nil, err
		}

		if strings.Contains(resolvedPointer, "#/components/examples/") {
			v.usedExampleObjects[resolvedDoc.uri+resolvedPointer] = true
		}

		currentDoc, currentPointer, current = resolvedDoc, resolvedPointer, resolved
	}
}

func (v *validator) validateOpenAPIExampleObject(
	owner location,
	exampleDoc *document,
	pointer string,
	example map[string]any,
) {
	if hasKey(example, "externalValue") {
		v.addIssue(exampleDoc.path, joinPointer(pointer, "externalValue"), "external example values are not allowed")
	}

	value, hasValue := example[valueKey]
	if !hasValue {
		v.addIssue(exampleDoc.path, pointer, "Example Object requires a local value")

		return
	}

	v.validateValue(owner, value, joinPointer(pointer, valueKey), example[exampleEvidenceKey])
}

func (v *validator) collectOpenAPIGroups(doc *document, root map[string]any) {
	paths, ok := asMap(root["paths"])
	if !ok {
		return
	}

	for _, pathName := range sortedKeys(paths) {
		v.collectOpenAPIPathGroups(doc, pathName, paths[pathName])
	}
}

func (v *validator) collectOpenAPIPathGroups(doc *document, pathName string, rawPath any) {
	pathPointer := joinPointer(joinPointer("#", "paths"), pathName)

	pathDoc, resolvedPointer, resolved, err := v.resolveObject(doc, pathPointer, rawPath)
	if err != nil {
		v.addIssue(doc.path, pathPointer, "unresolved path item")

		return
	}

	pathItem, ok := asMap(resolved)
	if !ok {
		return
	}

	for _, method := range []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"} {
		v.collectOpenAPIOperationGroups(pathDoc, pathName, resolvedPointer, pathItem, method)
	}
}

func (v *validator) collectOpenAPIOperationGroups(
	pathDoc *document,
	pathName string,
	pathPointer string,
	pathItem map[string]any,
	method string,
) {
	operation, exists := asMap(pathItem[method])
	if !exists {
		return
	}

	operationPointer := joinPointer(pathPointer, method)
	identity := strings.ToUpper(method) + " " + pathName
	v.collectOpenAPIRequestBodyGroup(pathDoc, operationPointer, operation, identity)
	v.collectOpenAPIResponseGroups(pathDoc, operationPointer, operation, identity)
}

func (v *validator) collectOpenAPIRequestBodyGroup(
	pathDoc *document,
	operationPointer string,
	operation map[string]any,
	identity string,
) {
	requestBody, exists := operation["requestBody"]
	if !exists {
		return
	}

	requestPointer := joinPointer(operationPointer, "requestBody")

	bodyDoc, bodyPointer, bodyValue, err := v.resolveObject(pathDoc, requestPointer, requestBody)
	if err != nil {
		v.addIssue(pathDoc.path, requestPointer, "unresolved request body")

		return
	}

	body, isObject := asMap(bodyValue)
	if isObject {
		v.collectMediaGroups(bodyDoc, bodyPointer, body["content"], identity+" request")
	}
}

func (v *validator) collectOpenAPIResponseGroups(
	pathDoc *document,
	operationPointer string,
	operation map[string]any,
	identity string,
) {
	responses, ok := asMap(operation["responses"])
	if !ok {
		return
	}

	for _, status := range sortedKeys(responses) {
		role, required := responseRole(status)
		if !required {
			continue
		}

		v.collectOpenAPIResponseGroup(pathDoc, operationPointer, status, role, responses[status], identity)
	}
}

func (v *validator) collectOpenAPIResponseGroup(
	pathDoc *document,
	operationPointer string,
	status string,
	role string,
	rawResponse any,
	identity string,
) {
	responsePointer := joinPointer(joinPointer(operationPointer, "responses"), status)

	responseDoc, resolvedPointer, responseValue, err := v.resolveObject(pathDoc, responsePointer, rawResponse)
	if err != nil {
		v.addIssue(pathDoc.path, responsePointer, "unresolved response")

		return
	}

	response, ok := asMap(responseValue)
	if !ok {
		return
	}

	label := identity + " " + role + " response " + status
	v.collectMediaGroups(responseDoc, resolvedPointer, response["content"], label)
}

func (v *validator) collectMediaGroups(doc *document, parentPointer string, rawContent any, label string) {
	content, ok := asMap(rawContent)
	if !ok {
		return
	}

	for _, mediaType := range sortedKeys(content) {
		media, ok := asMap(content[mediaType])
		if !ok {
			continue
		}

		_, hasSchema := media["schema"]
		if !hasSchema {
			continue
		}

		mediaPointer := joinPointer(joinPointer(parentPointer, "content"), mediaType)
		schemaPointer := joinPointer(mediaPointer, "schema")
		groupName := label + " " + mediaType
		v.report.groups++

		if hasMediaExampleDeclaration(media) {
			continue
		}

		if v.rootExampleExists(location{doc: doc, pointer: schemaPointer}, make(map[string]bool)) {
			continue
		}

		v.addIssue(doc.path, schemaPointer, "missing required sample group: "+groupName)
	}
}

func hasMediaExampleDeclaration(media map[string]any) bool {
	if hasKey(media, exampleKey) {
		return true
	}

	return nonEmptyExamples(media[examplesKey])
}

func responseRole(status string) (string, bool) {
	switch {
	case strings.EqualFold(status, "default"), strings.HasPrefix(status, "4"), strings.HasPrefix(status, "5"):
		return "failure", true
	case strings.HasPrefix(status, "2"):
		return "success", true
	default:
		return "", false
	}
}

func (v *validator) resolveObject(doc *document, pointer string, value any) (*document, string, any, error) {
	currentDoc, currentPointer, current := doc, pointer, value
	seen := make(map[string]bool)

	for {
		object, ok := asMap(current)
		if !ok {
			return currentDoc, currentPointer, current, nil
		}

		ref, hasRef := object["$ref"].(string)
		if !hasRef {
			return currentDoc, currentPointer, current, nil
		}

		key := currentDoc.uri + currentPointer + ref
		if seen[key] {
			return nil, "", nil, errCyclicReference
		}

		seen[key] = true

		resolvedDoc, resolvedPointer, resolved, err := v.resolveReference(currentDoc, ref)
		if err != nil || resolvedDoc == nil {
			return nil, "", nil, errUnresolvedReference
		}

		currentDoc, currentPointer, current = resolvedDoc, resolvedPointer, resolved
	}
}
