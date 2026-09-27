package signaling

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// Exercise terminal transitions while RPC callers, replies, events, and the
// heartbeat clock are active. Every caller must finish and no waiter may leak.
func TestSessionAdversarialTerminationStress(t *testing.T) {
	const iterations = 100
	const callers = 8
	for iteration := 0; iteration < iterations; iteration++ {
		clock := newClock()
		out := make(chan Message, callers+4)
		s, err := NewSession(context.Background(), SessionConfig{DeviceID: 7, DialogID: "dialog", SignalID: "signal", ControlID: "control", Heartbeat: time.Second, Clock: clock, Send: func(ctx context.Context, message Message) error {
			select {
			case out <- message:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}})
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var workers sync.WaitGroup
		results := make(chan error, callers)
		for call := 0; call < callers; call++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				<-start
				_, callErr := s.Call(context.Background(), "PTZ.Pan.Step", map[string]any{"direction": "LEFT"})
				results <- callErr
			}()
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for {
				select {
				case message := <-out:
					if message.Method != "rpc" {
						continue
					}
					var body rpcCommandEnvelope
					if json.Unmarshal(message.Body, &body) != nil {
						continue
					}
					reply, err := json.Marshal(map[string]any{"doorbot_id": 7, "session_id": "signal", "command": map[string]any{"jsonrpc": "2.0", "id": body.Command.ID, "result": map[string]any{"sessionId": "control"}}})
					if err != nil {
						t.Errorf("marshal RPC reply: %v", err)
						return
					}
					_ = s.Handle(Message{Method: "rpc", DialogID: "dialog", Body: reply})
				case <-s.done:
					return
				}
			}
		}()
		workers.Add(3)
		go func() { defer workers.Done(); <-start; clock.advance(MaxSessionAge) }()
		go func() { defer workers.Done(); <-start; s.Fail(ErrBackpressure) }()
		go func() { defer workers.Done(); <-start; _ = s.Close() }()
		close(start)
		finished := make(chan struct{})
		go func() { workers.Wait(); close(finished) }()
		select {
		case <-finished:
		case <-time.After(2 * time.Second):
			t.Fatalf("iteration %d deadlocked", iteration)
		}
		close(results)
		for err := range results {
			if err != nil && !errors.Is(err, ErrClosed) && !errors.Is(err, ErrExpired) && !errors.Is(err, ErrHeartbeat) && !errors.Is(err, ErrBackpressure) && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("iteration %d unexpected RPC error: %v", iteration, err)
			}
		}
		if pending := s.Pending(); pending != 0 {
			t.Fatalf("iteration %d retained %d RPC waiters", iteration, pending)
		}
	}
}
