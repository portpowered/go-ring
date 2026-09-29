package signaling

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/ringerrors"
)

type terminalError string

func (r terminalError) Error() string { return string(r) }

type sessionContextError struct {
	operation string
	cause     error
}

func (failure sessionContextError) Error() string {
	return failure.operation + ": " + failure.cause.Error()
}

func (failure sessionContextError) Unwrap() error { return failure.cause }

func wrapSessionContextError(operation string, cause error) error {
	if cause == nil {
		return nil
	}

	return sessionContextError{operation: operation, cause: cause}
}

var (
	ErrClosed       error = terminalError("session closed")
	ErrExpired      error = terminalError("session reached maximum age")
	ErrHeartbeat    error = terminalError("session heartbeat timed out")
	ErrBackpressure error = terminalError("session event queue full")
)

// Clock allows deterministic deadlines without waiting real session lifetimes.
type Clock interface {
	Now() time.Time
	After(duration time.Duration) <-chan time.Time
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

type sessionMessageBody struct {
	DeviceID int64           `json:"doorbot_id"`
	SignalID string          `json:"session_id"`
	Command  json.RawMessage `json:"command"`
}

type rpcCommandReply struct {
	ID      string          `json:"id"`
	Version string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Result  json.RawMessage `json:"result"`
	Error   *RPCError       `json:"error"`
}

type rpcCommandWrapper struct {
	Message json.RawMessage `json:"message"`
}

type rpcResultIdentity struct {
	SessionID string `json:"sessionId"`
}

type rpcCommandRequest struct {
	Version string         `json:"jsonrpc"`
	ID      string         `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
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
}

type SessionConfig struct {
	DeviceID                      int64
	DialogID, SignalID, ControlID string
	Heartbeat, MaxAge             time.Duration
	Clock                         Clock
	Send                          func(context.Context, Message) error
}

func NewSession(ctx context.Context, config SessionConfig) (*Session, error) {
	if config.DeviceID <= 0 ||
		config.DialogID == "" ||
		config.SignalID == "" ||
		config.ControlID == "" ||
		config.SignalID == config.ControlID ||
		config.Send == nil {
		return nil, ringerrors.NewBadRequestError("invalid session configuration", nil)
	}

	if config.Heartbeat <= 0 || config.Heartbeat > MaxHeartbeatInterval {
		return nil, ringerrors.NewBadRequestError("invalid heartbeat interval", nil)
	}

	if config.MaxAge == 0 {
		config.MaxAge = MaxSessionAge
	}

	if config.MaxAge <= 0 || config.MaxAge > MaxSessionAge {
		return nil, ringerrors.NewBadRequestError("invalid maximum session age", nil)
	}

	err := ctx.Err()
	if err != nil {
		return nil, wrapSessionContextError("create signaling session", err)
	}

	if config.Clock == nil {
		config.Clock = RealClock{}
	}

	child, cancel := context.WithCancel(ctx)
	session := &Session{
		ctx:       child,
		cancel:    cancel,
		done:      make(chan struct{}),
		clock:     config.Clock,
		send:      config.Send,
		deviceID:  config.DeviceID,
		dialogID:  config.DialogID,
		signalID:  config.SignalID,
		controlID: config.ControlID,
		lastPong:  config.Clock.Now(),
		expiresAt: config.Clock.Now().Add(config.MaxAge),
		pending:   make(map[string]chan rpcReply),
		events:    make(chan Message, EventQueueCapacity),
	}
	// Create timers before returning so fake-clock advances cannot race startup.
	expiry := config.Clock.After(config.MaxAge)
	tick := config.Clock.After(config.Heartbeat)

	session.workers.Add(2)

	go func() {
		defer session.workers.Done()

		select {
		case <-session.ctx.Done():
			session.finish(session.ctx.Err())
		case <-expiry:
			session.finish(ErrExpired)
		}
	}()
	go func() {
		defer session.workers.Done()

		for {
			select {
			case <-session.ctx.Done():
				return
			case <-tick:
				session.mu.Lock()
				age := session.clock.Now().Sub(session.lastPong)
				session.mu.Unlock()

				if age >= MissedHeartbeatIntervals*config.Heartbeat {
					session.finish(ErrHeartbeat)

					return
				}
				// Arm the next tick before sending to make the observable send a barrier.
				tick = session.clock.After(config.Heartbeat)

				err := session.Send(session.ctx, protocol.MethodPing, nil)
				if err != nil {
					session.finish(err)

					return
				}
			}
		}
	}()

	return session, nil
}

func (s *Session) Wait(ctx context.Context) error {
	select {
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()

		return s.terminal
	case <-ctx.Done():
		return wrapSessionContextError("wait for signaling session", ctx.Err())
	}
}
func (s *Session) Close() error {
	s.finish(ErrClosed)
	s.workers.Wait()

	return nil
}

// Fail terminates from the connection reader without joining any worker.
func (s *Session) Fail(err error) {
	if err == nil {
		err = ErrClosed
	}

	s.finish(err)
}
func (s *Session) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.pending)
}

func (s *Session) Send(ctx context.Context, method string, fields map[string]any) error {
	{
		err := ctx.Err()
		if err != nil {
			return wrapSessionContextError("send signaling message", err)
		}
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
	body[protocol.FieldDeviceID] = s.deviceID
	body[protocol.FieldSessionID] = s.signalID

	encoded, err := json.Marshal(body)
	if err != nil {
		return ringerrors.NewInternalServerError("invalid session payload", err)
	}

	writeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// The session context must also cancel a write when the caller context is still active.
	//nolint:contextcheck // AfterFunc explicitly links the inherited write context to the session lifetime.
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()

	return s.send(writeCtx, Message{
		Method:   method,
		DialogID: s.dialogID,
		RIID:     "",
		Body:     encoded,
	})
}

// Call registers correlation before sending. A result acknowledges the command;
// it does not claim physical motion completed. Calls are never retried.
func (s *Session) Call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	{
		err := ctx.Err()
		if err != nil {
			return nil, wrapSessionContextError("start signaling RPC", err)
		}
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
			cancel(s.terminalCause())
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

	const rpcIdentityFields = 3

	rpcParams := make(map[string]any, len(params)+rpcIdentityFields)
	for k, v := range params {
		rpcParams[k] = v
	}

	rpcParams[protocol.FieldSessionIDRPC] = s.controlID
	rpcParams[protocol.FieldTimestamp] = s.clock.Now().UnixMilli()
	rpcParams[protocol.FieldVersion] = protocol.PTZVersion

	err := s.Send(
		ctx,
		protocol.MethodRPC,
		map[string]any{
			protocol.FieldCommand: rpcCommandRequest{
				Version: protocol.JSONRPCVersion,
				ID:      id,
				Method:  method,
				Params:  rpcParams,
			},
		},
	)
	if err != nil {
		cause := context.Cause(callCtx)
		if cause != nil {
			return nil, wrapSessionContextError("send signaling RPC", cause)
		}
		// The session may cancel the write before the call's cancellation
		// watcher runs. Preserve its terminal cause rather than leaking the
		// internal context.Canceled used only to interrupt the transport.
		select {
		case <-s.done:
			return nil, s.terminalCause()
		default:
		}

		return nil, err
	}

	select {
	case reply := <-ch:
		return reply.result, reply.err
	case <-ctx.Done():
		return nil, wrapSessionContextError("wait for signaling RPC reply", context.Cause(callCtx))
	case <-s.done:
		return nil, s.terminalCause()
	}
}

// Handle is called by the connection's sole reader. Wrong-session messages are
// ignored; they must not satisfy a pending request or renew its heartbeat.
func (s *Session) Handle(message Message) error {
	if message.DialogID != s.dialogID {
		return nil
	}

	var body sessionMessageBody

	err := json.Unmarshal(message.Body, &body)
	if err != nil {
		return ringerrors.NewConnectionError("invalid session body", err)
	}

	if body.DeviceID != s.deviceID || body.SignalID != s.signalID {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return s.terminal
	}

	if message.Method == protocol.MethodPong {
		s.lastPong = s.clock.Now()

		return nil
	}

	if message.Method == protocol.MethodRPC {
		handled, err := s.handleRPC(body.Command)
		if err != nil {
			return err
		}

		if handled {
			return nil
		}
	}

	select {
	case s.events <- message:
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
		return Message{}, s.terminalCause()
	case <-ctx.Done():
		return Message{}, wrapSessionContextError("receive signaling event", ctx.Err())
	}
}

func (s *Session) finish(err error) {
	s.mu.Lock()

	if s.closed {
		s.mu.Unlock()

		return
	}

	s.closed = true
	s.terminal = err
	pending := s.pending
	s.pending = make(map[string]chan rpcReply)
	s.mu.Unlock()

	for _, ch := range pending {
		select {
		case ch <- rpcReply{result: nil, err: err}:
		default:
		}
	}

	close(s.done)
	// Publish the terminal cause before canceling an in-flight transport write.
	// Otherwise that write can return context.Canceled before Call can observe
	// the actual session failure through done.
	s.cancel()
}

func (s *Session) terminalCause() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.terminal
}

func (s *Session) handleRPC(body json.RawMessage) (bool, error) {
	var command rpcCommandReply

	err := json.Unmarshal(body, &command)
	if err != nil {
		return false, ringerrors.NewConnectionError("invalid RPC envelope (command cannot be decoded)", err)
	}

	if command.Version == "" {
		var wrapper rpcCommandWrapper

		err := json.Unmarshal(body, &wrapper)
		if err == nil && len(wrapper.Message) > 0 {
			err := json.Unmarshal(wrapper.Message, &command)
			if err != nil {
				return false, ringerrors.NewConnectionError("invalid wrapped RPC envelope", err)
			}
		}
	}

	if command.Version != protocol.JSONRPCVersion {
		return false, ringerrors.NewConnectionError(
			"invalid RPC envelope (unsupported or missing jsonrpc version)",
			nil,
		)
	}

	if command.Method != "" {
		return false, nil
	}

	if (len(command.Result) == 0) == (command.Error == nil) {
		return false, ringerrors.NewConnectionError("RPC reply must have exactly one result or error", nil)
	}

	if command.Error == nil {
		var result rpcResultIdentity

		err := json.Unmarshal(command.Result, &result)
		if err != nil {
			return false, ringerrors.NewConnectionError("invalid RPC result identity", err)
		}

		if result.SessionID == "" {
			return false, ringerrors.NewConnectionError("invalid RPC result identity", nil)
		}

		if result.SessionID != s.controlID {
			return true, nil
		}
	}

	if ch, ok := s.pending[command.ID]; ok {
		reply := rpcReply{result: command.Result, err: nil}
		if command.Error != nil {
			reply.err = command.Error
		}

		ch <- reply

		delete(s.pending, command.ID)
	}

	return true, nil
}
