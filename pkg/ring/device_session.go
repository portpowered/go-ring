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

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/signaling"
	"github.com/portpowered/go-ring/pkg/dependencies/webrtc"
	generatedsignaling "github.com/portpowered/go-ring/pkg/dependencymodels/signaling"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

func (c *SignalingConnection) waitForCameraStarted(
	ctx, negotiationCtx context.Context,
	events <-chan signaling.Message,
	session *DeviceSession,
	deadlineError func() error,
) error {
	for {
		select {
		case message := <-events:
			if message.Method == protocol.MethodSessionCreated || message.Method == protocol.MethodSDP {
				continue
			}

			err := session.core.Handle(message)
			if err != nil {
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
			return session.terminalError()
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
		return 0, 0, ringapimodels.NewBadRequestError(
			fmt.Sprintf("unsupported ICE candidate mode %q", req.ICEMode),
			nil,
		)
	}

	{
		_, err = webrtc.ParseSDP(req.Offer.SDP)
		if err != nil {
			return 0, 0, ringapimodels.NewBadRequestError("invalid SDP offer", err)
		}
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

func (c *SignalingConnection) StartDeviceSession(
	ctx context.Context,
	req StartDeviceSessionRequest,
) (*DeviceSession, error) {
	started := time.Now()

	err := ctx.Err()
	if err != nil {
		return nil, ringapimodels.NewConnectionError("session start canceled", err)
	}

	negotiation, negotiationCtx, err := c.prepareDeviceSession(ctx, req, started)
	if err != nil {
		return nil, err
	}

	defer negotiation.cancel()

	return c.finishDeviceSession(ctx, negotiationCtx, req, negotiation)
}

func (c *SignalingConnection) activateDeviceSession(
	ctx context.Context,
	session *DeviceSession,
	deviceID int64,
	signalID string,
	req StartDeviceSessionRequest,
) error {
	err := c.send(
		ctx,
		signaling.Message{
			Method:   protocol.MethodActivateSession,
			DialogID: session.dialogID,
			RIID:     session.riid,
			Body: mustJSON(generatedsignaling.SessionBody{
				DoorbotId:            int(deviceID),
				SessionId:            signalID,
				AdditionalProperties: nil,
			}),
		},
	)
	if err != nil {
		return sessionError("failed to activate device session", err)
	}

	err = session.core.Send(ctx, protocol.MethodMicEnable, mustJSON(generatedsignaling.SessionMicrophoneBody{
		DoorbotId:            int(deviceID),
		SessionId:            signalID,
		Enabled:              req.AudioEnabled,
		AdditionalProperties: nil,
	}))
	if err != nil {
		return sessionError("failed to set microphone state", err)
	}

	err = session.core.Send(ctx, protocol.MethodStreamOptions, mustJSON(generatedsignaling.SessionStreamAudioOptionsBody{
		DoorbotId:    int(deviceID),
		SessionId:    signalID,
		AudioEnabled: req.AudioEnabled,
	}))
	if err != nil {
		return sessionError("failed to set stream options", err)
	}

	return nil
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}

	return b
}
func (s *DeviceSession) watch(ctx context.Context) {
	err := s.core.Wait(ctx)
	if errors.Is(err, signaling.ErrExpired) || errors.Is(err, signaling.ErrHeartbeat) {
		// This bounded close must outlive the context whose expiry ended the session.
		ctx, cancel := context.WithTimeout(context.Background(), signaling.CloseTimeout)
		//nolint:contextcheck // Session teardown sends a final close after the parent context may be canceled.
		_ = s.connection.send(
			ctx,
			signaling.Message{
				Method:   protocol.MethodClose,
				DialogID: s.dialogID,
				RIID:     s.riid,
				Body: mustJSON(generatedsignaling.SessionBody{
					DoorbotId:            int(s.deviceID),
					SessionId:            s.signalID,
					AdditionalProperties: nil,
				}),
			},
		)

		cancel()
	}

	s.mu.Lock()

	if !s.closed {
		s.closed = true
		s.terminal = sessionError("device session ended", err)
	}

	s.mu.Unlock()
	s.connection.removeSession(s.dialogID)
	s.doneOnce.Do(func() { close(s.done) })
}
func (s *DeviceSession) matches(m signaling.Message) bool {
	var body generatedsignaling.SessionBody

	return m.DialogID == s.dialogID && json.Unmarshal(m.Body, &body) == nil && int64(body.DoorbotId) == s.deviceID &&
		body.SessionId == s.signalID
}
func (s *DeviceSession) handle(message signaling.Message) {
	err := s.core.Handle(message)
	if err != nil {
		s.terminate(err)

		return
	}

	if message.Method == protocol.MethodClose && s.matches(message) {
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

func (s *DeviceSession) terminalError() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.terminal
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

	desc, parseErr := webrtc.ParseSDP(s.offerSDP)
	if parseErr != nil {
		return ringapimodels.NewInternalServerError("stored offer SDP is invalid", parseErr)
	}

	{
		parseErr = webrtc.ValidateICE(desc, req.MID, req.MLineIndex)
		if parseErr != nil {
			return ringapimodels.NewBadRequestError("invalid ICE candidate media identity", parseErr)
		}
	}

	if req.Candidate == "" {
		return ringapimodels.NewBadRequestError("ICE candidate must not be empty", nil)
	}

	body := generatedsignaling.LiveIceBody{
		DoorbotId:            int(s.deviceID),
		Ice:                  req.Candidate,
		Mid:                  req.MID,
		Mlineindex:           req.MLineIndex,
		AdditionalProperties: nil,
	}

	return s.connection.send(
		ctx,
		signaling.Message{Method: protocol.MethodICE, DialogID: s.dialogID, RIID: s.riid, Body: mustJSON(body)},
	)
}

func (s *DeviceSession) call(ctx context.Context, method string, direction string, speed *float64) (*PTZResult, error) {
	wireDirection, ok := generatedsignaling.ValuesToPtzDirection[direction]
	if !ok {
		return nil, ringapimodels.NewBadRequestError("invalid PTZ direction", nil)
	}

	resultBytes, e := s.core.Call(ctx, method, wireDirection, speed)
	if e != nil {
		return nil, sessionError("PTZ command failed", e)
	}

	var result PTZResult

	err := json.Unmarshal(resultBytes, &result)
	if err != nil {
		return nil, ringapimodels.NewConnectionError("invalid PTZ result", err)
	}

	result.Raw = append(json.RawMessage(nil), resultBytes...)

	return &result, nil
}
func (s *DeviceSession) PanStep(ctx context.Context, r PanStepRequest) (*PTZResult, error) {
	if r.Direction != PanLeft && r.Direction != PanRight {
		return nil, ringapimodels.NewBadRequestError(fmt.Sprintf("invalid pan direction %q", r.Direction), nil)
	}

	return s.call(ctx, protocol.RPCPanStep, string(r.Direction), nil)
}
func (s *DeviceSession) TiltStep(ctx context.Context, r TiltStepRequest) (*PTZResult, error) {
	if r.Direction != TiltUp && r.Direction != TiltDown {
		return nil, ringapimodels.NewBadRequestError(fmt.Sprintf("invalid tilt direction %q", r.Direction), nil)
	}

	return s.call(ctx, protocol.RPCTiltStep, string(r.Direction), nil)
}
func (s *DeviceSession) PanContinuous(ctx context.Context, r PanContinuousRequest) (*PTZResult, error) {
	if r.Direction != PanLeft && r.Direction != PanRight {
		return nil, ringapimodels.NewBadRequestError(fmt.Sprintf("invalid pan direction %q", r.Direction), nil)
	}

	return s.continuous(ctx, PanAxis, string(r.Direction), r.Speed, protocol.RPCPanContinuous)
}
func (s *DeviceSession) TiltContinuous(ctx context.Context, r TiltContinuousRequest) (*PTZResult, error) {
	if r.Direction != TiltUp && r.Direction != TiltDown {
		return nil, ringapimodels.NewBadRequestError(fmt.Sprintf("invalid tilt direction %q", r.Direction), nil)
	}

	return s.continuous(ctx, TiltAxis, string(r.Direction), r.Speed, protocol.RPCTiltContinuous)
}

func (s *DeviceSession) continuous(
	ctx context.Context,
	axis PTZAxis,
	direction string,
	speed float64,
	method string,
) (*PTZResult, error) {
	if speed < 0 || speed > protocol.PTZMaxSpeed || math.IsNaN(speed) || math.IsInf(speed, 0) {
		return nil, ringapimodels.NewBadRequestError(fmt.Sprintf("invalid %s speed", axis), nil)
	}

	axisMu := &s.panMu
	if axis == TiltAxis {
		axisMu = &s.tiltMu
	}

	axisMu.Lock()
	defer axisMu.Unlock()

	s.mu.Lock()

	if s.closed {
		s.mu.Unlock()

		return nil, ringapimodels.NewClosedError("device session is closed", signaling.ErrClosed)
	}

	previousDirection, hadPrevious := s.movement[axis]
	s.movement[axis] = direction
	s.mu.Unlock()

	result, err := s.call(ctx, method, direction, &speed)
	if err != nil {
		s.mu.Lock()

		if hadPrevious {
			s.movement[axis] = previousDirection
		} else {
			delete(s.movement, axis)
		}

		s.mu.Unlock()
	}

	return result, err
}
func (s *DeviceSession) StopPTZ(ctx context.Context, request StopPTZRequest) (*PTZResult, error) {
	var axisMu *sync.Mutex

	switch request.Axis {
	case PanAxis:
		axisMu = &s.panMu
	case TiltAxis:
		axisMu = &s.tiltMu
	default:
		return nil, ringapimodels.NewBadRequestError("invalid PTZ axis", nil)
	}

	axisMu.Lock()
	defer axisMu.Unlock()

	s.mu.Lock()

	if s.closed {
		s.mu.Unlock()

		return nil, ringapimodels.NewClosedError("device session is closed", signaling.ErrClosed)
	}

	direction := s.movement[request.Axis]
	s.mu.Unlock()

	if direction == "" {
		return nil, ringapimodels.NewBadRequestError(
			fmt.Sprintf("no tracked continuous movement for axis %q", request.Axis),
			nil,
		)
	}

	method := protocol.RPCPanContinuous
	if request.Axis == TiltAxis {
		method = protocol.RPCTiltContinuous
	}

	stopSpeed := 0.0

	result, callErr := s.call(ctx, method, direction, &stopSpeed)
	if callErr == nil {
		s.mu.Lock()
		delete(s.movement, request.Axis)
		s.mu.Unlock()
	}

	return result, callErr
}

func (s *DeviceSession) SetMicrophone(ctx context.Context, r SetMicrophoneRequest) error {
	body := generatedsignaling.SessionMicrophoneBody{
		DoorbotId:            int(s.deviceID),
		SessionId:            s.signalID,
		Enabled:              r.Enabled,
		AdditionalProperties: nil,
	}

	return sessionError("microphone command failed", s.core.Send(ctx, protocol.MethodMicEnable, mustJSON(body)))
}
func (s *DeviceSession) SetStreamOptions(ctx context.Context, request SetStreamOptionsRequest) error {
	if request.AudioEnabled == nil && request.VideoEnabled == nil {
		return ringapimodels.NewBadRequestError("at least one stream option is required", nil)
	}

	var body json.RawMessage

	switch {
	case request.AudioEnabled != nil && request.VideoEnabled != nil:
		body = mustJSON(generatedsignaling.SessionStreamAudioVideoOptionsBody{
			DoorbotId:    int(s.deviceID),
			SessionId:    s.signalID,
			AudioEnabled: *request.AudioEnabled,
			VideoEnabled: *request.VideoEnabled,
		})
	case request.AudioEnabled != nil:
		body = mustJSON(generatedsignaling.SessionStreamAudioOptionsBody{
			DoorbotId:    int(s.deviceID),
			SessionId:    s.signalID,
			AudioEnabled: *request.AudioEnabled,
		})
	case request.VideoEnabled != nil:
		body = mustJSON(generatedsignaling.SessionStreamVideoOptionsBody{
			DoorbotId:    int(s.deviceID),
			SessionId:    s.signalID,
			VideoEnabled: *request.VideoEnabled,
		})
	}

	return sessionError("stream-options command failed", s.core.Send(ctx, protocol.MethodStreamOptions, body))
}
func (s *DeviceSession) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), signaling.CloseTimeout)
	defer cancel()

	s.closeWithContext(ctx, true)

	return nil
}

func (s *DeviceSession) closeWithContext(ctx context.Context, sendClose bool) {
	s.panMu.Lock()
	s.tiltMu.Lock()
	s.mu.Lock()

	if s.closed {
		s.mu.Unlock()
		s.tiltMu.Unlock()
		s.panMu.Unlock()

		return
	}

	s.closed = true
	s.terminal = signaling.ErrClosed

	movements := make(map[PTZAxis]string, len(s.movement))

	for axis, direction := range s.movement {
		movements[axis] = direction
	}

	s.movement = make(map[PTZAxis]string)
	s.mu.Unlock()
	s.tiltMu.Unlock()
	s.panMu.Unlock()

	if sendClose {
		// Stop each tracked continuous move before closing the signaling session.
		// Both RPC acknowledgements and the final close share one short best-effort budget.
		for axis, direction := range movements {
			method := protocol.RPCPanContinuous
			if axis == TiltAxis {
				method = protocol.RPCTiltContinuous
			}

			stopSpeed := 0.0
			_, _ = s.call(ctx, method, direction, &stopSpeed)
		}

		_ = s.core.Send(ctx, protocol.MethodClose, nil)
	}

	_ = s.core.Close()
	s.connection.removeSession(s.dialogID)
	s.doneOnce.Do(func() { close(s.done) })
}
