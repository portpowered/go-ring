package replay_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDiagnosticCLIHTTPReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("builds nested CLI module")
	}
	exe := buildReplayCLI(t)
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if (r.Header.Get("Authorization") != "Bearer replay-token" && r.Header.Get("Authorization") != "Bearer second-access") || r.Header.Get("hardware_id") == "" {
			http.Error(w, "wrong credentials", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/clients_api/session":
			_, _ = w.Write([]byte(`{}`))
		case "/device_info/v3/devices":
			_, _ = w.Write([]byte(`{"devices":[{"id":12345,"name":"Replay camera","family":"stickup_cams","kind":"stickup_cam"}]}`))
		case "/clients_api/doorbots/12345/siren_on", "/clients_api/doorbots/12345/siren_off":
			_, _ = w.Write([]byte(`{}`))
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	tokenFile := filepath.Join(t.TempDir(), "tokens.json")
	data, err := json.Marshal(map[string]any{"access_token": "replay-token", "refresh_token": "replay-refresh", "token_type": "Bearer", "expires_in": 3600, "hardware_id": "replay-hardware", "received_at": time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenFile, data, 0o600); err != nil {
		t.Fatal(err)
	}
	runCLI := func(args ...string) string {
		t.Helper()
		command := exec.Command(exe, append([]string{"--token-file", tokenFile, "--api-base", server.URL}, args...)...) // #nosec G204 -- executes the CLI binary built in this test with local server URLs and temporary files.
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("CLI %v: %v\n%s", args, err, output)
		}
		return string(output)
	}
	if got := runCLI("devices", "list"); !strings.Contains(got, "12345\tReplay camera") {
		t.Fatalf("devices output: %q", got)
	}
	if got := runCLI("siren", "12345", "on"); !strings.Contains(got, "acknowledged") {
		t.Fatalf("siren on: %q", got)
	}
	if got := runCLI("siren", "12345", "off"); !strings.Contains(got, "acknowledged") {
		t.Fatalf("siren off: %q", got)
	}
	if got := runCLI("auth", "status"); !strings.Contains(got, "Saved login") || strings.Contains(got, "replay-token") {
		t.Fatalf("auth status: %q", got)
	}
	if got := runCLI("auth", "logout"); !strings.Contains(got, "removed") {
		t.Fatalf("auth logout: %q", got)
	}
	if _, err := os.Stat(tokenFile); !os.IsNotExist(err) {
		t.Fatalf("token file not removed: %v", err)
	}
	var oauthState string
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/oauth/v2/authorize" && r.URL.Query().Get("response_type") == "code":
			oauthState = r.URL.Query().Get("state")
			_, _ = w.Write([]byte(`<script id="oauth-args">{"csrf-token":"csrf-value"}</script>`))
		case r.Method == http.MethodPost && r.URL.Path == "/oauth/v2/signin":
			w.WriteHeader(http.StatusPreconditionFailed)
			_, _ = w.Write([]byte(`{"tsv_state":"email"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/oauth/v2/2fa/verify":
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && r.URL.Path == "/oauth/v2/authorize":
			w.Header().Set("Location", "https://ring.com/signin/callback?code=auth-code&state="+oauthState)
			w.WriteHeader(http.StatusFound)
		case r.Method == http.MethodPost && r.URL.Path == "/oauth/token":
			_ = r.ParseForm()
			if r.Form.Get("grant_type") == "refresh_token" {
				if r.Form.Get("refresh_token") == "rotated-refresh" {
					_, _ = w.Write([]byte(`{"access_token":"second-access","refresh_token":"second-refresh","expires_in":3600,"token_type":"Bearer"}`))
				} else {
					_, _ = w.Write([]byte(`{"access_token":"rotated-access","refresh_token":"rotated-refresh","expires_in":3600,"token_type":"Bearer"}`))
				}
			} else {
				_, _ = w.Write([]byte(`{"access_token":"initial-access","refresh_token":"initial-refresh","expires_in":3600,"token_type":"Bearer"}`))
			}
		default:
			http.Error(w, "unexpected auth request", http.StatusNotFound)
		}
	}))
	defer authServer.Close()
	login := exec.Command(exe, "--token-file", tokenFile, "--oauth-base", authServer.URL, "auth", "login") // #nosec G204 -- executes the CLI binary built in this test with local fixture credentials.
	login.Env = append(os.Environ(), "RING_PASSWORD=replay-password", "RING_OTP_CODE=123456")
	login.Stdin = strings.NewReader("replay@example.com\n")
	if output, err := login.CombinedOutput(); err != nil {
		t.Fatalf("CLI login: %v\n%s", err, output)
	}
	rotated, err := os.ReadFile(tokenFile) // #nosec G304 -- tokenFile was created inside this test's temporary directory.
	if err != nil || !strings.Contains(string(rotated), "rotated-refresh") || !strings.Contains(string(rotated), "hardware_id") {
		t.Fatalf("login did not save rotated tokens: %v", err)
	}
	var expired map[string]any
	if err := json.Unmarshal(rotated, &expired); err != nil {
		t.Fatal(err)
	}
	expired["received_at"] = time.Now().Add(-2 * time.Hour)
	stale, err := json.Marshal(expired)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenFile, stale, 0o600); err != nil {
		t.Fatal(err)
	}
	refresh := exec.Command(exe, "--token-file", tokenFile, "--api-base", server.URL, "--oauth-base", authServer.URL, "devices", "list") // #nosec G204 -- executes the CLI binary built in this test with local server URLs and a temporary token file.
	if output, err := refresh.CombinedOutput(); err != nil {
		t.Fatalf("CLI refresh: %v\n%s", err, output)
	}
	refreshed, err := os.ReadFile(tokenFile) // #nosec G304 -- tokenFile was created inside this test's temporary directory.
	if err != nil || !strings.Contains(string(refreshed), "second-refresh") {
		t.Fatalf("refresh did not rotate token: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, expected := range []string{"GET /device_info/v3/devices", "PUT /clients_api/doorbots/12345/siren_on", "PUT /clients_api/doorbots/12345/siren_off"} {
		if !containsString(paths, expected) {
			t.Errorf("missing %s in %v", expected, paths)
		}
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
