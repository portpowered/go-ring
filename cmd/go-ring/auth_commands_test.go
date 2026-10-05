package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

type cliRequestExpectation struct {
	Method  string            `json:"method"`
	Origin  string            `json:"origin"`
	Path    string            `json:"path"`
	Query   string            `json:"query"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

type cliResponseExpectation struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

type cliExchange struct {
	Classification string                 `json:"classification"`
	Request        cliRequestExpectation  `json:"request"`
	Response       cliResponseExpectation `json:"response"`
}

const cliSyntheticClassification = "synthetic"

type cliTransport struct {
	exchange cliExchange
	calls    int
	closed   bool
}

func (transport *cliTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.calls++
	if transport.calls != 1 {
		return nil, commandError("unexpected replay request")
	}

	expected := transport.exchange.Request
	if request.Method != expected.Method || request.URL.Scheme+"://"+request.URL.Host != expected.Origin ||
		request.URL.EscapedPath() != expected.Path || request.URL.RawQuery != expected.Query {
		return nil, commandError("replay request route mismatch")
	}

	for key, value := range expected.Headers {
		if request.Header.Get(key) != value {
			return nil, commandError("replay request header mismatch")
		}
	}

	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, wrapCommandError("read replay request", err)
	}

	if string(body) != expected.Body {
		return nil, commandError("replay request body mismatch")
	}

	response := transport.exchange.Response
	headers := make(http.Header)

	for key, value := range response.Headers {
		headers.Set(key, value)
	}

	return &http.Response{
		StatusCode: response.Status, Header: headers, Request: request,
		Body: io.NopCloser(strings.NewReader(response.Body)),
	}, nil
}

func (transport *cliTransport) CloseIdleConnections() {
	transport.closed = true
}

func replayTokenStore(t *testing.T) (tokenStore, *cliTransport) {
	t.Helper()

	data, err := os.ReadFile("../../tests/replay/fixtures/http/synthetic/cli-refresh.json")
	if err != nil {
		t.Fatal(err)
	}

	var exchange cliExchange

	err = json.Unmarshal(data, &exchange)
	if err != nil {
		t.Fatal(err)
	}

	if exchange.Classification != cliSyntheticClassification {
		t.Fatal("CLI replay must remain synthetic")
	}

	transport := &cliTransport{exchange: exchange, calls: 0, closed: false}
	store := tokenStore{
		path: filepath.Join(t.TempDir(), "tokens.json"),
		clientOptions: []ring.Option{
			ring.WithHTTPClient(&http.Client{Transport: transport}),
			ring.WithEndpoints(ring.Endpoints{
				APIBaseURL: "", OAuthBaseURL: exchange.Request.Origin, SolutionsBaseURL: "", SignalingURL: "",
			}),
		},
	}

	err = store.save(storedTokens{
		AuthResponse: ringapimodels.AuthResponse{
			AccessToken: "synthetic-old-access", RefreshToken: "synthetic-refresh", ExpiresIn: 0, TokenType: "Bearer",
		},
		HardwareID: "synthetic-hardware", ReceivedAt: time.Now().Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	return store, transport
}

func TestExplicitRefreshPersistsTokensWithoutPrintingSecrets(t *testing.T) {
	t.Parallel()

	store, transport := replayTokenStore(t)

	var output bytes.Buffer

	err := authCommand(context.Background(), store, []string{"refresh"}, strings.NewReader(""), &output)
	if err != nil {
		t.Fatal(err)
	}

	if transport.calls != 1 {
		t.Fatalf("consumed %d exchanges, want 1", transport.calls)
	}

	tokens, err := store.load()
	if err != nil {
		t.Fatal(err)
	}

	if tokens.AccessToken != "synthetic-new-access" || tokens.RefreshToken != "synthetic-new-refresh" {
		t.Fatal("explicit refresh did not persist current credentials")
	}

	if strings.Contains(output.String(), tokens.AccessToken) || strings.Contains(output.String(), tokens.RefreshToken) {
		t.Fatal("refresh leaked credentials")
	}

	if !json.Valid(output.Bytes()) {
		t.Fatal("refresh status is not JSON")
	}
}

func TestExpiredReadDoesNotRefreshOrInvokeAction(t *testing.T) {
	t.Parallel()

	store, transport := replayTokenStore(t)
	called := false

	err := withClient(context.Background(), store, func(*ring.Client, ring.AuthContext) error {
		called = true

		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "auth refresh") || called || transport.calls != 0 {
		t.Fatal("expired read must fail with an explicit refresh instruction without network activity")
	}
}

func TestRefreshFailurePreservesCredentials(t *testing.T) {
	t.Parallel()

	store, transport := replayTokenStore(t)
	transport.exchange.Response.Status = http.StatusUnauthorized
	transport.exchange.Response.Body = `{"error":"invalid_grant"}`

	var output bytes.Buffer

	err := refreshLogin(context.Background(), store, &output)
	if err == nil || transport.calls != 1 {
		t.Fatal("authentication failure was not returned after consuming its request")
	}

	tokens, err := store.load()
	if err != nil {
		t.Fatal(err)
	}

	if tokens.AccessToken != "synthetic-old-access" || tokens.RefreshToken != "synthetic-refresh" || output.Len() != 0 {
		t.Fatal("failed refresh changed credentials or wrote ordinary output")
	}
}

func TestHelpAndExplicitCredentialExport(t *testing.T) {
	t.Parallel()

	store, _ := replayTokenStore(t)

	var output bytes.Buffer

	err := authCommand(context.Background(), store, []string{"export"}, strings.NewReader(""), &output)
	if err != nil {
		t.Fatal(err)
	}

	if !json.Valid(output.Bytes()) || !strings.Contains(output.String(), "synthetic-refresh") {
		t.Fatal("explicit credential export is incomplete")
	}

	for _, argument := range []string{"help", "-h", "--help"} {
		output.Reset()

		err = run(context.Background(), []string{argument}, strings.NewReader(""), &output)
		if err != nil || !strings.Contains(output.String(), "auth login|status|refresh|export|logout") {
			t.Fatalf("%s did not provide successful help: %v", argument, err)
		}
	}
}
