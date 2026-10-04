package replay_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const viewReplayOutputTimeout = 15 * time.Second

//nolint:paralleltest // LIB-05: this timed CLI-to-WebSocket transcript runs serially.
func TestDiagnosticCLIViewAndArrowReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("builds nested CLI module")
	}

	exe := buildReplayCLI(t)
	tokenFile := filepath.Join(t.TempDir(), "tokens.json")

	tokens, err := json.Marshal(
		map[string]any{
			"access_token":  "view-token",
			"refresh_token": "view-refresh",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"hardware_id":   "view-hardware",
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

	api := newDiagnosticCLIHTTPPairs(t, "cli-view-bootstrap.json")

	ws, rtc := newDiagnosticCLIRTCWebSocketPeer(t, "cli-view.json")

	command := exec.CommandContext(t.Context(),
		exe,
		"--token-file",
		tokenFile,
		"--api-base",
		api.URL,
		"--solutions-base",
		api.URL,
		"--signaling-url",
		ws.URL()+"?token={token}",
		"view",
		"12345",
		"--player",
		"none",
		"--debug",
	) // #nosec G204 -- executes the CLI binary built in this test with local servers and temporary token.
	keys, keyWriter := io.Pipe()
	command.Stdin = keys
	cliOutput := newViewCLIOutput()
	command.Stdout = cliOutput
	command.Stderr = cliOutput

	runDiagnosticViewInput(t, command, keyWriter, cliOutput)

	err = ws.AssertComplete(10 * time.Second)
	if err != nil {
		t.Fatal(err)
	}

	err = rtc.assertClosed()
	if err != nil {
		t.Fatal(err)
	}
}

func runDiagnosticViewInput(t *testing.T, command *exec.Cmd, keyWriter *io.PipeWriter, output *viewCLIOutput) {
	t.Helper()

	err := command.Start()
	if err != nil {
		t.Fatal(err)
	}

	waited := false

	defer func() {
		_ = keyWriter.Close()
		if !waited {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()

	waitForViewOutput(t, output, "Remote SDP answer applied")
	waitForViewOutput(t, output, "Session active.")
	waitForViewOutput(t, output, "First video packet received")

	for _, key := range []struct{ input, direction string }{
		{"\x1b[C", "right"},
		{"\x1b[D", "left"},
		{"\x1b[A", "up"},
		{"\x1b[B", "down"},
	} {
		_, err = io.WriteString(keyWriter, key.input)
		if err != nil {
			t.Fatalf("send %s arrow: %v\n%s", key.direction, err, output.String())
		}

		waitForViewOutput(t, output, "PTZ command acknowledged: "+key.direction)
	}

	_, err = io.WriteString(keyWriter, "q")
	if err != nil {
		t.Fatalf("quit view: %v\n%s", err, output.String())
	}

	err = keyWriter.Close()
	if err != nil {
		t.Fatalf("close view input: %v", err)
	}

	err = command.Wait()
	waited = true

	if err != nil {
		t.Fatalf("CLI view: %v\n%s", err, output.String())
	}
}

type viewCLIOutput struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	updated chan struct{}
}

func newViewCLIOutput() *viewCLIOutput {
	return &viewCLIOutput{mu: sync.Mutex{}, buffer: bytes.Buffer{}, updated: make(chan struct{}, 1)}
}

func (output *viewCLIOutput) Write(data []byte) (int, error) {
	output.mu.Lock()
	written, _ := output.buffer.Write(data)
	output.mu.Unlock()

	select {
	case output.updated <- struct{}{}:
	default:
	}

	return written, nil
}

func (output *viewCLIOutput) String() string {
	output.mu.Lock()
	defer output.mu.Unlock()

	return output.buffer.String()
}

func waitForViewOutput(t *testing.T, output *viewCLIOutput, expected string) {
	t.Helper()

	timer := time.NewTimer(viewReplayOutputTimeout)
	defer timer.Stop()

	for {
		if strings.Contains(output.String(), expected) {
			return
		}

		select {
		case <-output.updated:
		case <-timer.C:
			t.Fatalf("timed out waiting for CLI output %q: %s", expected, output.String())
		}
	}
}
