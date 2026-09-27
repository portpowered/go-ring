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
	directory := t.TempDir()
	tokenPath := filepath.Join(directory, "tokens.json")
	pushPath := filepath.Join(directory, "push.json")
	for _, path := range []string{tokenPath, pushPath} {
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := run(context.Background(), []string{"--token-file", tokenPath, "auth", "logout"}, strings.NewReader(""), io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{tokenPath, pushPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists after logout: %v", path, err)
		}
	}
}
