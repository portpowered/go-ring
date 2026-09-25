package ring

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/signaling"
)

func TestSignalingSendCanCancelWhileWaitingForWriter(t *testing.T) {
	c := &SignalingConnection{done: make(chan struct{}), writeGate: make(chan struct{}, 1)}
	c.writeGate <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := c.send(ctx, signaling.Message{Method: "queued"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued send error = %v, want deadline exceeded", err)
	}
}
