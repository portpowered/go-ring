package main

import "strconv"

const (
	exampleKey           = "example"
	examplesKey          = "examples"
	valueKey             = "value"
	constKey             = "const"
	enumKey              = "enum"
	defsKey              = "$defs"
	definitionsKey       = "definitions"
	propertiesKey        = "properties"
	patternPropertiesKey = "patternProperties"
	dependentSchemasKey  = "dependentSchemas"
	dependenciesKey      = "dependencies"
)

func (v *validator) validateSchemaDeclarations() {
	docs := v.sortedDocuments()
	for _, doc := range docs {
		v.walkDocumentSchemas(doc, doc.data, "#")
	}
}

func (v *validator) walkDocumentSchemas(doc *document, value any, pointer string) {
	switch current := value.(type) {
	case map[string]any:
		v.walkDocumentSchemaObject(doc, current, pointer)
	case []any:
		for index, child := range current {
			v.walkDocumentSchemas(doc, child, joinPointer(pointer, strconv.Itoa(index)))
		}
	}
}

func (v *validator) walkDocumentSchemaObject(doc *document, object map[string]any, pointer string) {
	if isOpenAPIExampleObject(object) {
		return
	}

	if pointer == "#" && isSchemaObject(object) {
		v.schemaNode(doc, object, pointer)

		return
	}

	for key, child := range object {
		childPointer := joinPointer(pointer, key)
		if v.walkNamedDocumentSchema(doc, key, child, childPointer) {
			continue
		}

		v.walkDocumentSchemas(doc, child, childPointer)
	}
}

func (v *validator) walkNamedDocumentSchema(doc *document, key string, child any, pointer string) bool {
	switch key {
	case "schemas":
		schemaMap, ok := asMap(child)
		if !ok {
			return false
		}

		for name, schema := range schemaMap {
			v.schemaNode(doc, schema, joinPointer(pointer, name))
		}

		return true
	case "schema":
		v.schemaNode(doc, child, pointer)

		return true
	case exampleKey, examplesKey, valueKey, "default", constKey, enumKey, "messages":
		return true
	default:
		return false
	}
}

func (v *validator) schemaNode(doc *document, value any, pointer string) {
	schema, isMap := asMap(value)
	owner := location{doc: doc, pointer: pointer}

	if !isMap {
		return
	}

	if sample, ok := schema[exampleKey]; ok {
		v.validateValue(owner, sample, joinPointer(pointer, exampleKey), schema[exampleEvidenceKey])
	}

	if examples, ok := schema[examplesKey]; ok {
		list, ok := examples.([]any)

		switch {
		case !ok:
			v.addIssue(doc.path, joinPointer(pointer, examplesKey), "schema examples must be an array")
		case len(list) == 0:
			v.addIssue(doc.path, joinPointer(pointer, examplesKey), "schema examples must contain at least one value")
		default:
			for index, sample := range list {
				examplesPointer := joinPointer(joinPointer(pointer, examplesKey), strconv.Itoa(index))
				v.validateValue(owner, sample, examplesPointer, schema[exampleEvidenceKey])
			}
		}
	}

	for key, child := range schema {
		if isSchemaChildKey(key) {
			v.schemaChild(doc, key, child, joinPointer(pointer, key))
		}
	}
}

func (v *validator) schemaChild(doc *document, key string, value any, pointer string) {
	switch child := value.(type) {
	case map[string]any:
		if isSchemaMapContainer(key) {
			for name, schema := range child {
				v.schemaNode(doc, schema, joinPointer(pointer, name))
			}

			return
		}

		if key == dependenciesKey {
			for name, schema := range child {
				if _, ok := asMap(schema); ok {
					v.schemaNode(doc, schema, joinPointer(pointer, name))
				}
			}

			return
		}

		v.schemaNode(doc, child, pointer)
	case []any:
		for index, schema := range child {
			v.schemaNode(doc, schema, joinPointer(pointer, strconv.Itoa(index)))
		}
	}
}

func isSchemaMapContainer(key string) bool {
	switch key {
	case propertiesKey, patternPropertiesKey, definitionsKey, defsKey, dependentSchemasKey:
		return true
	default:
		return false
	}
}

func isOpenAPIExampleObject(value map[string]any) bool {
	if !hasKey(value, valueKey) && !hasKey(value, "externalValue") {
		return false
	}

	for key := range value {
		switch key {
		case "$ref", "summary", "description", valueKey, "externalValue", exampleEvidenceKey:
		default:
			return false
		}
	}

	return true
}
