package ring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/signaling"
	"github.com/portpowered/go-ring/pkg/generatedsignaling"
)

type SessionState string

var (
	ErrSessionClosed       = signaling.ErrClosed
	ErrSessionExpired      = signaling.ErrExpired
	ErrSessionHeartbeat    = signaling.ErrHeartbeat
	ErrSessionBackpressure = signaling.ErrBackpressure
)

const (
	SessionActive  SessionState = "active"
	SessionClosed  SessionState = "closed"
	SessionExpired SessionState = "expired"
	SessionFailed  SessionState = "failed"
)

type SessionDescription struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}
type StartDeviceSessionRequest struct {
	DeviceID     string
	Offer        SessionDescription
	AudioEnabled bool
	VideoEnabled bool
	MaxAge       time.Duration
	ICEMode      ICECandidateMode
}
type ICECandidateMode string

const (
	ICETrickle    ICECandidateMode = "trickle"
	ICENonTrickle ICECandidateMode = "non_trickle"
)

type ICECandidateRequest struct {
	Candidate  string
	MID        string
	MLineIndex int
}
type PanDirection string
type TiltDirection string

const (
	PanLeft  PanDirection  = protocol.PanLeft
	PanRight PanDirection  = protocol.PanRight
	TiltUp   TiltDirection = protocol.TiltUp
	TiltDown TiltDirection = protocol.TiltDown
)

type PanStepRequest struct{ Direction PanDirection }
type TiltStepRequest struct{ Direction TiltDirection }
type PanContinuousRequest struct {
	Direction PanDirection
	Speed     float64
}
type TiltContinuousRequest struct {
	Direction TiltDirection
	Speed     float64
}
type PTZAxis string

const (
	PanAxis  PTZAxis = "pan"
	TiltAxis PTZAxis = "tilt"
)

type StopPTZRequest struct{ Axis PTZAxis }
type SetMicrophoneRequest struct{ Enabled bool }
type SetStreamOptionsRequest struct {
	AudioEnabled *bool
	VideoEnabled *bool
}
type SessionEvent struct {
	Method string
	Body   json.RawMessage
}

// PTZResult exposes captured acknowledgement fields while Raw retains unknown extensions.
type PTZResult struct {
	SessionID string          `json:"sessionId"`
	Timestamp float64         `json:"timestamp"`
	Version   float64         `json:"version"`
	Raw       json.RawMessage `json:"-"`
}

// RPCError describes a command rejection returned by the signaling service.
type RPCError = signaling.RPCError

type DeviceSession struct {
	connection *SignalingConnection
	core       *signaling.Session
	dialogID   string
	answer     SessionDescription
	offerSDP   string
	started    time.Time
	deviceID   int64
	signalID   string
	riid       string
	iceMode    ICECandidateMode
	mu         sync.Mutex
	closed     bool
	terminal   error
	movement   map[PTZAxis]string
	ready      chan struct{}
	done       chan struct{}
	doneOnce   sync.Once
}

type liveAnswerInfo struct {
	PingInterval json.RawMessage `json:"ping_interval"`
	SessionID    string          `json:"session_id"`
}

type liveAnswerBody struct {
	DeviceID  int64          `json:"doorbot_id"`
	SessionID string         `json:"session_id"`
	SDP       string         `json:"sdp"`
	Info      liveAnswerInfo `json:"session_info"`
	Type      string         `json:"type"`
}

type liveNegotiation struct {
	signalID  string
	riid      string
	answerSDP string
	controlID string
	heartbeat time.Duration
}

