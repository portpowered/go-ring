package pushreceiver

import (
	"errors"
	"testing"
	"time"
)

func TestContextualErrorPreservesCauseAndSentinelIdentity(t *testing.T) {
	t.Parallel()

	wrapped := wrapError(ErrGcmAuthorization, "register push client")
	if !errors.Is(wrapped, ErrGcmAuthorization) {
		t.Fatal("wrapped authorization error did not retain sentinel identity")
	}
}

func TestBackoffDurationStaysWithinConfiguredMaximum(t *testing.T) {
	t.Parallel()

	backoff := NewBackoff(10*time.Millisecond, 25*time.Millisecond)
	for range 20 {
		duration := backoff.duration()
		if duration < 0 || duration > 25*time.Millisecond {
			t.Fatalf("duration = %s, want a value from zero through 25ms", duration)
		}
	}
}

func TestBackoffDurationReturnsZeroForInvalidBounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		base time.Duration
		max  time.Duration
	}{
		{name: "zero base", base: 0, max: time.Second},
		{name: "negative base", base: -time.Second, max: time.Second},
		{name: "zero maximum", base: time.Second, max: 0},
		{name: "negative maximum", base: time.Second, max: -time.Second},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			backoff := NewBackoff(test.base, test.max)
			if duration := backoff.duration(); duration != 0 {
				t.Fatalf("duration = %s, want zero", duration)
			}
		})
	}
}
