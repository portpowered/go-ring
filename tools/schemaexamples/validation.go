package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func (v *validator) compileSchema(owner location) *jsonschema.Schema {
	key := owner.doc.uri + owner.pointer
	if schema, ok := v.compiled[key]; ok {
		return schema
	}

	if v.compiler == nil {
		return nil
	}

	schema, err := v.compiler.Compile(pointerURL(owner.doc, owner.pointer))
	if err != nil {
		v.addIssue(owner.doc.path, owner.pointer, "schema could not be compiled")

		return nil
	}

	v.compiled[key] = schema

	return schema
}

func safeValidationReason(err error) string {
	var validationErr *jsonschema.ValidationError
	if errors.As(err, &validationErr) && validationErr.ErrorKind != nil {
		keywords := validationErr.ErrorKind.KeywordPath()
		if len(keywords) > 0 {
			return "does not satisfy schema keyword " + strings.Join(keywords, ".")
		}
	}

	return "does not satisfy its schema"
}

func (v *validator) validateValue(owner location, sample any, samplePointer string, evidence any) {
	v.report.examples++
	if evidence != "synthetic" {
		v.addIssue(owner.doc.path, samplePointer, "sample requires x-example-evidence: synthetic")
	}

	schema := v.compileSchema(owner)
	if schema == nil {
		return
	}

	err := schema.Validate(sample)
	if err != nil {
		v.addIssue(owner.doc.path, samplePointer, safeValidationReason(err))
	}
}

func (v *validator) addIssue(path, pointer, message string) {
	relative, err := filepath.Rel(".", path)
	if err == nil {
		path = filepath.ToSlash(relative)
	}

	formattedIssue := fmt.Sprintf("%s%s: %s", filepath.ToSlash(path), pointer, message)
	v.issues = append(v.issues, formattedIssue)
}

func asMap(value any) (map[string]any, bool) {
	result, ok := value.(map[string]any)

	return result, ok
}

func joinPointer(pointer, token string) string {
	token = strings.ReplaceAll(token, "~", "~0")
	token = strings.ReplaceAll(token, "/", "~1")

	if pointer == "" {
		pointer = "#"
	}

	return pointer + "/" + token
}

func (v *validator) rootExampleExists(owner location, seen map[string]bool) bool {
	key := owner.doc.uri + owner.pointer
	if seen[key] {
		return false
	}

	seen[key] = true

	node, ok := resolvePointer(owner.doc.data, owner.pointer)
	if !ok {
		return false
	}

	if schema, ok := asMap(node); ok {
		if hasKey(schema, exampleKey) || nonEmptyExamples(schema[examplesKey]) {
			return true
		}

		if ref, ok := schema["$ref"].(string); ok {
			doc, pointer, _, err := v.resolveReference(owner.doc, ref)
			if err == nil {
				return v.rootExampleExists(location{doc: doc, pointer: pointer}, seen)
			}
		}
	}

	return false
}

func hasKey(value map[string]any, key string) bool {
	_, ok := value[key]

	return ok
}

func nonEmptyExamples(value any) bool {
	switch examples := value.(type) {
	case []any:
		return len(examples) > 0
	case map[string]any:
		return len(examples) > 0
	default:
		return false
	}
}

func isSchemaObject(value map[string]any) bool {
	for _, key := range []string{
		"$schema", "$id", "$ref", "$dynamicRef", "$anchor", defsKey, definitionsKey, "type",
		propertiesKey, patternPropertiesKey, "additionalProperties", "unevaluatedProperties", "required",
		enumKey, constKey, "items", "prefixItems", "allOf", "anyOf", "oneOf", "not", "if", "then", "else",
		"format", "contentSchema", "contentMediaType", "contentEncoding", "minimum", "maximum",
		"exclusiveMinimum", "exclusiveMaximum", "minLength", "maxLength", "pattern", "uniqueItems",
		"minItems", "maxItems", "minProperties", "maxProperties", dependenciesKey, dependentSchemasKey,
		"propertyNames", "readOnly", "writeOnly", "nullable",
	} {
		if _, ok := value[key]; ok {
			return true
		}
	}

	return false
}

func isSchemaChildKey(key string) bool {
	switch key {
	case propertiesKey, patternPropertiesKey, "additionalProperties", "unevaluatedProperties", dependentSchemasKey,
		definitionsKey, defsKey, "items", "prefixItems", "contains", "propertyNames", "if", "then", "else",
		"not", "allOf", "anyOf", "oneOf", "additionalItems", dependenciesKey, "contentSchema":
		return true
	default:
		return false
	}
}