func (c *SignalingConnection) waitForLiveAnswer(ctx context.Context, events <-chan signaling.Message, id int64, deadlineError func() error) (liveNegotiation, error) {
	state := liveNegotiation{heartbeat: signaling.DefaultHeartbeatInterval}
	for state.answerSDP == "" || state.signalID == "" {
		select {
		case m := <-events:
			switch m.Method {
			case protocol.MethodSessionCreated:
				var body generatedsignaling.SessionCreatedBody
				if json.Unmarshal(m.Body, &body) != nil || int64(body.DoorbotId) != id || body.SessionId == "" {
					return state, fmt.Errorf("invalid session_created response")
				}
				state.signalID, state.riid = body.SessionId, m.RIID
			case protocol.MethodSDP:
				var body liveAnswerBody
				if json.Unmarshal(m.Body, &body) != nil || body.DeviceID != id || body.Type != "answer" || body.SDP == "" {
					return state, fmt.Errorf("invalid SDP answer")
				}
				if state.signalID != "" && body.SessionID != state.signalID {
					return state, fmt.Errorf("SDP signaling session mismatch")
				}
				state.signalID, state.answerSDP, state.controlID = body.SessionID, body.SDP, body.Info.SessionID
				if len(body.Info.PingInterval) > 0 {
					var seconds int
					if json.Unmarshal(body.Info.PingInterval, &seconds) != nil || seconds <= 0 || seconds > int(signaling.MaxHeartbeatInterval/time.Second) {
						return state, fmt.Errorf("invalid negotiated heartbeat interval")
					}
					state.heartbeat = time.Duration(seconds) * time.Second
				}
				if m.RIID != "" {
					state.riid = m.RIID
				}
			case protocol.MethodClose:
				return state, fmt.Errorf("signaling peer closed during negotiation")
			}
		case <-ctx.Done():
			return state, fmt.Errorf("signaling negotiation failed: %w", deadlineError())
		case <-c.done:
			return state, c.Err()
		}
	}
	return state, nil
}

func (c *SignalingConnection) waitForCameraStarted(ctx, negotiationCtx context.Context, events <-chan signaling.Message, session *DeviceSession, deadlineError func() error) error {
	for {
		select {
		case message := <-events:
			if message.Method == protocol.MethodSessionCreated || message.Method == protocol.MethodSDP {
				continue
			}
			if err := session.core.Handle(message); err != nil {
				return err
			}
			if message.Method == protocol.MethodClose && session.matches(message) {
				return signaling.ErrClosed
			}
			if message.Method == protocol.MethodCameraStarted && session.matches(message) {
				close(session.ready)
				return nil
			}
		case <-negotiationCtx.Done():
			return fmt.Errorf("session activation failed: %w", deadlineError())
		case <-ctx.Done():
			return ctx.Err()
		case <-c.done:
			return c.Err()
		case <-session.done:
			return session.Wait(context.Background())
		}
	}
}

func validateSessionRequest(req StartDeviceSessionRequest) (int64, time.Duration, error) {
	id, err := strconv.ParseInt(req.DeviceID, 10, 64)
	if err != nil || id <= 0 {
		return 0, 0, fmt.Errorf("device ID must be a positive integer")
	}
	if req.Offer.Type != "offer" || req.Offer.SDP == "" {
		return 0, 0, fmt.Errorf("offer must contain type offer and SDP")
	}
	if req.ICEMode != "" && req.ICEMode != ICETrickle && req.ICEMode != ICENonTrickle {
		return 0, 0, fmt.Errorf("unsupported ICE candidate mode %q", req.ICEMode)
	}
	if _, err = signaling.ParseSDP(req.Offer.SDP); err != nil {
		return 0, 0, fmt.Errorf("invalid SDP offer: %w", err)
	}
	maxAge := req.MaxAge
	if maxAge == 0 {
		maxAge = signaling.MaxSessionAge
	}
	if maxAge <= 0 || maxAge > signaling.MaxSessionAge {
		return 0, 0, fmt.Errorf("maximum session age must be between zero and sixty minutes")
	}
	return id, maxAge, nil
}

