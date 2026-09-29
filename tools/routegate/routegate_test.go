package routegate_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/tools/routegate"
)

const generatedRequestSource = `package sample

import (
	"context"
	"net/http"
	routes "example.com/routegatefixture/generatedhttp"
)

type API struct { httpClient *http.Client }

func (c *API) GetWidget(ctx context.Context, widgetID string) error {
	req, err := routes.NewGetWidgetRequest("https://api.example.test", widgetID)
	if err != nil { return err }
	req = req.WithContext(ctx)
	return c.doGeneratedJSON(ctx, req)
}

func (c *API) doGeneratedJSON(ctx context.Context, req *http.Request) error {
	return c.sendAuthorizedRequest(ctx, req)
}

func (c *API) sendAuthorizedRequest(ctx context.Context, req *http.Request) error {
	req = req.WithContext(ctx)
	_, err := c.sendWithRetry(ctx, req)
	return err
}

func (c *API) sendWithRetry(ctx context.Context, req *http.Request) (*http.Response, error) {
	attemptReq := req
	return c.httpClient.Do(attemptReq)
}
`

func TestGeneratedRequestTransportAcceptsWithContextAndVerifiedClient(t *testing.T) {
	t.Parallel()

	root := fixtureRoot(t, generatedRequestSource)

	findings := audit(t, root)
	if len(findings) != 0 {
		t.Fatalf("valid generated request flow produced findings:\n%s", strings.Join(findings, "\n"))
	}
}

func TestHandwrittenRequestAndDynamicHelperAreRejected(t *testing.T) {
	t.Parallel()

	source := strings.Replace(generatedRequestSource,
		`req, err := routes.NewGetWidgetRequest("https://api.example.test", widgetID)`,
		`req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.example.test/widgets/"+widgetID, nil)`,
		1,
	)
	source = strings.Replace(source, `return c.doGeneratedJSON(ctx, req)`,
		`return c.doJSONRequest(ctx, http.MethodGet, "/widgets/"+widgetID, nil, nil)`, 1)

	findings := audit(t, fixtureRoot(t, source))
	for _, rule := range []string{"handwritten-http-route", "unbound-generic-http-helper"} {
		if !hasRule(findings, rule) {
			t.Errorf("expected %s finding, got:\n%s", rule, strings.Join(findings, "\n"))
		}
	}
}

func TestGeneratedRequestMutationAndClientSubstitutionAreRejected(t *testing.T) {
	t.Parallel()

	source := strings.Replace(generatedRequestSource,
		`req = req.WithContext(ctx)`,
		`req = req.WithContext(ctx)
	req.URL.Path = "/unmodeled"`, 1)
	source = strings.Replace(source, `return c.doGeneratedJSON(ctx, req)`,
		`other := &http.Client{}
	_, err = other.Do(req)
	return err`, 1)

	findings := audit(t, fixtureRoot(t, source))
	for _, rule := range []string{"generated-request-route-mutation", "unverified-http-client-receiver"} {
		if !hasRule(findings, rule) {
			t.Errorf("expected %s finding, got:\n%s", rule, strings.Join(findings, "\n"))
		}
	}
}

func TestGeneratedBuilderMustResolveToDiscoveredPackage(t *testing.T) {
	t.Parallel()

	source := strings.Replace(generatedRequestSource,
		`routes "example.com/routegatefixture/generatedhttp"`,
		`routes "example.com/untrusted/generatedhttp"`, 1)

	findings := audit(t, fixtureRoot(t, source))
	if !hasRule(findings, "unverified-generated-request-builder") {
		t.Fatalf("expected import binding finding, got:\n%s", strings.Join(findings, "\n"))
	}
}

func TestRequestCannotEscapeToUnverifiedHelper(t *testing.T) {
	t.Parallel()

	source := strings.Replace(generatedRequestSource, `return c.doGeneratedJSON(ctx, req)`,
		`return c.forwardToUnknown(ctx, req)`, 1)

	findings := audit(t, fixtureRoot(t, source))
	if !hasRule(findings, "generated-request-escape") {
		t.Fatalf("expected request escape finding, got:\n%s", strings.Join(findings, "\n"))
	}
}

func TestGeneratedRequestCannotEscapeThroughUnverifiedTransportMutation(t *testing.T) {
	t.Parallel()

	source := strings.Replace(generatedRequestSource,
		`attemptReq := req`,
		`attemptReq := req
	attemptReq.URL.Path = "/unmodeled"`, 1)

	findings := audit(t, fixtureRoot(t, source))
	if !hasRule(findings, "handwritten-http-send") {
		t.Fatalf("expected unverified transport send finding, got:\n%s", strings.Join(findings, "\n"))
	}
}

func TestProductionRepositoryHasNoRouteCoverageGaps(t *testing.T) {
	t.Parallel()

	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	root = filepath.Clean(filepath.Join(root, "..", ".."))

	findings, err := routegate.Audit(root)
	if err != nil {
		t.Fatal(err)
	}

	if len(findings) != 0 {
		messages := make([]string, 0, len(findings))
		for _, finding := range findings {
			messages = append(messages, finding.String())
		}

		t.Fatalf("production route coverage has gaps:\n%s", strings.Join(messages, "\n"))
	}
}

func fixtureRoot(t *testing.T, source string) string {
	t.Helper()
	root := t.TempDir()
	write := func(name, content string) {
		t.Helper()

		path := filepath.Join(root, filepath.FromSlash(name))

		err := os.MkdirAll(filepath.Dir(path), 0o700)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(path, []byte(content), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	write("go.mod", "module example.com/routegatefixture\n\ngo 1.24.0\n")
	write("api/openapi.yaml", `openapi: 3.1.0
info: {title: routegate fixture, version: 0.1.0}
servers: [{url: https://api.example.test}]
paths:
  /widgets/{widget_id}:
    get:
      operationId: getWidget
      parameters:
        - {name: widget_id, in: path, required: true, schema: {type: string}}
      responses: {'200': {description: ok}}
`)
	write("api/asyncapi.yaml", `asyncapi: 2.6.0
info: {title: routegate fixture, version: 0.1.0}
servers: {}
channels: {}
operations: {}
components:
  messages: {}
  schemas: {}
`)
	write("internal/protocol/endpoints.go", "package protocol\n")
	write("generatedhttp/client.gen.go", `// Code generated by fixture. DO NOT EDIT.
package generatedhttp

import "net/http"

// NewGetWidgetRequest constructs the generated request.
func NewGetWidgetRequest(server string, widgetID string) (*http.Request, error) {
	return http.NewRequest(http.MethodGet, server+"/widgets/"+widgetID, nil)
}

// Corresponds with GET /widgets/{widget_id} (the `+"`getWidget`"+` operationId)
func (c *Client) GetWidget() {}

type Client struct{}
`)
	write("client.go", source)

	return root
}

func audit(t *testing.T, root string) []string {
	t.Helper()

	findings, err := routegate.Audit(root)
	if err != nil {
		t.Fatal(err)
	}

	result := make([]string, 0, len(findings))
	for _, finding := range findings {
		result = append(result, finding.String())
	}

	return result
}

func hasRule(findings []string, rule string) bool {
	for _, finding := range findings {
		if strings.Contains(finding, ": "+rule+":") {
			return true
		}
	}

	return false
}
