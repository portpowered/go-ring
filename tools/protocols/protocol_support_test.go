package protocols

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

type openAPIDocument struct {
	raw        map[string]any
	doc        *openapi3.T
	validators map[string]*jsonschema.Schema
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func readYAMLObject(t *testing.T, path string) map[string]any {
	t.Helper()
	// #nosec G304 -- callers use checked-in API paths under the repository.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := yaml.Unmarshal(data, &result); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return result
}

func rejectRemoteReferences(t *testing.T, value any, path string) {
	t.Helper()
	switch current := value.(type) {
	case map[string]any:
		if ref, ok := current["$ref"].(string); ok && !strings.HasPrefix(ref, "#/") {
			t.Errorf("%s has a network or external reference %q", path, ref)
		}
		for key, child := range current {
			rejectRemoteReferences(t, child, path+"."+key)
		}
	case map[any]any:
		for key, child := range current {
			rejectRemoteReferences(t, child, fmt.Sprintf("%s.%v", path, key))
		}
	case []any:
		for index, child := range current {
			rejectRemoteReferences(t, child, fmt.Sprintf("%s[%d]", path, index))
		}
	case []map[string]any:
		for index, child := range current {
			rejectRemoteReferences(t, child, fmt.Sprintf("%s[%d]", path, index))
		}
	}
}

func loadOpenAPI(t *testing.T, name string) openAPIDocument {
	t.Helper()
	path := filepath.Join(repositoryRoot(t), "api", name)
	raw := readYAMLObject(t, path)
	rejectRemoteReferences(t, raw, name)

	normalized := mapValue(cloneJSON(t, raw))
	normalizeOpenAPISchemaForKin(normalized)
	normalizedBytes, err := yaml.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	doc, err := loader.LoadFromData(normalizedBytes)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate %s: %v", name, err)
	}
	return openAPIDocument{raw: raw, doc: doc, validators: map[string]*jsonschema.Schema{}}
}

func (doc openAPIDocument) validate(t *testing.T, schema *openapi3.SchemaRef, value any) error {
	t.Helper()
	if schema == nil || schema.Value == nil {
		return missingSchemaError{}
	}
	keyData, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("encode OpenAPI schema: %v", err)
	}
	key := string(keyData)
	compiled := doc.validators[key]
	if compiled == nil {
		var schemaValue any
		if err := json.Unmarshal(keyData, &schemaValue); err != nil {
			t.Fatalf("decode OpenAPI schema: %v", err)
		}
		restoreJSONSchemaNullTypes(schemaValue)
		compiled = compileJSONSchema(t, doc.raw, schemaValue)
		doc.validators[key] = compiled
	}
	return compiled.Validate(value)
}

type missingSchemaError struct{}

func (missingSchemaError) Error() string { return "schema is missing or unresolved" }

func normalizeOpenAPISchemaForKin(value any) {
	switch current := value.(type) {
	case map[string]any:
		if constant, exists := current["const"]; exists {
			if _, ok := current["enum"]; ok {
				current["allOf"] = append(sliceValue(current["allOf"]), map[string]any{"enum": []any{constant}})
			} else {
				current["enum"] = []any{constant}
			}
			delete(current, "const")
		}
		if current["type"] == "null" {
			delete(current, "type")
			if _, ok := current["enum"]; ok {
				current["allOf"] = append(sliceValue(current["allOf"]), map[string]any{"enum": []any{nil}})
			} else {
				current["enum"] = []any{nil}
			}
		}
		if types, ok := current["type"].([]any); ok {
			nonNull := make([]any, 0, len(types))
			hasNull := false
			for _, schemaType := range types {
				if schemaType == "null" {
					hasNull = true
				} else {
					nonNull = append(nonNull, schemaType)
				}
			}
			if hasNull && len(nonNull) == 1 {
				current["type"] = nonNull[0]
				current["nullable"] = true
			}
		}
		for _, child := range current {
			normalizeOpenAPISchemaForKin(child)
		}
	case []any:
		for _, child := range current {
			normalizeOpenAPISchemaForKin(child)
		}
	}
}

func restoreJSONSchemaNullTypes(value any) {
	switch current := value.(type) {
	case map[string]any:
		if current["nullable"] == true {
			if schemaType, ok := current["type"].(string); ok {
				current["type"] = []any{schemaType, "null"}
				delete(current, "nullable")
			}
		}
		for _, child := range current {
			restoreJSONSchemaNullTypes(child)
		}
	case []any:
		for _, child := range current {
			restoreJSONSchemaNullTypes(child)
		}
	}
}

