package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

const (
	testOpenAPIPath       = "api/openapi.yaml"
	sharedDocsURL         = "https://provider.example/docs"
	getZetaOperation      = "GET /zeta"
	supportedReviewStatus = "supported"
)

var errTestCause testCauseError

type testCauseError struct{}

func (testCauseError) Error() string {
	return "underlying cause"
}

func TestCollectsExternalDocsAndReferencedDescriptionURLs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	openapiPath := writeDescriptionReferenceFixture(t, root)
	links, err := collectSchemaLinks(root, []string{openapiPath})
	requireNoError(t, err)

	providerURL := "https://developer.tuya.com/en/docs/cloud/device-management?id=K9g6rfntdz78a"
	provider := findLink(t, links, providerURL, linkKindExternalDocs)

	internal := findLink(t, links, "https://portpowered.github.io/go-tuya/docs/guides/tuya/", linkKindDescription)
	if internal.Source != filepath.ToSlash(filepath.Join("schemas", "shared.yaml")) {
		t.Fatalf("referenced URL owner = %q, want schemas/shared.yaml", internal.Source)
	}

	refPointer := "api/openapi.yaml#/paths/~1example/get/responses/200/content/application~1json/schema/$ref"
	if !slices.Contains(internal.ReferencedFrom, refPointer) {
		t.Fatalf("local reference owner not recorded: %#v", internal.ReferencedFrom)
	}

	if len(provider.Operations) != 1 || provider.Operations[0] != "GET /example" {
		t.Fatalf("externalDocs operation = %#v, want GET /example", provider.Operations)
	}
}

func TestCollectsOpenAPIVariableTemplatesWithoutTruncatingBraces(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	openAPI := `openapi: 3.1.0
servers:
  - url: https://api.{domain}
paths:
  /example:
    get:
      description: The client sends requests to https://api.{domain}.
      responses:
        "200":
          description: OK
`
	openAPIPath := filepath.Join(root, "openapi.yaml")
	writeTestFile(t, openAPIPath, []byte(openAPI))

	links, err := collectSchemaLinks(root, []string{openAPIPath})
	requireNoError(t, err)

	server := findLink(t, links, "https://api.{domain}", linkKindURL)
	if server.Pointer != "#/servers/0/url" {
		t.Fatalf("server template pointer = %q, want #/servers/0/url", server.Pointer)
	}

	assertURLTemplate(t, *server)

	description := findLink(t, links, "https://api.{domain}", linkKindDescription)
	if description.Pointer != "#/paths/~1example/get/description" {
		t.Fatalf("description template pointer = %q, want operation description", description.Pointer)
	}

	assertURLTemplate(t, *description)
}

func assertURLTemplate(t *testing.T, link schemaLink) {
	t.Helper()

	status, err := checkDestination(link, t.TempDir(), "/docs")
	if err != nil || status != checkTemplate {
		t.Fatalf("valid OpenAPI URL template = (%q, %v), want template", status, err)
	}
}

func writeDescriptionReferenceFixture(t *testing.T, root string) string {
	t.Helper()

	apiDir := filepath.Join(root, "api")
	sharedDir := filepath.Join(root, "schemas")

	requireNoError(t, os.MkdirAll(apiDir, 0o700))
	requireNoError(t, os.MkdirAll(sharedDir, 0o700))

	openapi := `openapi: 3.1.0
paths:
  /example:
    get:
      externalDocs:
        url: https://developer.tuya.com/en/docs/cloud/device-management?id=K9g6rfntdz78a
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                $ref: ../schemas/shared.yaml#/components/schemas/Linked
`
	shared := `components:
  schemas:
    Linked:
      type: object
      description: See https://portpowered.github.io/go-tuya/docs/guides/tuya/.
`
	openapiPath := filepath.Join(apiDir, "openapi.yaml")
	writeTestFile(t, openapiPath, []byte(openapi))
	writeTestFile(t, filepath.Join(sharedDir, "shared.yaml"), []byte(shared))

	return openapiPath
}

