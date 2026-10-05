package replay_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	api := newDiagnosticCLIHTTPPairs(t, "cli-health-sound-reboot.json")
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
}