func compileJSONSchema(t *testing.T, root map[string]any, schema any) *jsonschema.Schema {
	t.Helper()
	resourceURL := "https://go-ring.invalid/protocol-schema.json"
	resource := map[string]any{
		"$schema": jsonschema.Draft2020.String(),
		"schema":  schema,
	}
	for key, value := range root {
		resource[key] = value
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	if err := compiler.AddResource(resourceURL, resource); err != nil {
		t.Fatalf("add JSON Schema resource: %v", err)
	}
	compiled, err := compiler.Compile(resourceURL + "#/schema")
	if err != nil {
		t.Fatalf("compile Draft 2020-12 JSON Schema: %v", err)
	}
	return compiled
}

func mapValue(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func sliceValue(value any) []any {
	result, _ := value.([]any)
	return result
}

func stringValue(value any) string {
	result, _ := value.(string)
	return result
}

func nestedMap(value any, keys ...string) map[string]any {
	current := value
	for _, key := range keys {
		current = mapValue(current)[key]
	}
	return mapValue(current)
}

func cloneJSON(t *testing.T, value any) any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func schemaNamed(t *testing.T, doc openAPIDocument, name string) *openapi3.SchemaRef {
	t.Helper()
	if doc.doc.Components == nil {
		t.Fatal("OpenAPI components are missing")
	}
	schema := doc.doc.Components.Schemas[name]
	if schema == nil {
		t.Fatalf("schema %q is missing", name)
	}
	schemaCopy := *schema
	schemaCopy.Ref = "#/components/schemas/" + name
	return &schemaCopy
}

func operationFor(t *testing.T, doc openAPIDocument, path, method string) *openapi3.Operation {
	t.Helper()
	item := doc.doc.Paths.Value(path)
	if item == nil {
		t.Fatalf("OpenAPI path %q is missing", path)
	}
	operation := item.GetOperation(strings.ToUpper(method))
	if operation == nil {
		t.Fatalf("OpenAPI operation %s %s is missing", method, path)
	}
	return operation
}

func capturedHTTPFiles(t *testing.T) []string {
	t.Helper()
	pattern := filepath.Join(repositoryRoot(t), "tests", "replay", "fixtures", "http", "captured", "**", "*.json")
	root := filepath.Join(repositoryRoot(t), "tests", "replay", "fixtures", "http", "captured")
	files := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(path), ".json") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk HTTP fixture tree (%s): %v", pattern, err)
	}
	sort.Strings(files)
	return files
}

type capturedQueryParameter struct {
	Name string `json:"name"`
}

type capturedHTTPRequest struct {
	Method string                   `json:"method"`
	Origin string                   `json:"origin"`
	Path   string                   `json:"path"`
	Query  []capturedQueryParameter `json:"query"`
	Body   any                      `json:"body"`
	JSON   bool                     `json:"json"`
}

type capturedHTTPResponse struct {
	Status int  `json:"status"`
	Body   any  `json:"body"`
	JSON   bool `json:"json"`
}

type capturedHTTPExchange struct {
	Request  capturedHTTPRequest  `json:"request"`
	Response capturedHTTPResponse `json:"response"`
}

func readHTTPExchange(t *testing.T, path string) capturedHTTPExchange {
	t.Helper()
	// #nosec G304 -- callers enumerate checked-in captured fixture paths.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var row capturedHTTPExchange
	if err := json.Unmarshal(data, &row); err != nil {
		t.Fatalf("parse fixture %s: %v", filepath.Base(path), err)
	}
	return row
}

func httpRequestSchema(operation *openapi3.Operation) (*openapi3.RequestBody, *openapi3.SchemaRef) {
	if operation.RequestBody == nil || operation.RequestBody.Value == nil {
		return nil, nil
	}
	body := operation.RequestBody.Value
	media := body.Content["application/json"]
	if media == nil {
		return body, nil
	}
	return body, media.Schema
}

func httpResponseSchema(operation *openapi3.Operation, status int) (*openapi3.Response, *openapi3.SchemaRef) {
	if operation.Responses == nil {
		return nil, nil
	}
	responseRef := operation.Responses.Value(strconv.Itoa(status))
	if responseRef == nil || responseRef.Value == nil {
		return nil, nil
	}
	response := responseRef.Value
	media := response.Content["application/json"]
	if media == nil {
		return response, nil
	}
	return response, media.Schema
}