func TestCollectsGraphQLURLsWithSourceAndLine(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	requireNoError(t, os.MkdirAll(filepath.Join(root, "pkg", "schemas"), 0o700))
	requireNoError(t, os.MkdirAll(filepath.Join(root, "docs"), 0o700))
	graphqlPath := filepath.Join(root, "pkg", "schemas", "endpoints-schema.graphql")
	graphql := "type Query {\n  # Find API details at https://provider.example/graphql\n  status: String\n}\n"
	bindingsPath := filepath.Join(root, "docs", "schema-bindings.yaml")
	bindings := "bindings:\n  description: See https://provider.example/binding\n"

	writeTestFile(t, graphqlPath, []byte(graphql))
	writeTestFile(t, bindingsPath, []byte(bindings))

	links, err := collectSchemaLinks(root, []string{graphqlPath, bindingsPath})
	requireNoError(t, err)

	graphqlLink := findLink(t, links, "https://provider.example/graphql", linkKindURL)
	if graphqlLink.Source != filepath.ToSlash(filepath.Join("pkg", "schemas", "endpoints-schema.graphql")) ||
		graphqlLink.Line != 2 || graphqlLink.Pointer != "#/line/2" {
		t.Fatalf("GraphQL link location = %#v, want source line 2", graphqlLink)
	}

	bindingLink := findLink(t, links, "https://provider.example/binding", linkKindDescription)
	if bindingLink.Source != filepath.ToSlash(filepath.Join("docs", "schema-bindings.yaml")) {
		t.Fatalf("YAML binding source = %q, want docs/schema-bindings.yaml", bindingLink.Source)
	}
}

func TestSharedExternalDocsRetainAndRequireEveryOperationReview(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	schemaInputs := writeMultiHopExternalDocsFixture(t, root)
	links, err := collectSchemaLinks(root, schemaInputs)
	requireNoError(t, err)
	link := findLink(t, links, sharedDocsURL, linkKindExternalDocs)
	wantOperations := []string{getZetaOperation, "POST /alpha"}
	assertMultiHopOperationProvenance(t, link, wantOperations)

	reviews := completeSharedDocsReview()
	assertAllOperationReviewsRequired(t, link, wantOperations, reviews)
}

func assertMultiHopOperationProvenance(t *testing.T, link *schemaLink, wantOperations []string) {
	t.Helper()

	if !slices.Equal(link.Operations, wantOperations) {
		t.Fatalf("shared externalDocs operations = %#v, want %#v", link.Operations, wantOperations)
	}

	if len(link.ReferencedFrom) < 3 {
		t.Fatalf("multi-hop reference provenance = %#v, want both hops for two operations", link.ReferencedFrom)
	}

	for _, operation := range wantOperations {
		found := slices.ContainsFunc(
			link.ReferencedFrom,
			func(pointer string) bool { return operationIdentity(pointer) == operation },
		)
		if !found {
			t.Errorf("reference provenance does not identify %s: %#v", operation, link.ReferencedFrom)
		}
	}
}

func assertAllOperationReviewsRequired(
	t *testing.T,
	link *schemaLink,
	wantOperations []string,
	reviews map[string]reviewEntry,
) {
	t.Helper()

	evidence := findEvidence(reviews, *link)
	if evidence == nil || len(evidence.Operations) != 2 {
		t.Fatalf("review evidence for all operations = %#v, want two entries", evidence)
	}

	if evidence.Operations[0].Name != wantOperations[0] || evidence.Operations[1].Name != wantOperations[1] {
		t.Fatalf("review evidence operation order = %#v, want %#v", evidence.Operations, wantOperations)
	}

	review := reviews[link.URL]
	delete(review.Operations, "POST /alpha")

	reviews[link.URL] = review
	if findEvidence(reviews, *link) != nil {
		t.Fatal("shared externalDocs passed without evidence for every owning operation")
	}

	_, failure := evaluateLink(*link, t.TempDir(), "/site", reviews)
	if !strings.Contains(failure, getZetaOperation) && !strings.Contains(failure, "POST /alpha") {
		t.Fatalf("unreviewed shared externalDocs failure = %q, want an owning operation", failure)
	}
}