func (c *SignalingConnection) StartDeviceSession(ctx context.Context, req StartDeviceSessionRequest) (*DeviceSession, error) {
	started := time.Now()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id, maxAge, err := validateSessionRequest(req)
	if err != nil {
		return nil, err
	}
	negotiationBudget := min(maxAge, signaling.NegotiationTimeout)
	negotiationCtx, cancel := context.WithDeadline(ctx, started.Add(negotiationBudget))
	defer cancel()
	negotiationError := func() error {
		if !time.Now().Before(started.Add(maxAge)) {
			return signaling.ErrExpired
		}
		return negotiationCtx.Err()
	}
	dialog := uuid.NewString()
	events := make(chan signaling.Message, signaling.NegotiationQueueCapacity)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, c.Err()
	}
	c.pending[dialog] = events
	c.mu.Unlock()
	cleanup := func() { c.mu.Lock(); delete(c.pending, dialog); c.mu.Unlock() }
	body := generatedsignaling.LiveViewBody{DoorbotId: int(id), StreamOptions: &generatedsignaling.LiveStreamOptions{AudioEnabled: req.AudioEnabled, VideoEnabled: req.VideoEnabled}, Sdp: req.Offer.SDP, ReservedType: "offer"}
	raw, _ := json.Marshal(body)
	if err = c.send(negotiationCtx, signaling.Message{Method: "live_view", DialogID: dialog, Body: raw}); err != nil {
		cleanup()
		return nil, err
	}
	var signalID, riid string
	startedSuccessfully := false
	defer func() {
		if !startedSuccessfully && signalID != "" {
			ctx, cancel := context.WithTimeout(context.Background(), signaling.CloseTimeout)
			_ = c.send(ctx, signaling.Message{Method: "close", DialogID: dialog, RIID: riid, Body: mustJSON(map[string]any{"doorbot_id": id, "session_id": signalID})})
			cancel()
		}
	}()
	negotiated, err := c.waitForLiveAnswer(negotiationCtx, events, id, negotiationError)
	signalID, riid = negotiated.signalID, negotiated.riid
	if err != nil {
		cleanup()
		return nil, err
	}
	answerSDP, controlID, heartbeat := negotiated.answerSDP, negotiated.controlID, negotiated.heartbeat
	if controlID == "" || controlID == signalID {
		cleanup()
		return nil, fmt.Errorf("answer is missing an independent PTZ session identity")
	}
	answer, err := signaling.NormalizeAnswer(req.Offer.SDP, answerSDP)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("invalid SDP answer: %w", err)
	}
	if _, err = signaling.ParseSDP(answer); err != nil {
		cleanup()
		return nil, err
	}
	iceMode := req.ICEMode
	if iceMode == "" {
		iceMode = ICETrickle
	}
	created := &DeviceSession{connection: c, dialogID: dialog, answer: SessionDescription{Type: "answer", SDP: answer}, offerSDP: req.Offer.SDP, started: started, movement: map[PTZAxis]string{}, ready: make(chan struct{}), done: make(chan struct{}), deviceID: id, signalID: signalID, riid: riid, iceMode: iceMode}
	remaining := maxAge - time.Since(started)
	if remaining <= 0 {
		cleanup()
		return nil, signaling.ErrExpired
	}
	created.core, err = signaling.NewSession(ctx, signaling.SessionConfig{DeviceID: id, DialogID: dialog, SignalID: signalID, ControlID: controlID, Heartbeat: heartbeat, MaxAge: remaining, Send: func(ctx context.Context, m signaling.Message) error {
		if m.RIID == "" {
			m.RIID = riid
		}
		return c.send(ctx, m)
	}})
	if err != nil {
		cleanup()
		return nil, err
	}
	go created.watch()
	if err = c.send(negotiationCtx, signaling.Message{Method: "activate_session", DialogID: dialog, RIID: riid, Body: mustJSON(map[string]any{"doorbot_id": id, "session_id": signalID})}); err != nil {
		created.terminate(err)
		cleanup()
		return nil, err
	}
	if err = created.core.Send(negotiationCtx, "mic_enable", map[string]any{"enabled": req.AudioEnabled}); err != nil {
		created.terminate(err)
		cleanup()
		return nil, err
	}
	if err = created.core.Send(negotiationCtx, "stream_options", map[string]any{"audio_enabled": req.AudioEnabled}); err != nil {
		created.terminate(err)
		cleanup()
		return nil, err
	}
	if err = c.waitForCameraStarted(ctx, negotiationCtx, events, created, negotiationError); err != nil {
		created.terminate(err)
		cleanup()
		return nil, err
	}
	c.mu.Lock()
	delete(c.pending, dialog)
	if c.closed {
		c.mu.Unlock()
		created.terminate(c.Err())
		return nil, c.Err()
	}
	c.sessions[dialog] = created
	c.mu.Unlock()
	for {
		select {
		case m := <-events:
			created.handle(m)
		default:
			goto drained
		}
	}
drained:
	startedSuccessfully = true
	return created, nil
}

