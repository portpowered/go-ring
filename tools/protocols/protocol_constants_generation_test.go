package protocols_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestGeneratedProtocolConstantsAreCurrent(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)

	dependencyPath := filepath.Join(root, "tools", "protocols", "node_modules", "yaml")

	_, statErr := os.Stat(dependencyPath)

	if errors.Is(statErr, os.ErrNotExist) {
		t.Skip("run npm ci in tools/protocols before checking generated protocol constants")
	} else if statErr != nil {
		t.Fatal(statErr)
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()

	// #nosec G204 -- script and schema paths are fixed repository-owned inputs.
	command := exec.CommandContext(ctx, "node", "tools/protocols/generate_protocol_constants.mjs", "--check")
	command.Dir = root

	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated protocol constants are stale: %v\n%s", err, output)
	}
}