func writeMultiHopExternalDocsFixture(t *testing.T, root string) []string {
	t.Helper()

	for _, directory := range []string{"api", "schemas"} {
		requireNoError(t, os.MkdirAll(filepath.Join(root, directory), 0o700))
	}

	openapi := `openapi: 3.1.0
paths:
  /zeta:
    get:
      responses:
        "200":
          $ref: ../schemas/responses.yaml#/components/responses/Shared
  /alpha:
    post:
      responses:
        "200":
          $ref: ../schemas/responses.yaml#/components/responses/Shared
`
	response := `components:
  responses:
    Shared:
      description: Shared response
      content:
        application/json:
          schema:
            $ref: payload.yaml#/components/schemas/Linked
`
	payload := `components:
  schemas:
    Linked:
      type: object
      externalDocs:
        url: https://provider.example/docs
`

	writeTestFile(t, filepath.Join(root, testOpenAPIPath), []byte(openapi))
	writeTestFile(t, filepath.Join(root, "schemas", "responses.yaml"), []byte(response))
	writeTestFile(t, filepath.Join(root, "schemas", "payload.yaml"), []byte(payload))

	return []string{filepath.Join(root, testOpenAPIPath)}
}

func completeSharedDocsReview() map[string]reviewEntry {
	return map[string]reviewEntry{
		sharedDocsURL: {
			Publisher:   "Provider",
			Title:       "API reference",
			ReviewedOn:  "2026-10-04",
			Source:      sharedDocsURL,
			Assessment:  "Reviewed page content.",
			Limitations: "No private routes verified.",
			Operations: map[string]operationReview{
				getZetaOperation: {Status: supportedReviewStatus, Evidence: "GET operation is listed."},
				"POST /alpha":    {Status: supportedReviewStatus, Evidence: "POST operation is listed."},
			},
		},
	}
}

func TestEveryExternalDocsLinkHasReviewedOperationEvidence(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}

	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	schemaInputs := existingPublishedSchemaInputs(repoRoot)
	links, err := collectSchemaLinks(repoRoot, schemaInputs)
	requireNoError(t, err)
	reviews, err := loadReviews(filepath.Join(repoRoot, "tools", "schemalinks", "reviews.json"))
	requireNoError(t, err)

	for _, link := range links {
		if link.Kind != linkKindExternalDocs {
			continue
		}

		if len(link.Operations) == 0 {
			t.Errorf("externalDocs URL %s at %s:%d has no owning operation", link.URL, link.Source, link.Line)

			continue
		}

		if findEvidence(reviews, link) == nil {
			t.Errorf("externalDocs URL %s for %v has no reviewed operation evidence", link.URL, link.Operations)
		}
	}
}

func existingPublishedSchemaInputs(repoRoot string) []string {
	relativePaths := []string{
		testOpenAPIPath,
		"api/behaviors.yaml",
		"api/feature-controls.yaml",
		"api/feature-events.yaml",
		"api/asyncapi.yaml",
		"api/mqtt.asyncapi.yaml",
		"api/external/fcm.openapi.yaml",
		"pkg/schemas/graphql/endpoints-schema.graphql",
		"docs/schema-bindings.yaml",
	}

	inputs := make([]string, 0, len(relativePaths))

	for _, relativePath := range relativePaths {
		path := filepath.Join(repoRoot, relativePath)

		_, err := os.Stat(path)
		if err == nil {
			inputs = append(inputs, path)
		}
	}

	return inputs
}

func TestBrokenRuntimeOnlyInternalSchemaLinkFails(t *testing.T) {
	t.Parallel()
	site := t.TempDir()
	requireNoError(t, os.MkdirAll(filepath.Join(site, "docs", "guides"), 0o700))

	link := testSchemaLink("https://portpowered.github.io/go-tuya/docs/guides/runtime-only/", linkKindDescription, "")

	status, err := checkDestination(link, site, "/go-tuya")
	if status != checkInternal || err == nil {
		t.Fatalf("missing runtime-only link = (%q, %v), want internal failure", status, err)
	}

	requireNoError(t, os.MkdirAll(filepath.Join(site, "docs", "guides", "runtime-only"), 0o700))
	writeTestFile(t, filepath.Join(site, "docs", "guides", "runtime-only", "index.html"), []byte("<h1>Guide</h1>"))

	status, err = checkDestination(link, site, "/go-tuya")
	if status != checkInternal || err != nil {
		t.Fatalf("existing runtime-only link = (%q, %v), want success", status, err)
	}
}

