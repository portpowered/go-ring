package replay_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDiagnosticCLIHTTPReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("builds nested CLI module")
	}

	t.Parallel()

	exe := buildReplayCLI(t)

	var (
		mu    sync.Mutex
		paths []string
	)

	server := newDiagnosticCLIAPIServer(&mu, &paths)
	defer server.Close()

	tokenFile := seedDiagnosticTokenFile(t)

	assertDiagnosticCLICommands(t, exe, tokenFile, server.URL)

	authServer := newDiagnosticCLIAuthServer()
	defer authServer.Close()

	login := exec.CommandContext(
		t.Context(),
		exe,
		"--token-file",
		tokenFile,
		"--oauth-base",
		authServer.URL,
		"auth",
		"login",
	) // #nosec G204 -- executes the CLI binary built in this test with local fixture credentials.

	login.Env = append(os.Environ(), "RING_PASSWORD=replay-password", "RING_OTP_CODE=123456")

	login.Stdin = strings.NewReader("replay@example.com\n")
	{
		output, err := login.CombinedOutput()
		if err != nil {
			t.Fatalf("CLI login: %v\n%s", err, output)
		}
	}

	rotated, err := os.ReadFile(
		tokenFile,
	) // #nosec G304 -- tokenFile was created inside this test's temporary directory.
	if err != nil {
		t.Fatalf("login did not save rotated tokens: %v", err)
	}

	if !strings.Contains(string(rotated), "rotated-refresh") || !strings.Contains(string(rotated), "hardware_id") {
		t.Fatal("login did not save the rotated refresh token and hardware identity")
	}

	assertDiagnosticTokenFileKeys(t, rotated)

	var expired map[string]any
	{
		err := json.Unmarshal(rotated, &expired)
		if err != nil {
			t.Fatal(err)
		}
	}

	expired["received_at"] = time.Now().Add(-2 * time.Hour)

	stale, err := json.Marshal(expired)
	if err != nil {
		t.Fatal(err)
	}

	{
		err := os.WriteFile(tokenFile, stale, 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	refresh := exec.CommandContext(
		t.Context(),
		exe,
		"--token-file",
		tokenFile,
		"--api-base",
		server.URL,
		"--oauth-base",
		authServer.URL,
		"auth",
		"refresh",
	) // #nosec G204 -- executes the CLI binary built in this test with local server URLs and a temporary token file.
	{
		output, err := refresh.CombinedOutput()
		if err != nil {
			t.Fatalf("CLI refresh: %v\n%s", err, output)
		}
	}

	refreshed, err := os.ReadFile(
		tokenFile,
	) // #nosec G304 -- tokenFile was created inside this test's temporary directory.
	if err != nil {
		t.Fatalf("refresh did not rotate token: %v", err)
	}

	if !strings.Contains(string(refreshed), "second-refresh") {
		t.Fatal("refresh did not save the second refresh token")
	}

	assertDiagnosticTokenFileKeys(t, refreshed)

	mu.Lock()
	defer mu.Unlock()

	for _, expected := range []string{
		"GET /device_info/v3/devices",
		"PUT /clients_api/doorbots/12345/siren_on",
		"PUT /clients_api/doorbots/12345/siren_off",
	} {
		if !containsString(paths, expected) {
			t.Errorf("missing %s in %v", expected, paths)
		}
	}
}

func assertDiagnosticTokenFileKeys(t *testing.T, data []byte) {
	t.Helper()

	var saved map[string]json.RawMessage

	err := json.Unmarshal(data, &saved)
	if err != nil {
		t.Fatalf("decode persisted token file: %v", err)
	}

	keys := make([]string, 0, len(saved))
	for key := range saved {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	want := []string{"access_token", "expires_in", "hardware_id", "received_at", "refresh_token", "token_type"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("persisted token keys = %v, want exactly %v", keys, want)
	}
}

func seedDiagnosticTokenFile(t *testing.T) string {
	t.Helper()

	tokenFile := filepath.Join(t.TempDir(), "tokens.json")

	data, err := json.Marshal(map[string]any{
		"access_token": "replay-token", "refresh_token": "replay-refresh", "token_type": "Bearer",
		"expires_in": 3600, "hardware_id": "replay-hardware", "received_at": time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(tokenFile, data, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return tokenFile
}

func assertDiagnosticCLICommands(t *testing.T, exe, tokenFile, apiBase string) {
	t.Helper()

	runCLI := func(args ...string) string {
		t.Helper()

		command := exec.CommandContext(
			t.Context(), exe,
			append([]string{"--token-file", tokenFile, "--api-base", apiBase}, args...)...,
		) // #nosec G204 -- runs the test-built CLI binary with local servers and temporary files.

		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("CLI %v: %v\n%s", args, err, output)
		}

		return string(output)
	}

	if got := runCLI("devices", "list"); !json.Valid([]byte(got)) ||
		!strings.Contains(got, `"id":"12345"`) || !strings.Contains(got, `"name":"Replay camera"`) {
		t.Fatalf("devices output: %q", got)
	}

	if got := runCLI("siren", "12345", "on"); !strings.Contains(got, "acknowledged") {
		t.Fatalf("siren on: %q", got)
	}

	if got := runCLI("siren", "12345", "off"); !strings.Contains(got, "acknowledged") {
		t.Fatalf("siren off: %q", got)
	}

	if got := runCLI("auth", "status"); !json.Valid([]byte(got)) ||
		!strings.Contains(got, `"expires_at"`) || strings.Contains(got, "replay-token") {
		t.Fatalf("auth status: %q", got)
	}

	if got := runCLI("auth", "logout"); !strings.Contains(got, "removed") {
		t.Fatalf("auth logout: %q", got)
	}

	_, err := os.Stat(tokenFile)
	if !os.IsNotExist(err) {
		t.Fatalf("token file not removed: %v", err)
	}
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}

	return false
}

func newDiagnosticCLIAPIServer(mu *sync.Mutex, paths *[]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		mu.Lock()

		*paths = append(*paths, request.Method+" "+request.URL.Path)

		mu.Unlock()

		if (request.Header.Get("Authorization") != "Bearer replay-token" &&
			request.Header.Get("Authorization") != "Bearer second-access") ||
			request.Header.Get("Hardware_id") == "" {
			http.Error(responseWriter, "wrong credentials", http.StatusUnauthorized)

			return
		}

		switch request.URL.Path {
		case legacyClientSessionPath:
			_, _ = responseWriter.Write([]byte(`{}`))
		case legacyDeviceListPath:
			_, _ = responseWriter.Write(
				[]byte(
					`{"devices":[{"id":12345,"name":"Replay camera","family":"stickup_cams","kind":"stickup_cam"}]}`,
				),
			)
		case "/clients_api/doorbots/12345/siren_on", "/clients_api/doorbots/12345/siren_off":
			_, _ = responseWriter.Write([]byte(`{}`))
		default:
			http.Error(responseWriter, "unexpected request", http.StatusNotFound)
		}
	}))
}

