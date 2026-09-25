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

func newClock() *fakeClock          { return &fakeClock{now: time.Unix(1700000000, 0)} }
func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
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
	s, err := NewSession(context.Background(), SessionConfig{DeviceID: 1001, DialogID: "dialog", SignalID: "signal", ControlID: "control", Heartbeat: 10 * time.Second, Clock: clock, Send: func(ctx context.Context, m Message) error {
		select {
		case out <- m:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, clock, out
}
func nextMessage(t *testing.T, out chan Message) Message {
	t.Helper()
	select {
	case m := <-out:
		return m
	case <-time.After(time.Second):
		t.Fatal("no outbound message")
		return Message{}
	}
}
func reply(t *testing.T, s *Session, out Message, signal string) {
	t.Helper()
	var body struct {
		Command struct {
			ID     string         `json:"id"`
			Params map[string]any `json:"params"`
		} `json:"command"`
	}
	if err := json.Unmarshal(out.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Command.Params["sessionId"] != "control" {
		t.Fatal("control identity overwritten")
	}
	raw, _ := json.Marshal(map[string]any{"doorbot_id": 1001, "session_id": signal, "command": map[string]any{"jsonrpc": "2.0", "id": body.Command.ID, "result": map[string]any{"sessionId": "control", "timestamp": 1700000000000, "version": 1}}})
	if err := s.Handle(Message{Method: "rpc", DialogID: "dialog", Body: raw}); err != nil {
		t.Fatal(err)
	}
}

func TestRPCCorrelationCancellationAndLateReply(t *testing.T) {
	s, _, out := setupSession(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.Call(ctx, "PTZ.Pan.Step", map[string]any{"direction": "LEFT"}); done <- err }()
	m := nextMessage(t, out)
	reply(t, s, m, "other-session")
	if s.Pending() != 1 {
		t.Fatal("wrong session resolved call")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if s.Pending() != 0 {
		t.Fatal("canceled call leaked")
	}
	reply(t, s, m, "signal")
	go func() {
		_, err := s.Call(context.Background(), "PTZ.Pan.Step", map[string]any{"direction": "RIGHT"})
		done <- err
	}()
	next := nextMessage(t, out)
	if string(next.Body) == string(m.Body) {
		t.Fatal("reused command identity")
	}
	reply(t, s, next, "signal")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	reply(t, s, next, "signal") // duplicate result must not block the reader
}

func TestHeartbeatAndHardExpiry(t *testing.T) {
	s, c, out := setupSession(t)
	c.advance(10 * time.Second)
	if nextMessage(t, out).Method != "ping" {
		t.Fatal("missing ping")
	}
	if err := s.Handle(Message{Method: "pong", DialogID: "dialog", Body: json.RawMessage(`{"doorbot_id":1001,"session_id":"signal"}`)}); err != nil {
		t.Fatal(err)
	}
	// Keep replying for the full virtual hour. Active traffic must not renew
	// the hard age limit, and no private timer fields are modified by the test.
	for elapsed := 20 * time.Second; elapsed < MaxSessionAge; elapsed += 10 * time.Second {
		c.advance(10 * time.Second)
		nextMessage(t, out)
		if err := s.Handle(Message{Method: "pong", DialogID: "dialog", Body: json.RawMessage(`{"doorbot_id":1001,"session_id":"signal"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	c.advance(10 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Wait(ctx); !errors.Is(err, ErrExpired) {
		t.Fatal(err)
	}
}

func TestUnmatchedPongDoesNotExtendLiveness(t *testing.T) {
	s, c, out := setupSession(t)
	for i := 0; i < 2; i++ {
		c.advance(10 * time.Second)
		nextMessage(t, out)
		_ = s.Handle(Message{Method: "pong", DialogID: "wrong", Body: json.RawMessage(`{"doorbot_id":1001,"session_id":"signal"}`)})
	}
	c.advance(10 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Wait(ctx); !errors.Is(err, ErrHeartbeat) {
		t.Fatal(err)
	}
}

func TestExpiryCancelsBlockedWriteAndPendingRPC(t *testing.T) {
	c := newClock()
	entered := make(chan struct{}, 1)
	s, err := NewSession(context.Background(), SessionConfig{DeviceID: 1, DialogID: "d", SignalID: "s", ControlID: "c", Heartbeat: time.Second, Clock: c, Send: func(ctx context.Context, _ Message) error { entered <- struct{}{}; <-ctx.Done(); return ctx.Err() }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	done := make(chan error, 1)
	go func() { _, err := s.Call(context.Background(), "PTZ.Pan.Step", nil); done <- err }()
	<-entered
	c.advance(MaxSessionAge)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("blocked write succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("expiry did not interrupt write")
	}
	if s.Pending() != 0 {
		t.Fatal("pending RPC leaked")
	}
}

func TestEventsBackpressureAndClose(t *testing.T) {
	s, _, _ := setupSession(t)
	event := Message{Method: "rpc", DialogID: "dialog", Body: json.RawMessage(`{"doorbot_id":1001,"session_id":"signal","command":{"jsonrpc":"2.0","id":"notice","method":"PTZ.Pan.Halted","params":{"reason":"LIMIT_REACHED"}}}`)}
	for i := 0; i < 32; i++ {
		if err := s.Handle(event); err != nil {
			t.Fatal(err)
		}
	}
	if !errors.Is(s.Handle(event), ErrBackpressure) {
		t.Fatal("missing backpressure")
	}
	m, err := s.Receive(context.Background())
	if err != nil || m.Method != "rpc" {
		t.Fatal("lost event", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), "ping", nil); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestRPCDefaultDeadlineAndProtocolError(t *testing.T) {
	s, c, out := setupSession(t)
	done := make(chan error, 1)
	go func() {
		_, err := s.Call(context.Background(), "PTZ.Tilt.Step", map[string]any{"direction": "UP"})
		done <- err
	}()
	m := nextMessage(t, out)
	var body map[string]any
	if err := json.Unmarshal(m.Body, &body); err != nil {
		t.Fatal(err)
	}
	command := body["command"].(map[string]any)
	delete(command, "method")
	delete(command, "params")
	command["error"] = map[string]any{"code": -32602, "message": "invalid direction"}
	encoded, _ := json.Marshal(body)
	if err := s.Handle(Message{Method: "rpc", DialogID: "dialog", Body: encoded}); err != nil {
		t.Fatal(err)
	}
	var rpcError *RPCError
	if err := <-done; !errors.As(err, &rpcError) || rpcError.Code != -32602 {
		t.Fatal("lost RPC error", err)
	}
	go func() { _, err := s.Call(context.Background(), "PTZ.Tilt.Step", nil); done <- err }()
	nextMessage(t, out)
	c.advance(10 * time.Second)
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("RPC default deadline not enforced")
	}
	if s.Pending() != 0 {
		t.Fatal("RPC timeout leaked pending entry")
	}
}

func TestRPCResultRequiresControlSessionIdentity(t *testing.T) {
	s, _, out := setupSession(t)
	result := make(chan error, 1)
	go func() {
		_, err := s.Call(context.Background(), "PTZ.Pan.Step", map[string]any{"direction": "LEFT"})
		result <- err
	}()
	sent := nextMessage(t, out)
	var body struct {
		Command struct {
			ID string `json:"id"`
		} `json:"command"`
	}
	if err := json.Unmarshal(sent.Body, &body); err != nil {
		t.Fatal(err)
	}
	message := func(value any) Message {
		raw, _ := json.Marshal(map[string]any{"doorbot_id": 1001, "session_id": "signal", "command": map[string]any{"jsonrpc": "2.0", "id": body.Command.ID, "result": value}})
		return Message{Method: "rpc", DialogID: "dialog", Body: raw}
	}
	if err := s.Handle(message(map[string]any{"sessionId": "another-control"})); err != nil {
		t.Fatal(err)
	}
	if s.Pending() != 1 {
		t.Fatal("cross-control result resolved the command")
	}
	for _, malformed := range []any{nil, "not an object", map[string]any{"sessionId": 42}, map[string]any{}} {
		if err := s.Handle(message(malformed)); err == nil {
			t.Fatalf("accepted malformed result %v", malformed)
		}
	}
	if s.Pending() != 1 {
		t.Fatal("malformed result resolved the command")
	}
	reply(t, s, sent, "signal")
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
	s, clock, out := setupSession(t)
	// A clock may advance before the scheduler delivers its timers. Enforce the
	// absolute lifetime at the send boundary, independently of worker scheduling.
	clock.mu.Lock()
	clock.now = clock.now.Add(MaxSessionAge)
	clock.mu.Unlock()
	if err := s.Send(context.Background(), "mic_enable", map[string]any{"enabled": true}); !errors.Is(err, ErrExpired) {
		t.Fatalf("send = %v", err)
	}
	select {
	case m := <-out:
		t.Fatalf("sent after expiry: %s", m.Method)
	default:
	}
	if err := s.Wait(context.Background()); !errors.Is(err, ErrExpired) {
		t.Fatalf("terminal = %v", err)
	}
}
