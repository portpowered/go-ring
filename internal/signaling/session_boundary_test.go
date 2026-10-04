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

func TestMessageJSONBoundaryCases(t *testing.T) {
	t.Parallel()

	if wrapSessionContextError("unused", nil) != nil {
		t.Fatal("nil session context cause produced an error")
	}

	cause := context.Canceled

	wrapped := wrapSessionContextError("send RPC", cause)
	if wrapped.Error() != "send RPC: context canceled" || !errors.Is(wrapped, cause) {
		t.Fatalf("wrapped session context error = %v", wrapped)
	}

	for name, message := range map[string]Message{
		"unknown method": {Method: "future.method", DialogID: "", RIID: "", Body: nil},
		"invalid body":   {Method: "ping", DialogID: "", RIID: "", Body: json.RawMessage("{")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := json.Marshal(message)
			if err == nil {
				t.Fatal("invalid signaling message was marshaled")
			}
		})
	}

	encoded, err := json.Marshal(Message{
		Method:   "ping",
		DialogID: "",
		RIID:     "",
		Body:     json.RawMessage(`{"device_id":1001}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	var roundTripped Message

	err = json.Unmarshal(encoded, &roundTripped)
	if err != nil {
		t.Fatal(err)
	}

	if roundTripped.Method != "ping" || string(roundTripped.Body) != `{"device_id":1001}` {
		t.Fatalf("round-tripped message = %+v", roundTripped)
	}

	encoded, err = json.Marshal(Message{Method: "ping", DialogID: "", RIID: "", Body: nil})
	if err != nil {
		t.Fatal(err)
	}

	err = json.Unmarshal(encoded, &roundTripped)
	if err != nil || roundTripped.Method != "ping" {
		t.Fatalf("empty-body message round trip = %+v, %v", roundTripped, err)
	}

	for name, input := range map[string]string{
		"malformed envelope":  `{`,
		"missing method":      `{"body":{}}`,
		"missing body":        `{"method":"ping"}`,
		"invalid method type": `{"method":42,"body":{}}`,
		"invalid body type":   `{"method":"ping","body":42}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var message Message

			err := json.Unmarshal([]byte(input), &message)
			if err == nil {
				t.Fatal("invalid signaling envelope was accepted")
			}
		})
	}
}

func TestRPCEnvelopeBoundaryCases(t *testing.T) {
	t.Parallel()

	session, _, out := setupSession(t)

	for name, body := range map[string]json.RawMessage{
		"malformed body":  json.RawMessage(`{`),
		"missing command": json.RawMessage(`{"doorbot_id":1001,"session_id":"signal"}`),
	} {
		t.Run("frame "+name, func(t *testing.T) {
			t.Parallel()

			_, err := session.handleRPCFrame(body)
			if err == nil {
				t.Fatal("invalid RPC body was accepted")
			}
		})
	}

	for name, body := range map[string]json.RawMessage{
		"malformed command":        json.RawMessage(`{`),
		"malformed wrapper":        json.RawMessage(`{"message":42}`),
		"missing wrapper message":  json.RawMessage(`{}`),
		"invalid wrapped envelope": json.RawMessage(`{"message":{"jsonrpc":42,"id":"1","result":{}}}`),
		"unsupported version":      json.RawMessage(`{"jsonrpc":"1.0","id":"1","result":{}}`),
		"missing result or error":  json.RawMessage(`{"jsonrpc":"2.0","id":"1"}`),
		"both result and error":    json.RawMessage(`{"jsonrpc":"2.0","id":"1","result":{},"error":{"code":1}}`),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := session.handleRPC(body)
			if err == nil {
				t.Fatal("invalid RPC command was accepted")
			}
		})
	}

	handled, err := session.handleRPC(json.RawMessage(`{"jsonrpc":"2.0","method":"future.request"}`))
	if err != nil || handled {
		t.Fatalf("server RPC request handling = %t, %v", handled, err)
	}

	for name, test := range map[string]struct {
		method string
		speed  *float64
	}{
		"unknown method":           {method: "PTZ.Unknown", speed: nil},
		"step with speed":          {method: "PTZ.Pan.Step", speed: pointer(0.5)},
		"continuous without speed": {method: "PTZ.Pan.Continuous", speed: nil},
	} {
		t.Run("command "+name, func(t *testing.T) {
			t.Parallel()

			_, err := session.Call(context.Background(), test.method, generatedsignaling.PtzDirectionRight, test.speed)
			if err == nil {
				t.Fatal("invalid PTZ command was accepted")
			}
		})
	}

	select {
	case message := <-out:
		t.Fatalf("invalid command reached peer: %s", message.Method)
	default:
	}
}

