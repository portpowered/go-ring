package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

type cliOAuthReplayFixture struct {
	Classification string            `json:"classification"`
	VolatileRules  map[string]string `json:"volatile_rules"`
	Exchanges      []replay.Exchange `json:"exchanges"`
}

type cliOAuthBindings struct {
	state     string
	challenge string
	hardware  string
}

const cliOAuthReplayHost = "cli.synthetic.test"

func TestLoginAndExplicitRefreshConsumePairedOAuthTranscript(t *testing.T) {
	t.Setenv("RING_PASSWORD", "")
	t.Setenv("RING_OTP_CODE", "")

	server, transport := newCLIOAuthReplayServer(t, "cli-login-refresh.json")
	store := newOAuthReplayTokenStore(t, server)
	input := strings.NewReader("replay@example.com\nreplay-password\n123456\n")

	var loginOutput bytes.Buffer

	err := login(context.Background(), store, input, &loginOutput)
	if err != nil {
		t.Fatal(err)
	}

	tokens, err := store.load()
	if err != nil {
		t.Fatal(err)
	}

	if tokens.AccessToken != "rotated-access" || tokens.RefreshToken != "rotated-refresh" {
		t.Fatalf(
			"login saved tokens = (%q, %q), want the post-authorization rotation",
			tokens.AccessToken,
			tokens.RefreshToken,
		)
	}

	secrets := []string{
		"initial-access", "initial-refresh", "rotated-access", "rotated-refresh", "replay-password", "123456",
	}
	for _, secret := range secrets {
		if strings.Contains(loginOutput.String(), secret) {
			t.Fatalf("login output exposed %q", secret)
		}
	}

	var refreshOutput bytes.Buffer

	err = refreshLogin(context.Background(), store, &refreshOutput)
	if err != nil {
		t.Fatal(err)
	}

	tokens, err = store.load()
	if err != nil {
		t.Fatal(err)
	}

	if tokens.AccessToken != "second-access" || tokens.RefreshToken != "second-refresh" {
		t.Fatalf(
			"explicit refresh saved tokens = (%q, %q), want the paired refresh response",
			tokens.AccessToken,
			tokens.RefreshToken,
		)
	}

	for _, secret := range []string{"second-access", "second-refresh", "replay-password", "123456"} {
		if strings.Contains(refreshOutput.String(), secret) {
			t.Fatalf("refresh output exposed %q", secret)
		}
	}

	err = transport.AssertConsumed()
	if err != nil {
		t.Fatal(err)
	}
}

func TestLoginAuthenticationFailureConsumesPairedErrorResponse(t *testing.T) {
	t.Setenv("RING_PASSWORD", "")
	t.Setenv("RING_OTP_CODE", "")

	server, transport := newCLIOAuthReplayServer(t, "cli-login-auth-failure.json")
	store := newOAuthReplayTokenStore(t, server)

	var output bytes.Buffer

	err := login(context.Background(), store, strings.NewReader("replay@example.com\nwrong-password\n"), &output)
	if !ringapimodels.IsAuthenticationError(err) {
		t.Fatalf("login error = %v, want a typed authentication failure", err)
	}

	if strings.Contains(output.String(), "wrong-password") || strings.Contains(output.String(), "invalid_grant") {
		t.Fatalf("failed login output exposed credentials or response details: %q", output.String())
	}

	_, statErr := os.Stat(store.path)
	if !os.IsNotExist(statErr) {
		t.Fatalf("failed login left token file behind: %v", statErr)
	}

	err = transport.AssertConsumed()
	if err != nil {
		t.Fatal(err)
	}
}

