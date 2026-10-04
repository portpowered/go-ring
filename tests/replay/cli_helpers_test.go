package replay_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func buildReplayCLI(t *testing.T) string {
	t.Helper()

	cliDir, err := filepath.Abs(filepath.Join("..", "..", "cmd", "go-ring"))
	if err != nil {
		t.Fatal(err)
	}

	exe := filepath.Join(t.TempDir(), "go-ring")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}

	build := exec.CommandContext(t.Context(),
		"go",
		"build",
		"-o",
		exe,
		".",
	) // #nosec G204 -- fixed Go compiler invocation; output and working directory are test-owned.

	build.Dir, build.Env = cliDir, append(os.Environ(), "GOFLAGS=-buildvcs=false")

	{
		output, err := build.CombinedOutput()
		if err != nil {
			t.Fatalf("build CLI: %v\n%s", err, output)
		}
	}

	return exe
}
