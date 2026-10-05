package routegate_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestGeneratedRequestRejectsBodyAndFramingMutations(t *testing.T) {
	t.Parallel()

	for _, mutation := range []string{
		`req.Body = nil`,
		`req.GetBody = nil`,
		`req.Host = "unregistered.example"`,
		`req.ContentLength = 1`,
		`req.TransferEncoding = []string{"chunked"}`,
		`req.Trailer = http.Header{"X-Unregistered": []string{"value"}}`,
		`req.URL.Fragment = "unregistered"`,
		`req.URL.RawFragment = "unregistered"`,
		`req.URL.OmitHost = true`,
	} {
		t.Run(mutation, func(t *testing.T) {
			t.Parallel()

			source := strings.Replace(generatedRequestSource, "req = req.WithContext(ctx)",
				"req = req.WithContext(ctx)\n\t"+mutation, 1)
			root := fixtureRoot(t, source)
			assertRequestFixtureCompiles(t, root)

			findings := audit(t, root)
			if !hasRule(findings, "generated-request-route-mutation") {
				t.Fatalf("request mutation accepted:\n%s", strings.Join(findings, "\n"))
			}
		})
	}
}

func TestGeneratedRequestRejectsURLUserInformation(t *testing.T) {
	t.Parallel()

	source := strings.Replace(generatedRequestSource, `"net/http"`, `"net/http"
	"net/url"`, 1)
	source = strings.Replace(source, "req = req.WithContext(ctx)",
		`req = req.WithContext(ctx)
	req.URL.User = url.UserPassword("synthetic-user", "synthetic-password")`, 1)
	root := fixtureRoot(t, source)
	assertRequestFixtureCompiles(t, root)

	if findings := audit(t, root); !hasRule(findings, "generated-request-route-mutation") {
		t.Fatalf("URL user information mutation accepted:\n%s", strings.Join(findings, "\n"))
	}
}

func TestGeneratedRequestAllowsOriginalBodyRestorationOnRetryClone(t *testing.T) {
	t.Parallel()

	source := strings.Replace(generatedRequestSource, "attemptReq := req", `attemptReq := req.Clone(ctx)
	if req.GetBody != nil {
		var err error
		attemptReq.Body, err = req.GetBody()
		if err != nil { return nil, err }
	}`, 1)
	root := fixtureRoot(t, source)
	assertRequestFixtureCompiles(t, root)

	if findings := audit(t, root); len(findings) != 0 {
		t.Fatalf("original generated body restoration rejected:\n%s", strings.Join(findings, "\n"))
	}
}

func assertRequestFixtureCompiles(t *testing.T, root string) {
	t.Helper()

	command := exec.CommandContext(context.Background(), "go", "test", "./...")
	command.Dir = root

	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("compile request fixture: %v\n%s", err, output)
	}
}
