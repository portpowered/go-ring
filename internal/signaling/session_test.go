package signaling

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

type alarm struct {
	when time.Time
	ch   chan time.Time
}
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	alarms []alarm
}

type rpcCommandEnvelope struct {
	Command rpcCommand `json:"command"`
}

type rpcCommand struct {
	ID     string         `json:"id"`
	Params map[string]any `json:"params"`
}

type syntheticSocketFailureError struct{}

func (syntheticSocketFailureError) Error() string { return "synthetic socket failure" }

func newClock() *fakeClock { return &fakeClock{now: time.Unix(1700000000, 0)} }
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}
func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	ch := make(chan time.Time, 1)
	c.alarms = append(c.alarms, alarm{c.now.Add(d), ch})

	return ch
}
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)

	var keep []alarm

	for _, a := range c.alarms {
		if !a.when.After(c.now) {
			a.ch <- c.now
		} else {
			keep = append(keep, a)
		}
	}

	c.alarms = keep
}
func setupSession(t *testing.T) (*Session, *fakeClock, chan Message) {
	t.Helper()

	clock := newClock()
	out := make(chan Message, 16)

	session, err := NewSession(
		context.Background(),
		SessionConfig{
			DeviceID:  1001,
			DialogID:  "dialog",
			SignalID:  "signal",
			ControlID: "control",
			Heartbeat: 10 * time.Second,
			Clock:     clock,
			Send: func(ctx context.Context, message Message) error {
				select {
				case out <- message:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		err := session.Close()
		if err != nil {
			t.Error(err)
		}
	})

	return session, clock, out
}
func nextMessage(t *testing.T, out chan Message) Message {
	t.Helper()

	select {
	case message := <-out:
		return message
	case <-time.After(time.Second):
		t.Fatal("no outbound message")

		return Message{}
	}
}
func reply(t *testing.T, session *Session, out Message, signal string) {
	t.Helper()

	var body rpcCommandEnvelope
	{
		err := json.Unmarshal(out.Body, &body)
		if err != nil {
			t.Fatal(err)
		}
	}

	if body.Command.Params["sessionId"] != "control" {
		t.Fatal("control identity overwritten")
	}

	raw, err := json.Marshal(
		map[string]any{
			"doorbot_id": 1001,
			"session_id": signal,
			"command": map[string]any{
				"jsonrpc": "2.0",
				"id":      body.Command.ID,
				"result":  map[string]any{"sessionId": "control", "timestamp": 1700000000000, "version": 1},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	{
		err := session.Handle(Message{Method: "rpc", DialogID: "dialog", Body: raw})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestWrappedRPCReplyResolvesPendingPTZ(t *testing.T) {
	t.Parallel()

	session, _, out := setupSession(t)
	done := make(chan error, 1)

	go func() {
		_, err := session.Call(context.Background(), "PTZ.Pan.Step", map[string]any{"direction": "LEFT"})
		done <- err
	}()

	request := nextMessage(t, out)

	var body rpcCommandEnvelope

	{
		err := json.Unmarshal(request.Body, &body)
		if err != nil {
			t.Fatal(err)
		}
	}

	replyBody, err := json.Marshal(map[string]any{
		"doorbot_id": 1001,
		"session_id": "signal",
		"command": map[string]any{
			"destination": "client",
			"protocol":    "jsonrpc",
			"message": map[string]any{
				"jsonrpc": "2.0",
				"id":      body.Command.ID,
				"result":  map[string]any{"sessionId": "control"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	{
		err := session.Handle(Message{Method: "rpc", DialogID: "dialog", Body: replyBody})
		if err != nil {
			t.Fatal(err)
		}
	}

	{
		err := <-done
		if err != nil {
			t.Fatal(err)
		}
	}

	if session.Pending() != 0 {
		t.Fatal("wrapped PTZ reply left a pending call")
	}
}

func TestRPCCorrelationCancellationAndLateReply(t *testing.T) {
	t.Parallel()

	session, _, out := setupSession(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)

	go func() { _, err := session.Call(ctx, "PTZ.Pan.Step", map[string]any{"direction": "LEFT"}); done <- err }()

	requestMessage := nextMessage(t, out)
	reply(t, session, requestMessage, "other-session")

	if session.Pending() != 1 {
		t.Fatal("wrong session resolved call")
	}

	cancel()

	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	if session.Pending() != 0 {
		t.Fatal("canceled call leaked")
	}

	reply(t, session, requestMessage, "signal")

	go func() {
		_, err := session.Call(context.Background(), "PTZ.Pan.Step", map[string]any{"direction": "RIGHT"})
		done <- err
	}()

	next := nextMessage(t, out)
	if string(next.Body) == string(requestMessage.Body) {
		t.Fatal("reused command identity")
	}

	reply(t, session, next, "signal")

	err = <-done
	if err != nil {
		t.Fatal(err)
	}

	reply(t, session, next, "signal") // duplicate result must not block the reader
}

func TestHeartbeatAndHardExpiry(t *testing.T) {
	t.Parallel()

	session, clock, out := setupSession(t)
	clock.advance(10 * time.Second)

	if nextMessage(t, out).Method != "ping" {
		t.Fatal("missing ping")
	}

	err := session.Handle(
		Message{Method: "pong", DialogID: "dialog", Body: json.RawMessage(`{"doorbot_id":1001,"session_id":"signal"}`)},
	)
	if err != nil {
		t.Fatal(err)
	}
	// Keep replying for the full virtual hour. Active traffic must not renew
	// the hard age limit, and no private timer fields are modified by the test.
	for elapsed := 20 * time.Second; elapsed < MaxSessionAge; elapsed += 10 * time.Second {
		clock.advance(10 * time.Second)
		nextMessage(t, out)

		err := session.Handle(
			Message{
				Method:   "pong",
				DialogID: "dialog",
				Body:     json.RawMessage(`{"doorbot_id":1001,"session_id":"signal"}`),
			},
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	clock.advance(10 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err = session.Wait(ctx)
	if !errors.Is(err, ErrExpired) {
		t.Fatal(err)
	}
}

func TestUnmatchedPongDoesNotExtendLiveness(t *testing.T) {
	t.Parallel()

	session, clock, out := setupSession(t)
	for range 2 {
		clock.advance(10 * time.Second)
		nextMessage(t, out)

		_ = session.Handle(
			Message{
				Method:   "pong",
				DialogID: "wrong",
				Body:     json.RawMessage(`{"doorbot_id":1001,"session_id":"signal"}`),
			},
		)
	}

	clock.advance(10 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := session.Wait(ctx)
	if !errors.Is(err, ErrHeartbeat) {
		t.Fatal(err)
	}
}

func TestExpiryCancelsBlockedWriteAndPendingRPC(t *testing.T) {
	t.Parallel()

	clock := newClock()
	entered := make(chan struct{}, 1)

	session, err := NewSession(
		context.Background(),
		SessionConfig{
			DeviceID:  1,
			DialogID:  "d",
			SignalID:  "s",
			ControlID: "c",
			Heartbeat: time.Second,
			Clock:     clock,
			Send: func(ctx context.Context, _ Message) error {
				entered <- struct{}{}
				<-ctx.Done()

				return ctx.Err()
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = session.Close() }()

	done := make(chan error, 1)

	go func() { _, err := session.Call(context.Background(), "PTZ.Pan.Step", nil); done <- err }()

	<-entered
	clock.advance(MaxSessionAge)

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("blocked write succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("expiry did not interrupt write")
	}

	if session.Pending() != 0 {
		t.Fatal("pending RPC leaked")
	}
}

func TestEventsBackpressureAndClose(t *testing.T) {
	t.Parallel()

	session, _, _ := setupSession(t)

	event := Message{
		Method:   "rpc",
		DialogID: "dialog",
		Body: json.RawMessage(
			`{"doorbot_id":1001,"session_id":"signal"` +
				`,"command":{"jsonrpc":"2.0","id":"notice` +
				`","method":"PTZ.Pan.Halted","params":{"r` +
				`eason":"LIMIT_REACHED"}}}`,
		),
	}

	for range 32 {
		err := session.Handle(event)
		if err != nil {
			t.Fatal(err)
		}
	}

	if !errors.Is(session.Handle(event), ErrBackpressure) {
		t.Fatal("missing backpressure")
	}

	receivedMessage, err := session.Receive(context.Background())
	if err != nil || receivedMessage.Method != "rpc" {
		t.Fatal("lost event", err)
	}

	{
		err := session.Close()
		if err != nil {
			t.Fatal(err)
		}
	}

	{
		err := session.Close()
		if err != nil {
			t.Fatal(err)
		}
	}

	{
		err := session.Send(context.Background(), "ping", nil)
		if !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
	}
}

func TestRPCDefaultDeadlineAndProtocolError(t *testing.T) {
	t.Parallel()

	session, clock, out := setupSession(t)
	done := make(chan error, 1)

	go func() {
		_, err := session.Call(context.Background(), "PTZ.Tilt.Step", map[string]any{"direction": "UP"})
		done <- err
	}()

	requestMessage := nextMessage(t, out)

	var body map[string]any

	{
		err := json.Unmarshal(requestMessage.Body, &body)
		if err != nil {
			t.Fatal(err)
		}
	}

	command, ok := body["command"].(map[string]any)
	if !ok {
		t.Fatalf("RPC command has type %T, want object", body["command"])
	}

	delete(command, "method")
	delete(command, "params")
	command["error"] = map[string]any{"code": -32602, "message": "invalid direction"}

	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}

	{
		err := session.Handle(Message{Method: "rpc", DialogID: "dialog", Body: encoded})
		if err != nil {
			t.Fatal(err)
		}
	}

	var rpcError *RPCError
	{
		err := <-done
		if !errors.As(err, &rpcError) || rpcError.Code != -32602 {
			t.Fatal("lost RPC error", err)
		}
	}

	go func() { _, err := session.Call(context.Background(), "PTZ.Tilt.Step", nil); done <- err }()

	nextMessage(t, out)
	clock.advance(10 * time.Second)

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("RPC default deadline not enforced")
	}

	if session.Pending() != 0 {
		t.Fatal("RPC timeout leaked pending entry")
	}
}

func TestRPCResultRequiresControlSessionIdentity(t *testing.T) {
	t.Parallel()

	session, _, out := setupSession(t)
	result := make(chan error, 1)

	go func() {
		_, err := session.Call(context.Background(), "PTZ.Pan.Step", map[string]any{"direction": "LEFT"})
		result <- err
	}()

	sent := nextMessage(t, out)

	var body rpcCommandEnvelope

	err := json.Unmarshal(sent.Body, &body)
	if err != nil {
		t.Fatal(err)
	}

	message := func(value any) Message {
		raw, err := json.Marshal(
			map[string]any{
				"doorbot_id": 1001,
				"session_id": "signal",
				"command":    map[string]any{"jsonrpc": "2.0", "id": body.Command.ID, "result": value},
			},
		)
		if err != nil {
			t.Fatal(err)
		}

		return Message{Method: "rpc", DialogID: "dialog", Body: raw}
	}

	err = session.Handle(message(map[string]any{"sessionId": "another-control"}))
	if err != nil {
		t.Fatal(err)
	}

	if session.Pending() != 1 {
		t.Fatal("cross-control result resolved the command")
	}

	for _, malformed := range []any{nil, "not an object", map[string]any{"sessionId": 42}, map[string]any{}} {
		err := session.Handle(message(malformed))
		if err == nil {
			t.Fatalf("accepted malformed result %v", malformed)
		}
	}

	if session.Pending() != 1 {
		t.Fatal("malformed result resolved the command")
	}

	reply(t, session, sent, "signal")

	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("valid reply did not resolve command")
	}
}

func TestSendRejectsExpiredSessionBeforeTimerDelivery(t *testing.T) {
	t.Parallel()

	session, clock, out := setupSession(t)
	// A clock may advance before the scheduler delivers its timers. Enforce the
	// absolute lifetime at the send boundary, independently of worker scheduling.
	clock.mu.Lock()
	clock.now = clock.now.Add(MaxSessionAge)
	clock.mu.Unlock()

	err := session.Send(context.Background(), "mic_enable", map[string]any{"enabled": true})
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("send = %v", err)
	}

	select {
	case outboundMessage := <-out:
		t.Fatalf("sent after expiry: %s", outboundMessage.Method)
	default:
	}

	err = session.Wait(context.Background())
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("terminal = %v", err)
	}
}

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
		_, err := session.Call(canceled, "PTZ.Pan.Step", nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}

	{
		_, err := session.Call(context.Background(), "PTZ.Pan.Step", map[string]any{"invalid": make(chan int)})
		if err == nil {
			t.Fatal("accepted unencodable command")
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

	err = session.Handle(Message{DialogID: "dialog", Method: "rpc", Body: json.RawMessage(`{`)})
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
		_, err := session.Call(context.Background(), "PTZ.Pan.Step", nil)
		if !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
	}

	err = session.Send(context.Background(), "ping", nil)
	if !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}

	err = session.Handle(
		Message{DialogID: "dialog", Method: "pong", Body: json.RawMessage(`{"doorbot_id":1001,"session_id":"signal"}`)},
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

	go func() { _, err := session.Call(context.Background(), "PTZ.Pan.Step", nil); result <- err }()

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
