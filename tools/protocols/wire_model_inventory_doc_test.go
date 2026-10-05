package protocols_test

import (
	"bytes"
	"go/ast"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const wireInventoryUpdateEnvironment = "UPDATE_WIRE_MODEL_INVENTORY"
const wireInventoryModulePath = "github.com/portpowered/go-ring"

type documentedWireModel struct {
	family      string
	packagePath string
	goType      string
	schemaOwner string
	sourceFile  string
	generator   string
	typeSpec    *ast.TypeSpec
}

func TestWireModelInventoryDocumentMatchesGeneratedSources(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	assertWireInventoryGeneratorCommands(t, root)

	document := renderWireModelInventory(t, root)
	path := filepath.Join(root, "docs", "wire-model-inventory.md")

	if os.Getenv(wireInventoryUpdateEnvironment) == "1" {
		err := os.WriteFile(path, document, 0o600)
		if err != nil {
			t.Fatalf("write generated wire model inventory: %v", err)
		}

		return
	}

	actual, err := os.ReadFile(path) // #nosec G304 -- path is the checked-in inventory under repositoryRoot.
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(actual, document) {
		t.Fatalf(
			"wire model inventory is stale; regenerate it with %s=1 go test ./tools/protocols -run "+
				"TestWireModelInventoryDocumentMatchesGeneratedSources",
			wireInventoryUpdateEnvironment,
		)
	}
}

func renderWireModelInventory(t *testing.T, root string) []byte {
	t.Helper()

	models := make([]documentedWireModel, 0, 520)
	models = append(models, restWireModels(t, root)...)
	models = append(models, standaloneOpenAPIWireModels(
		t, root, "FCM HTTP and JSON payloads", "api/external/fcm.openapi.yaml",
		"pkg/dependencymodels/fcm", "fcm", "oapi-codegen v2.8.0 via make generate-api",
	)...)
	models = append(models, standaloneOpenAPIWireModels(
		t, root, "Public read projections", "api/client-models.openapi.yaml",
		"pkg/ringapimodels", "ringapimodels", "oapi-codegen v2.8.0 via make generate-api",
	)...)
	models = append(models, signalingWireModels(t, root)...)
	models = append(models, protobufWireModels(t, root)...)

	uses, parents := productionModelUses(t, root, models)

	rows := make([]string, 0, len(models))

	for _, model := range models {
		productionUses := documentedModelUses(model, uses, parents)
		rows = append(rows, modelMarkdownRow(model, productionUses))
	}

	slices.Sort(rows)

	var document strings.Builder

	document.WriteString("# Wire model inventory\n\n")
	document.WriteString(
		"This file is generated from canonical schemas and checked-in Go models by " +
			"`TestWireModelInventoryDocumentMatchesGeneratedSources`. The check verifies each " +
			"generated declaration has a schema owner and records direct or enclosing production " +
			"uses. Regenerate after a schema or model change with " +
			"`UPDATE_WIRE_MODEL_INVENTORY=1 go test ./tools/protocols -run " +
			"TestWireModelInventoryDocumentMatchesGeneratedSources`.\n\n",
	)
	document.WriteString(
		"| Schema or message owner | Generated Go type | Generated file | Generator | " +
			"Production uses |\n",
	)
	document.WriteString("| --- | --- | --- | --- | --- |\n")

	for _, row := range rows {
		document.WriteString(row)
		document.WriteByte('\n')
	}

	return []byte(document.String())
}

func modelMarkdownRow(model documentedWireModel, uses []string) string {
	formattedUses := make([]string, len(uses))
	for index, use := range uses {
		formattedUses[index] = "`" + use + "`"
	}

	return "| `" + model.schemaOwner + "` | `" + model.goType + "` | [`" + model.sourceFile + "`](../" +
		model.sourceFile + ") | `" + model.generator + "` | " + strings.Join(formattedUses, "<br>") + " |"
}

func packageNameFromType(goType string) string {
	name, _, _ := strings.Cut(goType, ".")

	return name
}

func stringValueForInventory(value any) string {
	if text, ok := value.(string); ok {
		return text
	}

	return ""
}

func anySliceForInventory(value any) []any {
	if values, ok := value.([]any); ok {
		return values
	}

	return nil
}

func fieldWireName(field *ast.Field) string {
	if field.Tag != nil {
		tag, err := strconv.Unquote(field.Tag.Value)
		if err == nil {
			name, _, _ := strings.Cut(reflect.StructTag(tag).Get("json"), ",")
			if name != "" && name != "-" {
				return name
			}
		}
	}

	if len(field.Names) > 0 {
		return field.Names[0].Name
	}

	return "embedded"
}

func assertWireInventoryGeneratorCommands(t *testing.T, root string) {
	t.Helper()

	makefilePath := filepath.Join(root, "Makefile")

	makefile, err := os.ReadFile(makefilePath) // #nosec G304 -- Makefile is under repositoryRoot.
	if err != nil {
		t.Fatal(err)
	}

	for _, command := range []string{
		"tools/restmodelsplit bundle",
		"oapi-codegen@v2.8.0 -config pkg/dependencymodels/rest/config.yaml api/openapi.yaml",
		"oapi-codegen@v2.8.0 -config pkg/dependencymodels/fcm/config.yaml api/external/fcm.openapi.yaml",
		"oapi-codegen@v2.8.0 -config pkg/ringapimodels/config.yaml api/client-models.openapi.yaml",
		"node generate_signaling.mjs",
		"node generate_mcs.mjs",
		"tools/restmodelsplit split",
	} {
		if !bytes.Contains(makefile, []byte(command)) {
			t.Errorf("Makefile generate-api does not contain expected generator command %q", command)
		}
	}
}
