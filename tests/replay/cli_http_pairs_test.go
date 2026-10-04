package replay_test

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/portpowered/go-ring/internal/testkit/replay"
)

const diagnosticReplayOrigin = "http://cli.synthetic.test"

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
		if request.Host != server.Listener.Addr().String() || request.URL.Scheme != "" || request.URL.Host != "" {
			t.Error("CLI request did not use the configured local HTTP origin")
			http.Error(writer, "origin mismatch", http.StatusBadRequest)

			return
		}

		clone := request.Clone(request.Context())
		clone.URL.Scheme = "http"
		clone.URL.Host = "cli.synthetic.test"

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

func writeDiagnosticPairResponse(t *testing.T, writer http.ResponseWriter, response *http.Response) {
	t.Helper()

	maps.Copy(writer.Header(), response.Header)

	writer.WriteHeader(response.StatusCode)

	_, err := io.Copy(writer, response.Body)
	if err != nil {
		t.Errorf("write paired response: %v", err)
	}
}
