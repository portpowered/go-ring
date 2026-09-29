package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogoutRemovesPushCredentials(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	tokenPath := filepath.Join(directory, "tokens.json")

	pushPath := filepath.Join(directory, "push.json")

	for _, path := range []string{tokenPath, pushPath} {
		err := os.WriteFile(path, []byte("{}"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	err := run(
		context.Background(),
		[]string{"--token-file", tokenPath, "auth", "logout"},
		strings.NewReader(""),
		io.Discard,
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{tokenPath, pushPath} {
		_, err = os.Stat(path)
		if !os.IsNotExist(err) {
			t.Fatalf("%s still exists after logout: %v", path, err)
		}
	}
}