func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func (s *DeviceSession) watch() {
	err := s.core.Wait(context.Background())
	if errors.Is(err, signaling.ErrExpired) || errors.Is(err, signaling.ErrHeartbeat) {
		ctx, cancel := context.WithTimeout(context.Background(), signaling.CloseTimeout)
		_ = s.connection.send(ctx, signaling.Message{Method: "close", DialogID: s.dialogID, RIID: s.riid, Body: mustJSON(map[string]any{"doorbot_id": s.deviceID, "session_id": s.signalID})})
		cancel()
	}
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.terminal = err
	}
	s.mu.Unlock()
	s.doneOnce.Do(func() { close(s.done) })
	s.connection.removeSession(s.dialogID)
}
func (s *DeviceSession) matches(m signaling.Message) bool {
	var body generatedsignaling.SessionBody
	return m.DialogID == s.dialogID && json.Unmarshal(m.Body, &body) == nil && int64(body.DoorbotId) == s.deviceID && body.SessionId == s.signalID
}
func (s *DeviceSession) handle(m signaling.Message) {
	if err := s.core.Handle(m); err != nil {
		s.terminate(err)
		return
	}
	if m.Method == "close" && s.matches(m) {
		s.terminate(signaling.ErrClosed)
	}
}
func (s *DeviceSession) terminate(err error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.terminal = err
	s.mu.Unlock()
	s.core.Fail(err)
}
func (s *DeviceSession) Answer() SessionDescription { return s.answer }
func (s *DeviceSession) State() SessionState {
	s.mu.Lock()
	closed, err := s.closed, s.terminal
	s.mu.Unlock()
	if !closed {
		return SessionActive
	}
	if errors.Is(err, signaling.ErrExpired) {
		return SessionExpired
	}
	if err != nil && !errors.Is(err, signaling.ErrClosed) {
		return SessionFailed
	}
	return SessionClosed
}
func (s *DeviceSession) Wait(ctx context.Context) error {
	err := s.core.Wait(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// Wait for the public wrapper to publish the terminal state and finish its
	// bounded signaling close after the core has observed expiry or failure.
	select {
	case <-s.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return err
}
func (s *DeviceSession) Receive(ctx context.Context) (*SessionEvent, error) {
	m, e := s.core.Receive(ctx)
	if e != nil {
		return nil, e
	}
	return &SessionEvent{Method: m.Method, Body: m.Body}, nil
}
func (s *DeviceSession) SendICE(ctx context.Context, req ICECandidateRequest) error {
	if s.iceMode != ICETrickle {
		return fmt.Errorf("SendICE requires trickle ICE mode")
	}
	desc, e := signaling.ParseSDP(s.offerSDP)
	if e != nil {
		return e
	}
	if e = signaling.ValidateICE(desc, req.MID, req.MLineIndex); e != nil {
		return e
	}
	if req.Candidate == "" {
		return fmt.Errorf("ICE candidate must not be empty")
	}
	body := generatedsignaling.LiveIceBody{DoorbotId: int(s.deviceID), Ice: req.Candidate, Mid: req.MID, Mlineindex: req.MLineIndex}
	return s.connection.send(ctx, signaling.Message{Method: protocol.MethodICE, DialogID: s.dialogID, RIID: s.riid, Body: mustJSON(body)})
}

func (s *DeviceSession) call(ctx context.Context, method string, params map[string]any) (*PTZResult, error) {
	b, e := s.core.Call(ctx, method, params)
	if e != nil {
		return nil, e
	}
	var result PTZResult
	if err := json.Unmarshal(b, &result); err != nil {
		return nil, fmt.Errorf("decode PTZ result: %w", err)
	}
	result.Raw = append(json.RawMessage(nil), b...)
	return &result, nil
}
func (s *DeviceSession) PanStep(ctx context.Context, r PanStepRequest) (*PTZResult, error) {
	if r.Direction != PanLeft && r.Direction != PanRight {
		return nil, fmt.Errorf("invalid pan direction %q", r.Direction)
	}
	return s.call(ctx, protocol.RPCPanStep, map[string]any{"direction": r.Direction})
}
func (s *DeviceSession) TiltStep(ctx context.Context, r TiltStepRequest) (*PTZResult, error) {
	if r.Direction != TiltUp && r.Direction != TiltDown {
		return nil, fmt.Errorf("invalid tilt direction %q", r.Direction)
	}
	return s.call(ctx, protocol.RPCTiltStep, map[string]any{"direction": r.Direction})
}
func (s *DeviceSession) PanContinuous(ctx context.Context, r PanContinuousRequest) (*PTZResult, error) {
	if r.Direction != PanLeft && r.Direction != PanRight {
		return nil, fmt.Errorf("invalid pan direction %q", r.Direction)
	}
	if r.Speed < 0 || math.IsNaN(r.Speed) || math.IsInf(r.Speed, 0) {
		return nil, fmt.Errorf("invalid pan speed")
	}
	s.mu.Lock()
	s.movement[PanAxis] = string(r.Direction)
	s.mu.Unlock()
	v, e := s.call(ctx, protocol.RPCPanContinuous, map[string]any{"direction": r.Direction, "speed": r.Speed})
	if e != nil {
		s.mu.Lock()
		delete(s.movement, PanAxis)
		s.mu.Unlock()
	}
	return v, e
}
func (s *DeviceSession) TiltContinuous(ctx context.Context, r TiltContinuousRequest) (*PTZResult, error) {
	if r.Direction != TiltUp && r.Direction != TiltDown {
		return nil, fmt.Errorf("invalid tilt direction %q", r.Direction)
	}
	if r.Speed < 0 || math.IsNaN(r.Speed) || math.IsInf(r.Speed, 0) {
		return nil, fmt.Errorf("invalid tilt speed")
	}
	s.mu.Lock()
	s.movement[TiltAxis] = string(r.Direction)
	s.mu.Unlock()
	v, e := s.call(ctx, protocol.RPCTiltContinuous, map[string]any{"direction": r.Direction, "speed": r.Speed})
	if e != nil {
		s.mu.Lock()
		delete(s.movement, TiltAxis)
		s.mu.Unlock()
	}
	return v, e
}
func (s *DeviceSession) StopPTZ(ctx context.Context, r StopPTZRequest) (*PTZResult, error) {
	s.mu.Lock()
	direction := s.movement[r.Axis]
	s.mu.Unlock()
	if direction == "" {
		return nil, fmt.Errorf("no tracked continuous movement for axis %q", r.Axis)
	}
	method := protocol.RPCPanContinuous
	if r.Axis == TiltAxis {
		method = protocol.RPCTiltContinuous
	}
	result, e := s.call(ctx, method, map[string]any{"direction": direction, "speed": 0.0})
	if e == nil {
		s.mu.Lock()
		delete(s.movement, r.Axis)
		s.mu.Unlock()
	}
	return result, e
}
func (s *DeviceSession) SetMicrophone(ctx context.Context, r SetMicrophoneRequest) error {
	return s.core.Send(ctx, "mic_enable", map[string]any{"enabled": r.Enabled})
}
func (s *DeviceSession) SetStreamOptions(ctx context.Context, r SetStreamOptionsRequest) error {
	body := map[string]any{}
	if r.AudioEnabled != nil {
		body["audio_enabled"] = *r.AudioEnabled
	}
	if r.VideoEnabled != nil {
		body["video_enabled"] = *r.VideoEnabled
	}
	if len(body) == 0 {
		return fmt.Errorf("at least one stream option is required")
	}
	return s.core.Send(ctx, "stream_options", body)
}
func (s *DeviceSession) Close() error { return s.close(true) }
func (s *DeviceSession) close(sendClose bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), signaling.CloseTimeout)
	defer cancel()
	return s.closeWithContext(ctx, sendClose)
}

func (s *DeviceSession) closeWithContext(ctx context.Context, sendClose bool) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.terminal = signaling.ErrClosed
	movements := make(map[PTZAxis]string, len(s.movement))
	for axis, direction := range s.movement {
		movements[axis] = direction
	}
	s.movement = make(map[PTZAxis]string)
	s.mu.Unlock()
	if sendClose {
		// Stop each tracked continuous move before closing the signaling session.
		// Both RPC acknowledgements and the final close share one short best-effort budget.
		for axis, direction := range movements {
			method := protocol.RPCPanContinuous
			if axis == TiltAxis {
				method = protocol.RPCTiltContinuous
			}
			_, _ = s.call(ctx, method, map[string]any{"direction": direction, "speed": 0.0})
		}
		_ = s.core.Send(ctx, "close", nil)
	}
	_ = s.core.Close()
	s.connection.removeSession(s.dialogID)
	s.doneOnce.Do(func() { close(s.done) })
	return nil
}