func TestCallSendFailuresPreserveCallerAndTransportCauses(t *testing.T) {
	t.Parallel()

	t.Run("caller cancellation", func(t *testing.T) {
		t.Parallel()

		entered := make(chan struct{})

		session, err := NewSession(context.Background(), SessionConfig{
			DeviceID:  1001,
			DialogID:  "dialog",
			SignalID:  "signal",
			ControlID: "control",
			Heartbeat: time.Second,
			MaxAge:    time.Minute,
			Clock:     newClock(),
			Send: func(ctx context.Context, _ Message) error {
				close(entered)
				<-ctx.Done()

				return ctx.Err()
			},
		})
		if err != nil {
			t.Fatal(err)
		}

		defer func() { _ = session.Close() }()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		callDone := make(chan error, 1)

		go func() {
			_, callErr := session.Call(ctx, "PTZ.Pan.Step", generatedsignaling.PtzDirectionLeft, nil)
			callDone <- callErr
		}()

		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("RPC write did not start")
		}

		cancel()

		select {
		case callErr := <-callDone:
			if !errors.Is(callErr, context.Canceled) {
				t.Fatalf("caller cancellation error = %v", callErr)
			}
		case <-time.After(time.Second):
			t.Fatal("RPC did not return after caller cancellation")
		}
	})

	t.Run("transport error", func(t *testing.T) {
		t.Parallel()

		transportErr := syntheticSocketFailureError{}

		session, err := NewSession(context.Background(), SessionConfig{
			DeviceID:  1001,
			DialogID:  "dialog",
			SignalID:  "signal",
			ControlID: "control",
			Heartbeat: time.Second,
			MaxAge:    time.Minute,
			Clock:     newClock(),
			Send: func(context.Context, Message) error {
				return transportErr
			},
		})
		if err != nil {
			t.Fatal(err)
		}

		defer func() { _ = session.Close() }()

		_, err = session.Call(context.Background(), "PTZ.Pan.Step", generatedsignaling.PtzDirectionLeft, nil)
		if !errors.Is(err, transportErr) {
			t.Fatalf("transport error = %v", err)
		}
	})
}

//nolint:paralleltest // This test temporarily mutates generated package-level method state.
func TestContinuousCommandRejectsMissingGeneratedProjection(t *testing.T) {
	const method = "PTZ.Pan.Continuous"

	projection, exists := generatedsignaling.ValuesToAnonymousSchema_199[method]
	if !exists {
		t.Fatal("generated continuous projection is already missing")
	}

	delete(generatedsignaling.ValuesToAnonymousSchema_199, method)

	defer func() {
		generatedsignaling.ValuesToAnonymousSchema_199[method] = projection
	}()

	session, _, out := setupSession(t)
	speed := 0.5

	_, err := session.Call(context.Background(), method, generatedsignaling.PtzDirectionRight, &speed)
	if err == nil {
		t.Fatal("continuous command without a generated method projection was accepted")
	}

	select {
	case message := <-out:
		t.Fatalf("unprojected command reached peer: %s", message.Method)
	default:
	}
}

func pointer(value float64) *float64 { return &value }
