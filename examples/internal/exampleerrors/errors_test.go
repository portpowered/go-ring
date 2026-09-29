package exampleerrors_test

import (
	"errors"
	"io"
	"testing"

	"github.com/portpowered/go-ring/examples/internal/exampleerrors"
)

func TestWrapPreservesCause(t *testing.T) {
	t.Parallel()

	err := exampleerrors.Wrap("open signaling", io.EOF)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("wrapped error = %v, want the original cause", err)
	}

	if err.Error() != "open signaling: EOF" {
		t.Fatalf("wrapped error = %q, want operation context", err.Error())
	}
}

func TestWrapNilCauseReturnsNil(t *testing.T) {
	t.Parallel()

	err := exampleerrors.Wrap("open signaling", nil)
	if err != nil {
		t.Fatalf("wrapped nil cause = %v, want nil", err)
	}
}