func newCLIOAuthReplayServer(t *testing.T, fixtureName string) (*httptest.Server, *replay.Transport) {
	t.Helper()

	path := filepath.Join("..", "..", "tests", "replay", "fixtures", "http", "synthetic", fixtureName)

	data, err := os.ReadFile(path) // #nosec G304 -- fixtureName is a fixed synthetic CLI fixture.
	if err != nil {
		t.Fatal(err)
	}

	var fixture cliOAuthReplayFixture

	err = json.Unmarshal(data, &fixture)
	if err != nil {
		t.Fatal(err)
	}

	if fixture.Classification != cliSyntheticClassification || len(fixture.Exchanges) == 0 {
		t.Fatal("CLI OAuth replay must have synthetic provenance and paired exchanges")
	}

	for _, exchange := range fixture.Exchanges {
		if exchange.Request.Origin != "http://cli.synthetic.test" || exchange.Request.HeadersMode != replay.HeadersExact {
			t.Fatal("CLI OAuth replay must use the synthetic origin and exact request headers")
		}
	}

	transport := replay.NewTransport(fixture.Exchanges...)
	bindings := &cliOAuthBindings{state: "", challenge: "", hardware: ""}
	server := httptest.NewUnstartedServer(nil)
	server.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !isCLIOAuthLocalRequest(request, server.Listener.Addr().String()) {
			t.Errorf("OAuth request did not use the configured local server: %s", request.URL)
			http.Error(writer, "origin mismatch", http.StatusBadRequest)

			return
		}

		clone := request.Clone(request.Context())
		clone.RequestURI = ""
		clone.URL.Scheme = "http"
		clone.URL.Host = cliOAuthReplayHost
		clone.Host = cliOAuthReplayHost

		err := bindings.normalize(clone)
		if err != nil {
			t.Errorf("OAuth replay field binding: %v", err)
			http.Error(writer, "volatile field mismatch", http.StatusBadRequest)

			return
		}

		response, replayErr := transport.RoundTrip(clone)
		if replayErr != nil {
			t.Errorf("OAuth paired request mismatch: %v", replayErr)
			http.Error(writer, "replay mismatch", http.StatusBadRequest)

			return
		}

		defer func() { _ = response.Body.Close() }()

		if location := response.Header.Get("Location"); location != "" {
			response.Header.Set("Location", strings.ReplaceAll(location, "$state", bindings.state))
		}

		for key, values := range response.Header {
			for _, value := range values {
				writer.Header().Add(key, value)
			}
		}

		writer.WriteHeader(response.StatusCode)

		_, copyErr := io.Copy(writer, response.Body)
		if copyErr != nil {
			t.Errorf("write OAuth replay response: %v", copyErr)
		}
	})
	server.Start()
	t.Cleanup(func() {
		server.Close()

		consumeErr := transport.AssertConsumed()
		if consumeErr != nil {
			t.Errorf("OAuth replay consumption: %v", consumeErr)
		}
	})

	return server, transport
}

func TestCLIOAuthLocalRequestValidatesRequestTarget(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodGet, "/oauth/v2/authorize?state=synthetic", nil)
	request.URL.Scheme = ""
	request.URL.Host = ""

	request.Host = "127.0.0.1:43127"

	if !isCLIOAuthLocalRequest(request, request.Host) {
		t.Fatal("valid local OAuth request was rejected")
	}

	testCases := map[string]func(*http.Request){
		"empty request target": func(request *http.Request) {
			request.RequestURI = ""
		},
		"mismatched path": func(request *http.Request) {
			request.RequestURI = "/oauth/v2/token?state=synthetic"
		},
		"mismatched query": func(request *http.Request) {
			request.RequestURI = "/oauth/v2/authorize?state=other"
		},
		"absolute request target": func(request *http.Request) {
			request.RequestURI = "http://example.test/oauth/v2/authorize?state=synthetic"
		},
		"invalid method": func(request *http.Request) {
			request.Method = "TRACE"
		},
		"unexpected authority": func(request *http.Request) {
			request.Host = "example.test"
		},
	}

	for name, mutate := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			candidate := request.Clone(request.Context())
			mutate(candidate)

			if isCLIOAuthLocalRequest(candidate, request.Host) {
				t.Fatal("invalid local OAuth request was accepted")
			}
		})
	}
}

func isCLIOAuthLocalRequest(request *http.Request, expectedAuthority string) bool {
	if request == nil || request.URL == nil || request.Host != expectedAuthority ||
		(request.Method != http.MethodGet && request.Method != http.MethodPost) ||
		request.URL.Scheme != "" || request.URL.Host != "" || request.URL.User != nil ||
		request.URL.Opaque != "" || request.URL.Fragment != "" || request.RequestURI == "" {
		return false
	}

	requestTarget, err := url.ParseRequestURI(request.RequestURI)
	if err != nil || requestTarget.Scheme != "" || requestTarget.Host != "" ||
		requestTarget.User != nil || requestTarget.Opaque != "" || requestTarget.Fragment != "" ||
		requestTarget.RequestURI() != request.RequestURI {
		return false
	}

	if request.URL.RequestURI() != request.RequestURI ||
		requestTarget.EscapedPath() != request.URL.EscapedPath() ||
		requestTarget.RawQuery != request.URL.RawQuery ||
		requestTarget.ForceQuery != request.URL.ForceQuery {
		return false
	}

	_, err = url.ParseQuery(requestTarget.RawQuery)

	return err == nil
}

func newOAuthReplayTokenStore(t *testing.T, server *httptest.Server) tokenStore {
	t.Helper()

	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	return tokenStore{
		path: filepath.Join(t.TempDir(), "tokens.json"),
		clientOptions: []ring.Option{
			ring.WithHTTPClient(client),
			ring.WithEndpoints(ring.Endpoints{
				APIBaseURL:       "",
				OAuthBaseURL:     server.URL,
				SolutionsBaseURL: "",
				SignalingURL:     "",
			}),
		},
	}
}

