package routegate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

type generatedSignalingField struct {
	property      string
	omitEmpty     bool
	pointer       bool
	generatedType string
}

func generatedAsyncPrimitiveFields(root string) (map[string]map[string]schemaPrimitiveField, error) {
	var document map[string]any

	err := readYAML(filepath.Join(root, "api", "asyncapi.yaml"), &document)
	if err != nil {
		return nil, err
	}

	components := stringMap(stringMap(document["components"])["schemas"])
	schemaProperties := generatedSchemaPrimitiveProperties(components)

	declarations, err := generatedSignalingDeclarations(root)
	if err != nil {
		return nil, err
	}

	return mergeGeneratedSchemaProperties(schemaProperties, declarations), nil
}

func generatedSchemaPrimitiveProperties(components map[string]any) map[string]map[string]schemaPrimitiveField {
	schemaProperties := make(map[string]map[string]schemaPrimitiveField, len(components))

	for schemaName, rawSchema := range components {
		properties := stringMap(stringMap(rawSchema)["properties"])
		constrained := make(map[string]schemaPrimitiveField)

		for propertyName, propertySchema := range properties {
			values, isOpen, propertyConstrained := schemaPrimitiveValues(propertySchema, components, make(map[string]bool))
			if propertyConstrained {
				constrained[propertyName] = schemaPrimitiveField{
					knownValues:   values,
					openValues:    isOpen,
					omitEmpty:     false,
					pointer:       false,
					generatedType: "",
				}
			}
		}

		schemaProperties[schemaName] = constrained
	}

	return schemaProperties
}

func generatedSignalingDeclarations(root string) (map[string]map[string]generatedSignalingField, error) {
	modelPath := filepath.Join(root, "pkg", "dependencymodels", "signaling")

	files, err := filepath.Glob(filepath.Join(modelPath, "*.go"))
	if err != nil {
		return nil, wrapRouteGateError(err, "find generated signaling models")
	}

	declarations := make(map[string]map[string]generatedSignalingField)

	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}

		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		if parseErr != nil {
			return nil, wrapRouteGateError(parseErr, "parse generated signaling model %s", path)
		}

		collectGeneratedSignalingDeclarations(file, declarations)
	}

	return declarations, nil
}

func collectGeneratedSignalingDeclarations(
	file *ast.File,
	declarations map[string]map[string]generatedSignalingField,
) {
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}

		for _, rawSpec := range general.Specs {
			specification, ok := rawSpec.(*ast.TypeSpec)
			if !ok {
				continue
			}

			structure, ok := specification.Type.(*ast.StructType)
			if !ok {
				continue
			}

			declarations[specification.Name.Name] = generatedSignalingFields(structure)
		}
	}
}

func generatedSignalingFields(structure *ast.StructType) map[string]generatedSignalingField {
	fields := make(map[string]generatedSignalingField)

	for _, field := range structure.Fields.List {
		if field.Tag == nil || len(field.Names) == 0 {
			continue
		}

		rawTag, validTag := stringConstant(field.Tag)
		if !validTag {
			continue
		}

		jsonName, exists := reflect.StructTag(rawTag).Lookup("json")
		jsonName, options, _ := strings.Cut(jsonName, ",")

		if !exists || jsonName == "" || jsonName == "-" {
			continue
		}

		generatedField := generatedSignalingField{
			property:      jsonName,
			omitEmpty:     strings.Contains(","+options+",", ",omitempty,"),
			pointer:       isPointerType(field.Type),
			generatedType: generatedGoType(field.Type),
		}
		for _, name := range field.Names {
			fields[name.Name] = generatedField
		}
	}

	return fields
}

func mergeGeneratedSchemaProperties(
	schemaProperties map[string]map[string]schemaPrimitiveField,
	declarations map[string]map[string]generatedSignalingField,
) map[string]map[string]schemaPrimitiveField {
	result := make(map[string]map[string]schemaPrimitiveField)

	for schemaName, properties := range schemaProperties {
		modelName := modelinaGoName(schemaName)

		fields, exists := declarations[modelName]
		if !exists {
			continue
		}

		for fieldName, field := range fields {
			property, constrained := properties[field.property]
			if !constrained {
				continue
			}

			if result[modelName] == nil {
				result[modelName] = make(map[string]schemaPrimitiveField)
			}

			property.omitEmpty = field.omitEmpty
			property.pointer = field.pointer
			property.generatedType = field.generatedType
			result[modelName][fieldName] = property
		}
	}

	return result
}

