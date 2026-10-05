package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

func readLocalFile(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, errorf("open local directory for %s: %w", path, err)
	}

	file, err := root.Open(filepath.Base(path))
	if err != nil {
		rootCloseErr := root.Close()

		return nil, errorf("open local file %s: %w", path, errors.Join(err, rootCloseErr))
	}

	data, readErr := io.ReadAll(file)
	fileCloseErr := file.Close()
	rootCloseErr := root.Close()

	readErr = errors.Join(readErr, fileCloseErr, rootCloseErr)
	if readErr != nil {
		return nil, errorf("read local file %s: %w", path, readErr)
	}

	return data, nil
}
