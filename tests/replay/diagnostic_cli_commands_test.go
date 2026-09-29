package replay_test

import (
	"encoding/json"
	"io"
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

//nolint:paralleltest // LIB-05: this test builds one CLI and checks an ordered HTTP request transcript.
func TestDiagnosticCLIHealthSoundAndRebootReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("builds nested CLI module")
	}

	exe := buildReplayCLI(t)

	tokenFile := filepath.Join(t.TempDir(), "tokens.json")

	tokens, err := json.Marshal(
		map[string]any{
			"access_token":  "fixture-token",
			"refresh_token": "fixture-refresh",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"hardware_id":   "fixture-hardware",
			"received_at":   time.Now(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	{
		err := os.WriteFile(tokenFile, tokens, 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	var (
		mu       sync.Mutex
		requests []string
	)

	api := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		mu.Lock()

		requests = append(requests, request.Method+" "+request.URL.RequestURI())

		mu.Unlock()
		responseWriter.Header().Set("Content-Type", "application/json")

		switch request.URL.Path {
		case "/clients_api/session":
			_, _ = io.WriteString(responseWriter, `{}`)
		case "/device_info/v3/devices/12345":
			_, _ = io.WriteString(
				responseWriter,
				`{"device":{"id":12345,"kind":"stickup_ca`+
					`m_mini_ptz_v1","description":"Fixture ca`+
					`mera","wifi_signal_strength":-55,"health`+
					`":{"connected":true,"rssi":-50,"firmware`+
					`_version":"fixture"}}}`,
			)
		case "/clients_api/ring_devices/12345/health":
			_, _ = io.WriteString(responseWriter, `{"battery_level":88,"signal_strength":-55}`)
		case "/clients_api/chimes/12345/play_sound":
			_, _ = io.WriteString(responseWriter, `{}`)
		case "/commands/v1/devices/12345":
			body, _ := io.ReadAll(request.Body)
			if request.Method != http.MethodPatch || !strings.Contains(string(body), `"command_name":"reboot"`) {
				http.Error(responseWriter, "wrong reboot request", http.StatusBadRequest)

				return
			}

			responseWriter.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(responseWriter, request)
		}
	}))
	defer api.Close()

	run := func(args ...string) string {
		t.Helper()

		base := []string{"--token-file", tokenFile, "--api-base", api.URL, "--solutions-base", api.URL}
		command := exec.CommandContext(t.Context(),
			exe,
			append(base, args...)...) // #nosec G204 -- exe is the CLI binary built into this test's temp directory.

		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("CLI %v: %v\n%s", args, err, output)
		}

		return string(output)
	}
	if output := run("health", "12345", "--refresh"); !strings.Contains(output, "Wi-Fi signal strength: -55") ||
		!strings.Contains(output, "Battery level: 88") {
		t.Fatalf("health output: %s", output)
	}

	if output := run("sound", "12345", "ding"); !strings.Contains(output, "Chime ding test request acknowledged") {
		t.Fatalf("sound output: %s", output)
	}

	if output := run("reboot", "12345"); !strings.Contains(output, "Reboot request acknowledged") {
		t.Fatalf("reboot output: %s", output)
	}

	mu.Lock()
	defer mu.Unlock()

	joined := strings.Join(requests, "\n")
	for _, route := range []string{
		"GET /device_info/v3/devices/12345",
		"GET /clients_api/ring_devices/12345/health",
		"POST /clients_api/chimes/12345/play_sound?kind=ding",
		"PATCH /commands/v1/devices/12345",
	} {
		if !strings.Contains(joined, route) {
			t.Fatalf("missing %s in requests:\n%s", route, joined)
		}
	}
}
