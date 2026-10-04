package replay_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/internal/testkit/replay"
)

func diagnosticPairRequest(t *testing.T, pair replay.Exchange) *http.Request {
	t.Helper()

	var body string

	err := json.Unmarshal(pair.Request.Body, &body)
	if err != nil {
		t.Fatal(err)
	}

	query := url.Values{}
	for _, value := range pair.Request.Query {
		query.Add(value.Name, value.Value)
	}

	request, err := http.NewRequestWithContext(t.Context(), pair.Request.Method,
		pair.Request.Origin+pair.Request.Path+"?"+query.Encode(), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	request.Header = pair.Request.Headers.Clone()

	return request
}

func closeDiagnosticPairResponse(t *testing.T, response *http.Response) {
	t.Helper()

	if response == nil {
		return
	}

	err := response.Body.Close()
	if err != nil {
		t.Errorf("close replay response: %v", err)
	}
}

func TestDiagnosticCLIHTTPPairsRejectMismatch(t *testing.T) {
	t.Parallel()

	pairs := loadDiagnosticHTTPPairs(t, "cli-health-sound-reboot.json")

	for name, mutate := range map[string]func(*http.Request){
		"method":       func(request *http.Request) { request.Method = http.MethodGet },
		"origin":       func(request *http.Request) { request.URL.Host = "unexpected.synthetic.test" },
		"escaped-path": func(request *http.Request) { request.URL.Path = "/unexpected" },
		"query":        func(request *http.Request) { request.URL.RawQuery = "unexpected=one&unexpected=two" },
		"headers":      func(request *http.Request) { request.Header.Set("Authorization", "Bearer wrong-synthetic-token") },
		"body":         func(request *http.Request) { request.Body = http.NoBody },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			transport := replay.NewTransport(pairs[0])
			request := diagnosticPairRequest(t, pairs[0])
			mutate(request)
			response, err := transport.RoundTrip(request)
			closeDiagnosticPairResponse(t, response)

			if err == nil || response != nil {
				t.Fatal("changed request accepted")
			}
		})
	}
}

func TestDiagnosticCLIHTTPPairsRejectOutOfOrderAndDuplicate(t *testing.T) {
	t.Parallel()
	pairs := loadDiagnosticHTTPPairs(t, "cli-health-sound-reboot.json")
	transport := replay.NewTransport(pairs...)
	response, err := transport.RoundTrip(diagnosticPairRequest(t, pairs[1]))
	closeDiagnosticPairResponse(t, response)

	if err == nil || response != nil {
		t.Fatal("out-of-order request accepted")
	}

	transport = replay.NewTransport(pairs[0])
	response, err = transport.RoundTrip(diagnosticPairRequest(t, pairs[0]))
	closeDiagnosticPairResponse(t, response)

	if err != nil {
		t.Fatalf("expected request rejected: %v", err)
	}

	err = transport.AssertConsumed()
	if err != nil {
		t.Fatalf("expected request unconsumed: %v", err)
	}

	response, err = transport.RoundTrip(diagnosticPairRequest(t, pairs[0]))
	closeDiagnosticPairResponse(t, response)

	if err == nil || response != nil {
		t.Fatal("duplicate request accepted")
	}
}
