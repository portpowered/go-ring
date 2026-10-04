package signaling

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	generatedsignaling "github.com/portpowered/go-ring/pkg/dependencymodels/signaling"
)

func TestSessionRejectsInvalidStartupWithoutSending(t *testing.T) {
	t.Parallel()

	for name, change := range map[string]func(*SessionConfig){
		"missing identity":      func(c *SessionConfig) { c.DialogID = "" },
		"same identity domains": func(c *SessionConfig) { c.ControlID = c.SignalID },
		"missing heartbeat":     func(c *SessionConfig) { c.Heartbeat = 0 },
		"unbounded heartbeat":   func(c *SessionConfig) { c.Heartbeat = 2 * time.Minute },
		"negative max age":      func(c *SessionConfig) { c.MaxAge = -time.Second },
		"extended max age":      func(c *SessionConfig) { c.MaxAge = MaxSessionAge + time.Second },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			config := SessionConfig{
				DeviceID:  1001,
				DialogID:  "d",
				SignalID:  "s",
				ControlID: "c",
				Heartbeat: time.Second,
				MaxAge:    0,
				Clock:     newClock(),
				Send: func(context.Context, Message) error {
					t.Error("invalid session sent a message")

					return nil
				},
			}
			change(&config)

			{
				s, err := NewSession(context.Background(), config)
				if err == nil {
					_ = s.Close()

					t.Fatal("invalid startup succeeded")
				}
			}
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	{
		session, err := NewSession(ctx, SessionConfig{
			DeviceID:  1001,
			DialogID:  "d",
			SignalID:  "s",
			ControlID: "c",
			Heartbeat: time.Second,
			MaxAge:    0,
			Clock:     newClock(),
			Send:      func(context.Context, Message) error { return nil },
		})
		if !errors.Is(err, context.Canceled) || session != nil {
			t.Fatalf("canceled startup: %v", err)
		}
	}
}

func TestSessionCancellationAndMalformedPayloadDoNotLeakPendingWork(t *testing.T) {
	t.Parallel()

	session, _, out := setupSession(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	{
		_, err := session.Receive(canceled)
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}

	err := session.Wait(canceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	err = session.Send(canceled, "mic_enable", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	{
		_, err := session.Call(canceled, "PTZ.Pan.Step", generatedsignaling.PtzDirectionLeft, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}

	{
		_, err := session.Call(context.Background(), "PTZ.Pan.Step", generatedsignaling.PtzDirection(255), nil)
		if err == nil {
			t.Fatal("accepted unrecognized generated PTZ direction")
		}
	}

	if session.Pending() != 0 {
		t.Fatal("malformed/canceled call retained pending entry")
	}

	select {
	case <-out:
		t.Fatal("canceled/malformed operation reached peer")
	default:
	}

	err = session.Handle(Message{DialogID: "dialog", RIID: "", Method: "rpc", Body: json.RawMessage(`{`)})
	if err == nil {
		t.Fatal("accepted malformed body")
	}

	session.Fail(nil)

	{
		_, err := session.Receive(context.Background())
		if !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
	}

	{
		_, err := session.Call(context.Background(), "PTZ.Pan.Step", generatedsignaling.PtzDirectionLeft, nil)
		if !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
	}

	err = session.Send(context.Background(), "ping", nil)
	if !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}

	err = session.Handle(
		Message{
			DialogID: "dialog",
			RIID:     "",
			Method:   "pong",
			Body:     json.RawMessage(`{"doorbot_id":1001,"session_id":"signal"}`),
		},
	)
	if !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestRPCErrorFormattingDoesNotExposePeerText(t *testing.T) {
	t.Parallel()

	err := &RPCError{Code: -32602, Message: "private response body"}
	if err.Error() != "session RPC error -32602" {
		t.Fatalf("unsafe error text: %s", err)
	}
}

func TestBlockedRPCPreservesTerminalCause(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	terminal := syntheticSocketFailureError{}

	session, err := NewSession(
		context.Background(),
		SessionConfig{
			DeviceID:  1,
			DialogID:  "d",
			SignalID:  "s",
			ControlID: "c",
			Heartbeat: time.Second,
			MaxAge:    0,
			Clock:     newClock(),
			Send: func(ctx context.Context, _ Message) error {
				close(entered)
				<-ctx.Done()

				return ctx.Err()
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = session.Close() }()

	result := make(chan error, 1)

	go func() {
		_, err := session.Call(context.Background(), "PTZ.Pan.Step", generatedsignaling.PtzDirectionLeft, nil)
		result <- err
	}()

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("write not entered")
	}

	session.Fail(terminal)

	select {
	case err := <-result:
		if !errors.Is(err, terminal) {
			t.Fatalf("lost failure cause: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RPC did not unblock")
	}
}
