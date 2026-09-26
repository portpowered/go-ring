package ring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/signaling"
	"github.com/portpowered/go-ring/pkg/dependencies/webrtc"
	dependencywebsocket "github.com/portpowered/go-ring/pkg/dependencies/websocket"
	"github.com/portpowered/go-ring/pkg/generatedsignaling"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

func (c *SignalingConnection) waitForCameraStarted(ctx, negotiationCtx context.Context, events <-chan signaling.Message, session *DeviceSession, deadlineError func() error) error {
	for {
		select {
		case message := <-events:
			if message.Method == protocol.MethodSessionCreated || message.Method == protocol.MethodSDP {
				continue
			}
			if err := session.core.Handle(message); err != nil {
				return sessionError("session activation message rejected", err)
			}
			if message.Method == protocol.MethodClose && session.matches(message) {
				return ringapimodels.NewClosedError("device session closed before activation", signaling.ErrClosed)
			}
			if message.Method == protocol.MethodCameraStarted && session.matches(message) {
				close(session.ready)
				return nil
			}
		case <-negotiationCtx.Done():
			return ringapimodels.NewConnectionError("session activation failed", deadlineError())
		case <-ctx.Done():
			return sessionError("session activation canceled", ctx.Err())
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
		return 0, 0, ringapimodels.NewBadRequestError("device ID must be a positive integer", err)
	}
	if req.Offer.Type != SDPTypeOffer || req.Offer.SDP == "" {
		return 0, 0, ringapimodels.NewBadRequestError("offer must contain type offer and SDP", nil)
	}
	if req.ICEMode != "" && req.ICEMode != ICETrickle && req.ICEMode != ICENonTrickle {
		return 0, 0, ringapimodels.NewBadRequestError(fmt.Sprintf("unsupported ICE candidate mode %q", req.ICEMode), nil)
	}
	if _, err = webrtc.ParseSDP(req.Offer.SDP); err != nil {
		return 0, 0, ringapimodels.NewBadRequestError("invalid SDP offer", err)
	}
	maxAge := req.MaxAge
	if maxAge == 0 {
		maxAge = signaling.MaxSessionAge
	}
	if maxAge <= 0 || maxAge > signaling.MaxSessionAge {
		return 0, 0, ringapimodels.NewBadRequestError("maximum session age must be between zero and sixty minutes", nil)
	}
	return id, maxAge, nil
}

func (c *SignalingConnection) StartDeviceSession(ctx context.Context, req StartDeviceSessionRequest) (*DeviceSession, error) {
	started := time.Now()
	if err := ctx.Err(); err != nil {
		return nil, ringapimodels.NewConnectionError("session start canceled", err)
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
	body := generatedsignaling.LiveViewBody{DoorbotId: int(id), StreamOptions: &generatedsignaling.LiveStreamOptions{AudioEnabled: req.AudioEnabled, VideoEnabled: req.VideoEnabled}, Sdp: req.Offer.SDP, ReservedType: protocol.SDPTypeOffer}
	raw, _ := json.Marshal(body)
	if err = c.send(negotiationCtx, signaling.Message{Method: protocol.MethodLiveView, DialogID: dialog, Body: raw}); err != nil {
		cleanup()
		return nil, sessionError("failed to send live-view offer", err)
	}
	var signalID, riid string
	startedSuccessfully := false
	defer func() {
		if !startedSuccessfully && signalID != "" {
			ctx, cancel := context.WithTimeout(context.Background(), signaling.CloseTimeout)
			_ = c.send(ctx, signaling.Message{Method: protocol.MethodClose, DialogID: dialog, RIID: riid, Body: mustJSON(generatedsignaling.SessionBody{DoorbotId: int(id), SessionId: signalID})})
			cancel()
		}
	}()
	negotiated, err := dependencywebsocket.AwaitLiveAnswer(negotiationCtx, c.done, c.Err, events, id, negotiationError)
	signalID, riid = negotiated.SignalID, negotiated.RIID
	if err != nil {
		cleanup()
		return nil, sessionError("live-view negotiation failed", err)
	}
	answerSDP, controlID, heartbeat := negotiated.AnswerSDP, negotiated.ControlID, negotiated.Heartbeat
	if controlID == "" || controlID == signalID {
		cleanup()
		return nil, ringapimodels.NewConnectionError("answer is missing an independent PTZ session identity", nil)
	}
	answer, err := webrtc.NormalizeAnswer(req.Offer.SDP, answerSDP)
	if err != nil {
		cleanup()
		return nil, ringapimodels.NewConnectionError("invalid SDP answer", err)
	}
	if _, err = webrtc.ParseSDP(answer); err != nil {
		cleanup()
		return nil, ringapimodels.NewConnectionError("invalid SDP answer", err)
	}
	iceMode := req.ICEMode
	if iceMode == "" {
		iceMode = ICETrickle
	}
	created := &DeviceSession{connection: c, dialogID: dialog, answer: SessionDescription{Type: SDPTypeAnswer, SDP: answer}, offerSDP: req.Offer.SDP, started: started, movement: map[PTZAxis]string{}, ready: make(chan struct{}), done: make(chan struct{}), deviceID: id, signalID: signalID, riid: riid, iceMode: iceMode}
	remaining := maxAge - time.Since(started)
	if remaining <= 0 {
		cleanup()
		return nil, ringapimodels.NewConnectionError("session expired during negotiation", signaling.ErrExpired)
	}
	created.core, err = signaling.NewSession(ctx, signaling.SessionConfig{DeviceID: id, DialogID: dialog, SignalID: signalID, ControlID: controlID, Heartbeat: heartbeat, MaxAge: remaining, Send: func(ctx context.Context, m signaling.Message) error {
		if m.RIID == "" {
			m.RIID = riid
		}
		return c.send(ctx, m)
	}})
	if err != nil {
		cleanup()
		return nil, sessionError("failed to create device session", err)
	}
	go created.watch()
	if err = c.send(negotiationCtx, signaling.Message{Method: protocol.MethodActivateSession, DialogID: dialog, RIID: riid, Body: mustJSON(generatedsignaling.SessionBody{DoorbotId: int(id), SessionId: signalID})}); err != nil {
		created.terminate(err)
		cleanup()
		return nil, sessionError("failed to activate device session", err)
	}
	if err = created.core.Send(negotiationCtx, protocol.MethodMicEnable, map[string]any{protocol.FieldEnabled: req.AudioEnabled}); err != nil {
		created.terminate(err)
		cleanup()
		return nil, sessionError("failed to set microphone state", err)
	}
	if err = created.core.Send(negotiationCtx, protocol.MethodStreamOptions, map[string]any{protocol.FieldAudioEnabled: req.AudioEnabled}); err != nil {
		created.terminate(err)
		cleanup()
		return nil, sessionError("failed to set stream options", err)
	}
	if err = c.waitForCameraStarted(ctx, negotiationCtx, events, created, negotiationError); err != nil {
		created.terminate(err)
		cleanup()
		return nil, sessionError("device session did not become ready", err)
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
		_ = s.connection.send(ctx, signaling.Message{Method: protocol.MethodClose, DialogID: s.dialogID, RIID: s.riid, Body: mustJSON(generatedsignaling.SessionBody{DoorbotId: int(s.deviceID), SessionId: s.signalID})})
		cancel()
	}
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.terminal = sessionError("device session ended", err)
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
	if m.Method == protocol.MethodClose && s.matches(m) {
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
	s.terminal = sessionError("device session ended", err)
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
		return sessionError("device session wait canceled", ctx.Err())
	}
	// Wait for the public wrapper to publish the terminal state and finish its
	// bounded signaling close after the core has observed expiry or failure.
	select {
	case <-s.done:
	case <-ctx.Done():
		return sessionError("device session wait canceled", ctx.Err())
	}
	return sessionError("device session ended", err)
}
func (s *DeviceSession) Receive(ctx context.Context) (*SessionEvent, error) {
	m, e := s.core.Receive(ctx)
	if e != nil {
		return nil, sessionError("device session receive failed", e)
	}
	return &SessionEvent{Method: m.Method, Body: m.Body}, nil
}
func (s *DeviceSession) SendICE(ctx context.Context, req ICECandidateRequest) error {
	if s.iceMode != ICETrickle {
		return ringapimodels.NewBadRequestError("SendICE requires trickle ICE mode", nil)
	}
	desc, e := webrtc.ParseSDP(s.offerSDP)
	if e != nil {
		return ringapimodels.NewInternalServerError("stored offer SDP is invalid", e)
	}
	if e = webrtc.ValidateICE(desc, req.MID, req.MLineIndex); e != nil {
		return ringapimodels.NewBadRequestError("invalid ICE candidate media identity", e)
	}
	if req.Candidate == "" {
		return ringapimodels.NewBadRequestError("ICE candidate must not be empty", nil)
	}
	body := generatedsignaling.LiveIceBody{DoorbotId: int(s.deviceID), Ice: req.Candidate, Mid: req.MID, Mlineindex: req.MLineIndex}
	return s.connection.send(ctx, signaling.Message{Method: protocol.MethodICE, DialogID: s.dialogID, RIID: s.riid, Body: mustJSON(body)})
}

func (s *DeviceSession) call(ctx context.Context, method string, params map[string]any) (*PTZResult, error) {
	b, e := s.core.Call(ctx, method, params)
	if e != nil {
		return nil, sessionError("PTZ command failed", e)
	}
	var result PTZResult
	if err := json.Unmarshal(b, &result); err != nil {
		return nil, ringapimodels.NewConnectionError("invalid PTZ result", err)
	}
	result.Raw = append(json.RawMessage(nil), b...)
	return &result, nil
}
func (s *DeviceSession) PanStep(ctx context.Context, r PanStepRequest) (*PTZResult, error) {
	if r.Direction != PanLeft && r.Direction != PanRight {
		return nil, ringapimodels.NewBadRequestError(fmt.Sprintf("invalid pan direction %q", r.Direction), nil)
	}
	return s.call(ctx, protocol.RPCPanStep, map[string]any{protocol.FieldDirection: r.Direction})
}
func (s *DeviceSession) TiltStep(ctx context.Context, r TiltStepRequest) (*PTZResult, error) {
	if r.Direction != TiltUp && r.Direction != TiltDown {
		return nil, ringapimodels.NewBadRequestError(fmt.Sprintf("invalid tilt direction %q", r.Direction), nil)
	}
	return s.call(ctx, protocol.RPCTiltStep, map[string]any{protocol.FieldDirection: r.Direction})
}
func (s *DeviceSession) PanContinuous(ctx context.Context, r PanContinuousRequest) (*PTZResult, error) {
	if r.Direction != PanLeft && r.Direction != PanRight {
		return nil, ringapimodels.NewBadRequestError(fmt.Sprintf("invalid pan direction %q", r.Direction), nil)
	}
	if r.Speed < 0 || r.Speed > protocol.PTZMaxSpeed || math.IsNaN(r.Speed) || math.IsInf(r.Speed, 0) {
		return nil, ringapimodels.NewBadRequestError("invalid pan speed", nil)
	}
	s.mu.Lock()
	s.movement[PanAxis] = string(r.Direction)
	s.mu.Unlock()
	v, e := s.call(ctx, protocol.RPCPanContinuous, map[string]any{protocol.FieldDirection: r.Direction, protocol.FieldSpeed: r.Speed})
	if e != nil {
		s.mu.Lock()
		delete(s.movement, PanAxis)
		s.mu.Unlock()
	}
	return v, e
}
func (s *DeviceSession) TiltContinuous(ctx context.Context, r TiltContinuousRequest) (*PTZResult, error) {
	if r.Direction != TiltUp && r.Direction != TiltDown {
		return nil, ringapimodels.NewBadRequestError(fmt.Sprintf("invalid tilt direction %q", r.Direction), nil)
	}
	if r.Speed < 0 || r.Speed > protocol.PTZMaxSpeed || math.IsNaN(r.Speed) || math.IsInf(r.Speed, 0) {
		return nil, ringapimodels.NewBadRequestError("invalid tilt speed", nil)
	}
	s.mu.Lock()
	s.movement[TiltAxis] = string(r.Direction)
	s.mu.Unlock()
	v, e := s.call(ctx, protocol.RPCTiltContinuous, map[string]any{protocol.FieldDirection: r.Direction, protocol.FieldSpeed: r.Speed})
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
		return nil, ringapimodels.NewBadRequestError(fmt.Sprintf("no tracked continuous movement for axis %q", r.Axis), nil)
	}
	method := protocol.RPCPanContinuous
	if r.Axis == TiltAxis {
		method = protocol.RPCTiltContinuous
	}
	result, e := s.call(ctx, method, map[string]any{protocol.FieldDirection: direction, protocol.FieldSpeed: 0.0})
	if e == nil {
		s.mu.Lock()
		delete(s.movement, r.Axis)
		s.mu.Unlock()
	}
	return result, e
}
func (s *DeviceSession) SetMicrophone(ctx context.Context, r SetMicrophoneRequest) error {
	return sessionError("microphone command failed", s.core.Send(ctx, protocol.MethodMicEnable, map[string]any{protocol.FieldEnabled: r.Enabled}))
}
func (s *DeviceSession) SetStreamOptions(ctx context.Context, r SetStreamOptionsRequest) error {
	body := map[string]any{}
	if r.AudioEnabled != nil {
		body[protocol.FieldAudioEnabled] = *r.AudioEnabled
	}
	if r.VideoEnabled != nil {
		body[protocol.FieldVideoEnabled] = *r.VideoEnabled
	}
	if len(body) == 0 {
		return ringapimodels.NewBadRequestError("at least one stream option is required", nil)
	}
	return sessionError("stream-options command failed", s.core.Send(ctx, protocol.MethodStreamOptions, body))
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
			_, _ = s.call(ctx, method, map[string]any{protocol.FieldDirection: direction, protocol.FieldSpeed: 0.0})
		}
		_ = s.core.Send(ctx, protocol.MethodClose, nil)
	}
	_ = s.core.Close()
	s.connection.removeSession(s.dialogID)
	s.doneOnce.Do(func() { close(s.done) })
	return nil
}
