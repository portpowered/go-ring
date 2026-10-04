package signaling

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/ringerrors"
	generatedsignaling "github.com/portpowered/go-ring/pkg/dependencymodels/signaling"
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
	Method   string
	DialogID string
	RIID     string
	Body     json.RawMessage
}

func (message Message) MarshalJSON() ([]byte, error) {
	method, exists := generatedsignaling.ValuesToAnonymousSchema_1[message.Method]
	if !exists {
		return nil, ringerrors.NewBadRequestError("unknown signaling method", nil)
	}

	var body map[string]interface{}
	if len(message.Body) > 0 {
		err := json.Unmarshal(message.Body, &body)
		if err != nil {
			return nil, ringerrors.NewBadRequestError("invalid signaling message body", err)
		}
	}

	envelope := generatedsignaling.SignalingInboundDiscriminator{
		Method:               &method,
		DialogId:             message.DialogID,
		Riid:                 message.RIID,
		Body:                 body,
		AdditionalProperties: nil,
	}

	encoded, err := json.Marshal(envelope)
	if err != nil {
		return nil, ringerrors.NewInternalServerError("encode signaling message", err)
	}

	return encoded, nil
}

func (message *Message) UnmarshalJSON(encoded []byte) error {
	var envelope generatedsignaling.SignalingInboundDiscriminator

	err := json.Unmarshal(encoded, &envelope)
	if err != nil {
		return ringerrors.NewConnectionError("invalid signaling envelope", err)
	}

	if envelope.Method == nil {
		return ringerrors.NewConnectionError("signaling envelope has no method", nil)
	}

	var fields map[string]json.RawMessage

	err = json.Unmarshal(encoded, &fields)
	if err != nil {
		return ringerrors.NewConnectionError("invalid signaling envelope fields", err)
	}

	var method string

	err = json.Unmarshal(fields[protocol.FieldMethod], &method)
	if err != nil {
		return ringerrors.NewConnectionError("invalid signaling method", err)
	}

	body, exists := fields[protocol.FieldBody]
	if !exists {
		return ringerrors.NewConnectionError("signaling envelope has no body", nil)
	}

	*message = Message{
		Method:   method,
		DialogID: envelope.DialogId,
		RIID:     envelope.Riid,
		Body:     append(json.RawMessage(nil), body...),
	}

	return nil
}

type RPCError struct {
	Code    int
	Message string
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

func (s *Session) Send(ctx context.Context, method string, body json.RawMessage) error {
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

	if len(body) == 0 {
		encoded, err := json.Marshal(generatedsignaling.SessionBody{
			DoorbotId:            int(s.deviceID),
			SessionId:            s.signalID,
			AdditionalProperties: nil,
		})
		if err != nil {
			return ringerrors.NewInternalServerError("invalid session payload", err)
		}

		body = encoded
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
		Body:     body,
	})
}

