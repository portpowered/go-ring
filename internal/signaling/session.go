package signaling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/portpowered/go-ring/internal/protocol"
	"sync"
	"time"
)

var (
	ErrClosed       = errors.New("session closed")
	ErrExpired      = errors.New("session reached maximum age")
	ErrHeartbeat    = errors.New("session heartbeat timed out")
	ErrBackpressure = errors.New("session event queue full")
)

// Clock allows deterministic deadlines without waiting real session lifetimes.
type Clock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}
type RealClock struct{}

func (RealClock) Now() time.Time                         { return time.Now() }
func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

type Message struct {
	Method   string          `json:"method"`
	DialogID string          `json:"dialog_id"`
	RIID     string          `json:"riid,omitempty"`
	Body     json.RawMessage `json:"body"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("session RPC error %d", e.Code) }

type rpcReply struct {
	result json.RawMessage
	err    error
}

// Session owns an activated device's routing and RPC state. Negotiation and the
// single socket reader belong to the connection. Send must honor its context.
type Session struct {
	mu                            sync.Mutex
	ctx                           context.Context
	cancel                        context.CancelFunc
	done                          chan struct{}
	workers                       sync.WaitGroup
	clock                         Clock
	send                          func(context.Context, Message) error
	deviceID                      int64
	dialogID, signalID, controlID string
	lastPong                      time.Time
	expiresAt                     time.Time
	terminal                      error
	closed                        bool
	sequence                      uint64
	pending                       map[string]chan rpcReply
	events                        chan Message
	writeGate                     chan struct{}
}

type SessionConfig struct {
	DeviceID                      int64
	DialogID, SignalID, ControlID string
	Heartbeat, MaxAge             time.Duration
	Clock                         Clock
	Send                          func(context.Context, Message) error
}

func NewSession(ctx context.Context, c SessionConfig) (*Session, error) {
	if c.DeviceID <= 0 || c.DialogID == "" || c.SignalID == "" || c.ControlID == "" || c.SignalID == c.ControlID || c.Send == nil {
		return nil, fmt.Errorf("invalid session configuration")
	}
	if c.Heartbeat <= 0 || c.Heartbeat > MaxHeartbeatInterval {
		return nil, fmt.Errorf("invalid heartbeat interval")
	}
	if c.MaxAge == 0 {
		c.MaxAge = MaxSessionAge
	}
	if c.MaxAge <= 0 || c.MaxAge > MaxSessionAge {
		return nil, fmt.Errorf("invalid maximum session age")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.Clock == nil {
		c.Clock = RealClock{}
	}
	child, cancel := context.WithCancel(ctx)
	s := &Session{ctx: child, cancel: cancel, done: make(chan struct{}), clock: c.Clock, send: c.Send, deviceID: c.DeviceID, dialogID: c.DialogID, signalID: c.SignalID, controlID: c.ControlID, lastPong: c.Clock.Now(), expiresAt: c.Clock.Now().Add(c.MaxAge), pending: make(map[string]chan rpcReply), events: make(chan Message, EventQueueCapacity), writeGate: make(chan struct{}, 1)}
	// Create timers before returning so fake-clock advances cannot race startup.
	expiry := c.Clock.After(c.MaxAge)
	tick := c.Clock.After(c.Heartbeat)
	s.workers.Add(2)
	go func() {
		defer s.workers.Done()
		select {
		case <-s.ctx.Done():
			s.finish(s.ctx.Err())
		case <-expiry:
			s.finish(ErrExpired)
		}
	}()
	go func() {
		defer s.workers.Done()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-tick:
				s.mu.Lock()
				age := s.clock.Now().Sub(s.lastPong)
				s.mu.Unlock()
				if age >= MissedHeartbeatIntervals*c.Heartbeat {
					s.finish(ErrHeartbeat)
					return
				}
				// Arm the next tick before sending to make the observable send a barrier.
				tick = s.clock.After(c.Heartbeat)
				if err := s.Send(s.ctx, protocol.MethodPing, nil); err != nil {
					s.finish(err)
					return
				}
			}
		}
	}()
	return s, nil
}

func (s *Session) finish(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.terminal = err
	for id, ch := range s.pending {
		ch <- rpcReply{err: err}
		delete(s.pending, id)
	}
	s.cancel()
	close(s.done)
}

func (s *Session) Wait(ctx context.Context) error {
	select {
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.terminal
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Session) Close() error { s.finish(ErrClosed); s.workers.Wait(); return nil }

// Fail terminates from the connection reader without joining any worker.
func (s *Session) Fail(err error) {
	if err == nil {
		err = ErrClosed
	}
	s.finish(err)
}
func (s *Session) Pending() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.pending) }

func (s *Session) Send(ctx context.Context, method string, fields map[string]any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case s.writeGate <- struct{}{}:
		defer func() { <-s.writeGate }()
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return s.Wait(context.Background())
	}
	s.mu.Lock()
	closed := s.closed
	terminal := s.terminal
	s.mu.Unlock()
	if closed {
		return terminal
	}
	if !s.clock.Now().Before(s.expiresAt) {
		s.finish(ErrExpired)
		return ErrExpired
	}
	body := make(map[string]any, len(fields)+2)
	for k, v := range fields {
		body[k] = v
	}
	// Routing fields cannot be overridden by command payloads.
	body["doorbot_id"] = s.deviceID
	body["session_id"] = s.signalID
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("invalid session payload")
	}
	writeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	return s.send(writeCtx, Message{Method: method, DialogID: s.dialogID, Body: encoded})
}

// Call registers correlation before sending. A result acknowledges the command;
// it does not claim physical motion completed. Calls are never retried.
func (s *Session) Call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Every RPC has a bounded result wait even when the caller provides no
	// deadline. The injected clock also cancels a queued or blocked write.
	callCtx, cancel := context.WithCancelCause(ctx)
	timeout := s.clock.After(RPCResponseTimeout)
	finished, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-finished:
		case <-s.done:
			cancel(s.Wait(context.Background()))
		case <-callCtx.Done():
		case <-timeout:
			cancel(context.DeadlineExceeded)
		}
	}()
	defer func() { close(finished); cancel(nil); <-stopped }()
	ctx = callCtx
	s.mu.Lock()
	if s.closed {
		err := s.terminal
		s.mu.Unlock()
		return nil, err
	}
	s.sequence++
	id := fmt.Sprintf("%s-%d", s.controlID, s.sequence)
	ch := make(chan rpcReply, 1)
	s.pending[id] = ch
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pending, id); s.mu.Unlock() }()
	p := make(map[string]any, len(params)+3)
	for k, v := range params {
		p[k] = v
	}
	p["sessionId"] = s.controlID
	p["timestamp"] = s.clock.Now().UnixMilli()
	p["version"] = protocol.PTZVersion
	err := s.Send(ctx, protocol.MethodRPC, map[string]any{"command": map[string]any{"jsonrpc": protocol.JSONRPCVersion, "id": id, "method": method, "params": p}})
	if err != nil {
		if cause := context.Cause(callCtx); cause != nil {
			return nil, cause
		}
		// The session may cancel the write before the call's cancellation
		// watcher runs. Preserve its terminal cause rather than leaking the
		// internal context.Canceled used only to interrupt the transport.
		select {
		case <-s.done:
			return nil, s.Wait(context.Background())
		default:
		}
		return nil, err
	}
	select {
	case reply := <-ch:
		return reply.result, reply.err
	case <-ctx.Done():
		return nil, context.Cause(callCtx)
	case <-s.done:
		return nil, s.Wait(context.Background())
	}
}

// Handle is called by the connection's sole reader. Wrong-session messages are
// ignored; they must not satisfy a pending request or renew its heartbeat.
func (s *Session) Handle(m Message) error {
	if m.DialogID != s.dialogID {
		return nil
	}
	var body struct {
		DeviceID int64           `json:"doorbot_id"`
		SignalID string          `json:"session_id"`
		Command  json.RawMessage `json:"command"`
	}
	if err := json.Unmarshal(m.Body, &body); err != nil {
		return fmt.Errorf("invalid session body")
	}
	if body.DeviceID != s.deviceID || body.SignalID != s.signalID {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return s.terminal
	}
	if m.Method == protocol.MethodPong {
		s.lastPong = s.clock.Now()
		return nil
	}
	if m.Method == protocol.MethodRPC {
		var command struct {
			ID      string          `json:"id"`
			Version string          `json:"jsonrpc"`
			Method  string          `json:"method"`
			Result  json.RawMessage `json:"result"`
			Error   *RPCError       `json:"error"`
		}
		if err := json.Unmarshal(body.Command, &command); err != nil || command.Version != protocol.JSONRPCVersion {
			return fmt.Errorf("invalid RPC envelope")
		}
		if command.Method == "" {
			if (len(command.Result) == 0) == (command.Error == nil) {
				return fmt.Errorf("RPC reply must have exactly one result or error")
			}
			if command.Error == nil {
				var result struct {
					SessionID string `json:"sessionId"`
				}
				if err := json.Unmarshal(command.Result, &result); err != nil || result.SessionID == "" {
					return fmt.Errorf("invalid RPC result identity")
				}
				if result.SessionID != s.controlID {
					return nil
				}
			}
			if ch, ok := s.pending[command.ID]; ok {
				reply := rpcReply{result: command.Result}
				if command.Error != nil {
					reply.err = command.Error
				}
				ch <- reply
				delete(s.pending, command.ID)
			}
			return nil
		}
	}
	select {
	case s.events <- m:
		return nil
	default:
		return ErrBackpressure
	}
}

func (s *Session) Receive(ctx context.Context) (Message, error) {
	select {
	case m := <-s.events:
		return m, nil
	case <-s.done:
		return Message{}, s.Wait(context.Background())
	case <-ctx.Done():
		return Message{}, ctx.Err()
	}
}