func schemaPrimitiveValues(
	schema any,
	components map[string]any,
	seen map[string]bool,
) (map[string]bool, bool, bool) {
	current, ok := schema.(map[string]any)
	if !ok {
		return nil, false, false
	}

	values, open, constrained := ownSchemaPrimitiveValues(current)
	mergeReferencedSchemaPrimitiveValues(current, components, seen, values, &open, &constrained)

	if _, hasProperties := current["properties"]; hasProperties {
		return values, open, constrained
	}

	mergeComposedSchemaPrimitiveValues(current, components, seen, values, &open, &constrained)

	return values, open, constrained
}

func ownSchemaPrimitiveValues(current map[string]any) (map[string]bool, bool, bool) {
	values := make(map[string]bool)
	constrained := false
	open := false

	for _, key := range [...]string{"const", "enum", "x-extensible-enum"} {
		raw, exists := current[key]
		if !exists {
			continue
		}

		constrained = true

		if key == "x-extensible-enum" {
			open = true
		}

		addSchemaPrimitiveValues(key, raw, values)
	}

	return values, open, constrained
}

func addSchemaPrimitiveValues(key string, raw any, values map[string]bool) {
	if key == "const" {
		if value, valid := schemaScalarString(raw); valid {
			values[value] = true
		}

		return
	}

	for _, item := range schemaSlice(raw) {
		if value, valid := schemaScalarString(item); valid {
			values[value] = true
		}
	}
}

func mergeReferencedSchemaPrimitiveValues(
	current map[string]any,
	components map[string]any,
	seen map[string]bool,
	values map[string]bool,
	open *bool,
	constrained *bool,
) {
	if reference, ok := current["$ref"].(string); ok {
		const prefix = "#/components/schemas/"
		if strings.HasPrefix(reference, prefix) {
			name := strings.TrimPrefix(reference, prefix)
			mergeNamedSchemaPrimitiveValues(name, components, seen, values, open, constrained)
		}
	}
}

func mergeNamedSchemaPrimitiveValues(
	name string,
	components map[string]any,
	seen map[string]bool,
	values map[string]bool,
	open *bool,
	constrained *bool,
) {
	if seen[name] {
		return
	}

	seen[name] = true
	referencedValues, referencedOpen, referenced := schemaPrimitiveValues(components[name], components, seen)
	mergeSchemaValues(values, referencedValues)

	*constrained = *constrained || referenced
	*open = *open || referencedOpen
}

func mergeComposedSchemaPrimitiveValues(
	current map[string]any,
	components map[string]any,
	seen map[string]bool,
	values map[string]bool,
	open *bool,
	constrained *bool,
) {
	for _, key := range [...]string{"allOf", "anyOf", "oneOf"} {
		children, _ := current[key].([]any)

		for _, child := range children {
			childValues, childOpen, childConstrained := schemaPrimitiveValues(child, components, seen)
			mergeSchemaValues(values, childValues)

			*constrained = *constrained || childConstrained
			*open = *open || childOpen
		}
	}
}

func schemaSlice(value any) []any {
	items, _ := value.([]any)

	return items
}

func schemaScalarString(value any) (string, bool) {
	switch scalar := value.(type) {
	case string:
		return scalar, true
	case int:
		return strconv.Itoa(scalar), true
	case int64:
		return strconv.FormatInt(scalar, 10), true
	case float64:
		return strconv.FormatFloat(scalar, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(scalar), true
	default:
		return "", false
	}
}

func mergeSchemaValues(destination, source map[string]bool) {
	for value := range source {
		destination[value] = true
	}
}

func isPointerType(expression ast.Expr) bool {
	_, ok := unparen(expression).(*ast.StarExpr)

	return ok
}

func generatedGoType(expression ast.Expr) string {
	switch value := unparen(expression).(type) {
	case *ast.StarExpr:
		return generatedGoType(value.X)
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return value.Sel.Name
	default:
		return ""
	}
}

func modelinaGoName(name string) string {
	for _, acronym := range []string{"ICE", "PTZ", "RPC"} {
		name = strings.ReplaceAll(name, acronym, strings.ToUpper(acronym[:1])+strings.ToLower(acronym[1:]))
	}

	return name
}