// Call registers correlation before sending. A result acknowledges the command;
// it does not claim physical motion completed. Calls are never retried.
func (s *Session) Call(
	ctx context.Context,
	method string,
	direction generatedsignaling.PtzDirection,
	speed *float64,
) (json.RawMessage, error) {
	err := ctx.Err()
	if err != nil {
		return nil, wrapSessionContextError("start signaling RPC", err)
	}

	commandMethod, continuousWireMethod, continuousMethod, err := validatePTZCommand(method, direction, speed)
	if err != nil {
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

	encodedBody, err := marshalPTZPayload(
		commandMethod,
		continuousWireMethod,
		continuousMethod,
		direction,
		speed,
		int(s.deviceID),
		s.signalID,
		s.controlID,
		id,
		int(s.clock.Now().UnixMilli()),
	)
	if err != nil {
		return nil, err
	}

	err = s.Send(ctx, protocol.MethodRPC, encodedBody)
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

func validatePTZCommand(
	method string,
	direction generatedsignaling.PtzDirection,
	speed *float64,
) (generatedsignaling.PtzCommandMethod, generatedsignaling.AnonymousSchema_199, bool, error) {
	commandMethod, exists := generatedsignaling.ValuesToPtzCommandMethod[method]
	if !exists || direction.Value() == nil {
		return 0, 0, false, ringerrors.NewBadRequestError("invalid PTZ command method or direction", nil)
	}

	continuous := commandMethod == generatedsignaling.PtzCommandMethodPtzDotPanDotContinuous ||
		commandMethod == generatedsignaling.PtzCommandMethodPtzDotTiltDotContinuous
	if continuous != (speed != nil) {
		return 0, 0, false, ringerrors.NewBadRequestError("PTZ speed must be supplied only for continuous commands", nil)
	}

	continuousMethod, exists := generatedsignaling.ValuesToAnonymousSchema_199[method]
	if continuous && !exists {
		return 0, 0, false, ringerrors.NewBadRequestError("invalid continuous PTZ command method", nil)
	}

	return commandMethod, continuousMethod, continuous, nil
}

func marshalPTZPayload(
	commandMethod generatedsignaling.PtzCommandMethod,
	continuousMethod generatedsignaling.AnonymousSchema_199,
	continuous bool,
	direction generatedsignaling.PtzDirection,
	speed *float64,
	deviceID int,
	signalID, controlID, id string,
	timestamp int,
) (json.RawMessage, error) {
	var payload any

	if continuous {
		params := generatedsignaling.PtzContinuousParams{
			SessionId:            controlID,
			Timestamp:            timestamp,
			Version:              protocol.PTZVersion,
			Direction:            &direction,
			Speed:                *speed,
			Reason:               "",
			AdditionalProperties: nil,
		}
		command := generatedsignaling.PtzContinuousWireCommand{
			Jsonrpc:              protocol.JSONRPCVersion,
			Id:                   id,
			Method:               &continuousMethod,
			Params:               &params,
			AdditionalProperties: nil,
		}
		payload = generatedsignaling.PtzContinuousCommandBody{
			DoorbotId:            deviceID,
			SessionId:            signalID,
			Command:              &command,
			AdditionalProperties: nil,
		}
	} else {
		params := generatedsignaling.PtzWireParams{
			SessionId:            controlID,
			Timestamp:            timestamp,
			Version:              protocol.PTZVersion,
			Direction:            &direction,
			Speed:                0,
			Reason:               "",
			AdditionalProperties: nil,
		}
		command := generatedsignaling.PtzWireCommand{
			Jsonrpc:              protocol.JSONRPCVersion,
			Id:                   id,
			Method:               &commandMethod,
			Params:               &params,
			AdditionalProperties: nil,
		}
		payload = generatedsignaling.PtzCommandBody{
			DoorbotId:            deviceID,
			SessionId:            signalID,
			Command:              &command,
			AdditionalProperties: nil,
		}
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, ringerrors.NewInternalServerError("invalid PTZ payload", err)
	}

	return encoded, nil
}

// Handle is called by the connection's sole reader. Wrong-session messages are
// ignored; they must not satisfy a pending request or renew its heartbeat.
func (s *Session) Handle(message Message) error {
	if message.DialogID != s.dialogID {
		return nil
	}

	var identity generatedsignaling.SessionBody

	err := json.Unmarshal(message.Body, &identity)
	if err != nil {
		return ringerrors.NewConnectionError("invalid session body", err)
	}

	if int64(identity.DoorbotId) != s.deviceID || identity.SessionId != s.signalID {
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
		handled, err := s.handleRPCFrame(message.Body)
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

func (s *Session) handleRPCFrame(body json.RawMessage) (bool, error) {
	var rpcBody generatedsignaling.ServerRpcBody

	err := json.Unmarshal(body, &rpcBody)
	if err != nil {
		return false, ringerrors.NewConnectionError("invalid RPC body", err)
	}

	var rpcFields map[string]json.RawMessage

	err = json.Unmarshal(body, &rpcFields)
	if err != nil {
		return false, ringerrors.NewConnectionError("invalid RPC body", err)
	}

	commandBody := rpcFields[protocol.FieldCommand]
	if rpcBody.Command == nil || len(commandBody) == 0 {
		return false, ringerrors.NewConnectionError("invalid RPC body", nil)
	}

	return s.handleRPC(commandBody)
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
	var command generatedsignaling.ServerRpcCommand

	err := json.Unmarshal(body, &command)
	if err != nil {
		return false, ringerrors.NewConnectionError("invalid RPC envelope (command cannot be decoded)", err)
	}

	if command.Jsonrpc == "" {
		var wrapper generatedsignaling.RpcCommandWrapper

		err = json.Unmarshal(body, &wrapper)
		if err != nil || wrapper.Message == nil {
			return false, ringerrors.NewConnectionError("invalid RPC wrapper", err)
		}

		body, err = json.Marshal(wrapper.Message)
		if err != nil {
			return false, ringerrors.NewConnectionError("invalid RPC wrapper message", err)
		}

		err = json.Unmarshal(body, &command)
		if err != nil {
			return false, ringerrors.NewConnectionError("invalid wrapped RPC envelope", err)
		}
	}

	if command.Jsonrpc != protocol.JSONRPCVersion {
		return false, ringerrors.NewConnectionError(
			"invalid RPC envelope (unsupported or missing jsonrpc version)",
			nil,
		)
	}

	if command.Method != "" {
		return false, nil
	}

	if (command.Result == nil) == (command.Error == nil) {
		return false, ringerrors.NewConnectionError("RPC reply must have exactly one result or error", nil)
	}

	if command.Error == nil {
		var resultFields map[string]json.RawMessage

		err := json.Unmarshal(body, &resultFields)
		if err != nil {
			return false, ringerrors.NewConnectionError("invalid RPC result", err)
		}

		var result generatedsignaling.RpcResultValue

		err = json.Unmarshal(resultFields[protocol.FieldResult], &result)
		if err != nil {
			return false, ringerrors.NewConnectionError("invalid RPC result identity", err)
		}

		if result.SessionId == "" {
			return false, ringerrors.NewConnectionError("invalid RPC result identity", nil)
		}

		if result.SessionId != s.controlID {
			return true, nil
		}
	}

	if ch, ok := s.pending[command.Id]; ok {
		var result json.RawMessage

		if command.Error == nil {
			var resultFields map[string]json.RawMessage

			err = json.Unmarshal(body, &resultFields)
			if err == nil {
				result = append(json.RawMessage(nil), resultFields[protocol.FieldResult]...)
			}
		}

		reply := rpcReply{result: result, err: nil}
		if command.Error != nil {
			reply.err = &RPCError{Code: command.Error.Code, Message: command.Error.Message}
		}

		ch <- reply

		delete(s.pending, command.Id)
	}

	return true, nil
}