func (bindings *cliOAuthBindings) normalize(request *http.Request) error {
	query := request.URL.Query()
	if _, exists := query["state"]; exists {
		err := bindings.normalizeAuthorizationQuery(request, query)
		if err != nil {
			return err
		}
	}

	if hardware := request.Header.Get("Hardware_id"); hardware != "" {
		if hardware != bindings.hardware {
			return commandError("hardware identity changed across OAuth exchanges")
		}

		request.Header.Set("Hardware_id", "$hardware")
	}

	if request.Method != http.MethodPost {
		return nil
	}

	body, err := io.ReadAll(request.Body)
	if err != nil {
		return wrapCommandError("read OAuth request body", err)
	}

	if request.ContentLength != int64(len(body)) || request.Header.Get("Content-Length") != strconv.Itoa(len(body)) {
		return commandError("OAuth form content length does not match its encoded body")
	}

	form, err := url.ParseQuery(string(body))
	if err != nil {
		return wrapCommandError("parse OAuth form", err)
	}

	if form.Encode() != string(body) {
		return commandError("OAuth form encoding is not canonical")
	}

	if _, exists := form["code_verifier"]; exists {
		verifier, err := requireOneFormValue(form, "code_verifier")
		if err != nil {
			return err
		}

		err = bindings.verifyCodeVerifier(verifier)
		if err != nil {
			return err
		}

		form.Set("code_verifier", "$verifier")
	}

	normalized := form.Encode()
	request.Body = io.NopCloser(strings.NewReader(normalized))
	request.ContentLength = int64(len(normalized))
	request.Header.Set("Content-Length", strconv.Itoa(len(normalized)))

	return nil
}

func (bindings *cliOAuthBindings) normalizeAuthorizationQuery(request *http.Request, query url.Values) error {
	state, err := requireOneFormValue(query, "state")
	if err != nil {
		return err
	}

	err = bindings.bindState(state)
	if err != nil {
		return err
	}

	challenge, err := requireOneFormValue(query, "code_challenge")
	if err != nil {
		return err
	}

	err = bindings.bindChallenge(challenge)
	if err != nil {
		return err
	}

	hardware, err := requireOneFormValue(query, "hardware_id")
	if err != nil {
		return err
	}

	err = bindings.bindHardware(hardware)
	if err != nil {
		return err
	}

	query.Set("state", "$state")
	query.Set("code_challenge", "$challenge")
	query.Set("hardware_id", "$hardware")
	request.URL.RawQuery = query.Encode()

	return nil
}

func requireOneFormValue(values url.Values, key string) (string, error) {
	items := values[key]
	if len(items) != 1 {
		return "", commandError("OAuth " + key + " must occur once")
	}

	return items[0], nil
}

func (bindings *cliOAuthBindings) bindState(value string) error {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 16 || hex.EncodeToString(decoded) != value {
		return commandError("OAuth state is not canonical 16-byte lowercase hex")
	}

	if bindings.state != "" && bindings.state != value {
		return commandError("OAuth state changed across exchanges")
	}

	bindings.state = value

	return nil
}

func (bindings *cliOAuthBindings) bindChallenge(value string) error {
	if !canonicalOAuthBytes(value) {
		return commandError("PKCE challenge is not canonical")
	}

	if bindings.challenge != "" && bindings.challenge != value {
		return commandError("PKCE challenge changed across exchanges")
	}

	bindings.challenge = value

	return nil
}

func (bindings *cliOAuthBindings) bindHardware(value string) error {
	identity, err := uuid.Parse(value)
	if err != nil || identity.String() != value || identity.Version() != 4 || identity.Variant() != uuid.RFC4122 {
		return commandError("hardware identity is not a canonical UUID v4")
	}

	if bindings.hardware != "" && bindings.hardware != value {
		return commandError("hardware identity changed across exchanges")
	}

	bindings.hardware = value

	return nil
}

func (bindings *cliOAuthBindings) verifyCodeVerifier(value string) error {
	if !canonicalOAuthBytes(value) {
		return commandError("PKCE verifier is not canonical")
	}

	digest := sha256.Sum256([]byte(value))
	if bindings.challenge == "" || base64.RawURLEncoding.EncodeToString(digest[:]) != bindings.challenge {
		return commandError("PKCE verifier does not match its challenge")
	}

	return nil
}

func canonicalOAuthBytes(value string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(value)

	return err == nil && len(decoded) == 32 && base64.RawURLEncoding.EncodeToString(decoded) == value
}