func TestServerVariableTemplatesAreRetained(t *testing.T) {
	t.Parallel()

	serverTemplate := testSchemaLink("https://api.{domain}", linkKindURL, "#/servers/0/url")
	assertURLTemplate(t, serverTemplate)

	descriptionTemplate := testSchemaLink("https://api.{domain}", linkKindDescription, "#/paths/~1example/get/description")
	assertURLTemplate(t, descriptionTemplate)
}

func TestMalformedURLTemplatesAreRejected(t *testing.T) {
	t.Parallel()

	links := []schemaLink{
		testSchemaLink("https://api.{domain}", linkKindExternalDocs, "#/paths/~1example/get/externalDocs/url"),
		testSchemaLink("https://api.{domain", linkKindURL, "#/servers/0/url"),
		testSchemaLink("https://api.{bad-name}", linkKindURL, "#/servers/0/url"),
	}

	for _, link := range links {
		status, err := checkDestination(link, t.TempDir(), "/docs")
		if status != checkExternal || err == nil {
			t.Errorf("malformed URL template %q = (%q, %v), want failure", link.URL, status, err)
		}
	}
}

func TestContextualErrorsPreserveTheirCause(t *testing.T) {
	t.Parallel()

	err := withErrorContext(errTestCause, "schema context %s", "entry")

	if !errors.Is(err, errTestCause) {
		t.Fatalf("wrapped error does not preserve its cause: %v", err)
	}

	if got, want := err.Error(), "schema context entry: underlying cause"; got != want {
		t.Fatalf("wrapped error = %q, want %q", got, want)
	}
}

func TestRunRejectsBrokenRuntimeOnlySchemaReference(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, directory := range []string{"api", "site"} {
		requireNoError(t, os.MkdirAll(filepath.Join(root, directory), 0o700))
	}

	writeTestFile(t, filepath.Join(root, "site", "index.html"), []byte("<html></html>"))

	linkURL := "https://portpowered.github.io/go-test/docs/guides/runtime-only/"
	schema := "openapi: 3.1.0\ninfo:\n  title: test\n  version: '1'\n  description: See " + linkURL + "\npaths: {}\n"
	writeTestFile(t, filepath.Join(root, testOpenAPIPath), []byte(schema))

	err := runFrom(root, "site", testOpenAPIPath, "/go-test", "site/schema-links.json", "tools/schemalinks/reviews.json")
	if err == nil ||
		!strings.Contains(err.Error(), "broken internal schema links") ||
		!strings.Contains(err.Error(), linkURL) {
		t.Fatalf("run error = %v, want broken runtime-only schema link", err)
	}

	manifestPath := filepath.Join(root, "site", "schema-links.json")
	manifestBytes, err := os.ReadFile(manifestPath) // #nosec G304 -- manifestPath is inside this test's temporary root.
	requireNoError(t, err)

	var output manifest

	err = json.Unmarshal(manifestBytes, &output)
	requireNoError(t, err)

	if len(output.Links) != 1 || output.Links[0].URL != linkURL || output.Links[0].Check != checkInternal {
		t.Fatalf("manifest link = %#v, want the runtime-only internal schema URL", output.Links)
	}
}

func findLink(t *testing.T, links []schemaLink, url, kind string) *schemaLink {
	t.Helper()

	for index := range links {
		if links[index].URL == url && links[index].Kind == kind {
			return &links[index]
		}
	}

	t.Fatalf("link %s (%s) was not collected: %#v", url, kind, links)

	return nil
}

func testSchemaLink(url, kind, pointer string) schemaLink {
	return schemaLink{
		URL:            url,
		Kind:           kind,
		Source:         "",
		Pointer:        pointer,
		Operation:      "",
		Operations:     nil,
		Line:           0,
		ReferencedFrom: nil,
	}
}

func writeTestFile(t *testing.T, path string, contents []byte) {
	t.Helper()

	err := os.WriteFile(path, contents, 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}
