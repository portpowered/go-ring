package routegate

import (
	"path/filepath"
	"strings"
)

func isGeneratedGo(data []byte) bool {
	text := string(data)

	return strings.Contains(text, "Code generated") && strings.Contains(text, "DO NOT EDIT.")
}

func pathWithin(path, base string) bool {
	relative, err := filepath.Rel(base, path)

	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func relativePath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}

	return filepath.ToSlash(relative)
}
