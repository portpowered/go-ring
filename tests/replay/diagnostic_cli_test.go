package replay_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestDiagnosticCLIHTTPReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("builds nested CLI module")
	}

	t.Parallel()

	exe := buildReplayCLI(t)

	server := newDiagnosticCLIHTTPPairs(t, "cli-devices-siren.json")

	tokenFile := seedDiagnosticTokenFile(t)

	assertDiagnosticCLICommands(t, exe, tokenFile, server.URL)

	authServer := newDiagnosticCLIAuthPairs(t)
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