func newDiagnosticCLIAuthServer() *httptest.Server {
	var oauthState string

	return httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet &&
			request.URL.Path == oauthAuthorizePath &&
			request.URL.Query().Get("response_type") == "code":
			oauthState = request.URL.Query().Get("state")
			_, _ = responseWriter.Write([]byte(`<script id="oauth-args">{"csrf-token":"csrf-value"}</script>`))
		case request.Method == http.MethodPost && request.URL.Path == oauthSignInPath:
			responseWriter.WriteHeader(http.StatusPreconditionFailed)
			_, _ = responseWriter.Write([]byte(syntheticEmailTwoFactorState))
		case request.Method == http.MethodPost && request.URL.Path == oauthTwoFactorPath:
			_, _ = responseWriter.Write([]byte(`{}`))
		case request.Method == http.MethodGet && request.URL.Path == oauthAuthorizePath:
			responseWriter.Header().Set("Location", "https://ring.com/signin/callback?code=auth-code&state="+oauthState)
			responseWriter.WriteHeader(http.StatusFound)
		case request.Method == http.MethodPost && request.URL.Path == oauthTokenPath:
			_ = request.ParseForm()

			if request.Form.Get("grant_type") == "refresh_token" {
				if request.Form.Get("refresh_token") == "rotated-refresh" {
					_, _ = responseWriter.Write(
						[]byte(
							`{"access_token":"second-access","refresh_token":"second-refresh","expires_in":3600,"token_type":"Bearer"}`,
						),
					)
				} else {
					_, _ = responseWriter.Write([]byte(`{"access_token":"rotated-access","refres` +
						`h_token":"rotated-refresh","expires_in":` +
						`3600,"token_type":"Bearer"}`))
				}
			} else {
				_, _ = responseWriter.Write([]byte(`{"access_token":"initial-access","refres` +
					`h_token":"initial-refresh","expires_in":` +
					`3600,"token_type":"Bearer"}`))
			}
		default:
			http.Error(responseWriter, "unexpected auth request", http.StatusNotFound)
		}
	}))
}
