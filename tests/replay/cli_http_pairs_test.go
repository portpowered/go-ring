package replay_test

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/internal/testkit/replay"
)

const (
	diagnosticReplayOrigin = "http://cli.synthetic.test"
	diagnosticReplayHost   = "cli.synthetic.test"
	unexpectedAuthority    = "unexpected.example"
)

type diagnosticHTTPPairs struct {
	Classification string            `json:"classification"`
	Exchanges      []replay.Exchange `json:"exchanges"`
}

func loadDiagnosticHTTPPairs(t *testing.T, name string) []replay.Exchange {
	t.Helper()

	path := filepath.Join("fixtures", "http", "synthetic", name)

	data, err := os.ReadFile(path) // #nosec G304 -- name is fixed by the synthetic CLI replay inventory.
	if err != nil {
		t.Fatal(err)
	}

	var pairs diagnosticHTTPPairs

	err = json.Unmarshal(data, &pairs)
	if err != nil {
		t.Fatal(err)
	}

	if pairs.Classification != "synthetic" || len(pairs.Exchanges) == 0 {
		t.Fatal("CLI pairs must have synthetic provenance")
	}

	for _, pair := range pairs.Exchanges {
		if pair.Request.Origin != diagnosticReplayOrigin || pair.Request.HeadersMode != replay.HeadersExact {
			t.Fatal("CLI pairs must use the explicit local origin mapping and exact headers")
		}
	}

	return pairs.Exchanges
}

func newDiagnosticCLIHTTPPairs(t *testing.T, name string) *httptest.Server {
	t.Helper()

	return newDiagnosticCLIHTTPPairsWithRules(t, name, nil, nil)
}

func newDiagnosticCLIHTTPPairsWithRules(
	t *testing.T, name string, normalize func(*http.Request) error, restore func(*http.Response),
) *httptest.Server {
	t.Helper()
	transport := replay.NewTransport(loadDiagnosticHTTPPairs(t, name)...)
	server := httptest.NewUnstartedServer(nil)
	server.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !isDiagnosticLocalRequest(request, server.Listener.Addr().String()) {
			t.Error("CLI request did not use the configured local HTTP origin and request target")
			http.Error(writer, "origin mismatch", http.StatusBadRequest)

			return
		}

		clone := request.Clone(request.Context())
		clone.URL.Scheme = "http"
		clone.URL.Host = diagnosticReplayHost
		clone.Host = diagnosticReplayHost
		clone.RequestURI = ""

		if normalize != nil {
			err := normalize(clone)
			if err != nil {
				t.Errorf("CLI volatile request rule: %v", err)
				http.Error(writer, "volatile field mismatch", http.StatusBadRequest)

				return
			}
		}

		response, err := transport.RoundTrip(clone)
		if err != nil {
			t.Errorf("CLI paired request mismatch: %v", err)
			http.Error(writer, "replay mismatch", http.StatusBadRequest)

			return
		}

		defer func() {
			err := response.Body.Close()
			if err != nil {
				t.Errorf("close paired response: %v", err)
			}
		}()

		if restore != nil {
			restore(response)
		}

		writeDiagnosticPairResponse(t, writer, response)
	})
	server.Start()
	t.Cleanup(func() {
		server.Close()

		err := transport.AssertConsumed()
		if err != nil {
			t.Errorf("CLI paired replay consumption: %v", err)
		}
	})

	return server
}

func TestDiagnosticCLIAdapterRejectsUnexpectedHostOverride(t *testing.T) {
	t.Parallel()

	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "/health", nil)
	if err != nil {
		t.Fatal(err)
	}

	request.Host = unexpectedAuthority
	if usesDiagnosticLocalAuthority(request, "127.0.0.1:43127") {
		t.Fatal("adapter accepted an unexpected request Host override")
	}
}

func usesDiagnosticLocalAuthority(request *http.Request, expectedAuthority string) bool {
	return request.Host == expectedAuthority && request.URL.Scheme == "" && request.URL.Host == ""
}

func isDiagnosticLocalRequest(request *http.Request, expectedAuthority string) bool {
	if request == nil || request.URL == nil || !usesDiagnosticLocalAuthority(request, expectedAuthority) ||
		request.RequestURI == "" || request.URL.User != nil || request.URL.Opaque != "" || request.URL.Fragment != "" {
		return false
	}

	if !validDiagnosticHTTPMethod(request.Method) {
		return false
	}

	requestTarget, err := url.ParseRequestURI(request.RequestURI)
	if err != nil || requestTarget.Scheme != "" || requestTarget.Host != "" || requestTarget.User != nil ||
		requestTarget.Opaque != "" || requestTarget.Fragment != "" || requestTarget.RequestURI() != request.RequestURI {
		return false
	}

	if request.URL.RequestURI() != request.RequestURI || requestTarget.EscapedPath() != request.URL.EscapedPath() ||
		requestTarget.RawQuery != request.URL.RawQuery || requestTarget.ForceQuery != request.URL.ForceQuery {
		return false
	}

	_, err = url.ParseQuery(requestTarget.RawQuery)

	return err == nil
}

func validDiagnosticHTTPMethod(method string) bool {
	if method == "" {
		return false
	}

	for _, character := range method {
		if character > 127 {
			return false
		}

		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", character) {
			continue
		}

		return false
	}

	return true
}

func TestDiagnosticCLIAdapterRequiresConsistentLocalRequest(t *testing.T) {
	t.Parallel()

	const expectedAuthority = "127.0.0.1:43127"

	newRequest := func() *http.Request {
		request := httptest.NewRequest(http.MethodGet, "/health?mode=full", nil)
		request.URL.Scheme = ""
		request.URL.Host = ""
		request.Host = expectedAuthority

		return request
	}

	if !isDiagnosticLocalRequest(newRequest(), expectedAuthority) {
		t.Fatal("adapter rejected a consistent local origin-form request")
	}

	for name, mutate := range map[string]func(*http.Request){
		"invalid method":          func(request *http.Request) { request.Method = "GET /" },
		"effective authority":     func(request *http.Request) { request.Host = unexpectedAuthority },
		"path and request target": func(request *http.Request) { request.URL.Path = "/other" },
		"request target":          func(request *http.Request) { request.RequestURI = "/other?mode=full" },
		"malformed query": func(request *http.Request) {
			request.URL.RawQuery = "mode=%ZZ"
			request.RequestURI = request.URL.RequestURI()
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			request := newRequest().Clone(context.Background())
			mutate(request)

			if isDiagnosticLocalRequest(request, expectedAuthority) {
				t.Fatal("adapter accepted an inconsistent local request")
			}
		})
	}
}

func writeDiagnosticPairResponse(t *testing.T, writer http.ResponseWriter, response *http.Response) {
	t.Helper()

	maps.Copy(writer.Header(), response.Header)

	writer.WriteHeader(response.StatusCode)

	_, err := io.Copy(writer, response.Body)
	if err != nil {
		t.Errorf("write paired response: %v", err)
	}
}
