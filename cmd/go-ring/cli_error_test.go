package main

import (
	"errors"
	"os"
	"testing"
)

func TestCommandErrorRetainsTypeAndCause(t *testing.T) {
	err := wrapCommandError("load token file", os.ErrNotExist)
	var typed cliError
	if !errors.As(err, &typed) {
		t.Fatal("command error lost its CLI error type")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatal("command error lost its underlying cause")
	}
	if got, want := err.Error(), "load token file: "+os.ErrNotExist.Error(); got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestCommandErrorIncludesDiagnosticDetail(t *testing.T) {
	err := wrapCommandErrorDetail("decode frame", os.ErrInvalid, "ffmpeg diagnostics")
	want := "decode frame: " + os.ErrInvalid.Error() + ": ffmpeg diagnostics"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}
