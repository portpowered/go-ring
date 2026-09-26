package websocket

import (
	"context"
	"encoding/json"
	"time"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/ringerrors"
	"github.com/portpowered/go-ring/internal/signaling"
	"github.com/portpowered/go-ring/pkg/generatedsignaling"
)

// LiveNegotiation contains the identities and timing returned by signaling.
type LiveNegotiation struct {
	SignalID  string
	RIID      string
	AnswerSDP string
	ControlID string
	Heartbeat time.Duration
}

// AwaitLiveAnswer pairs session_created with the SDP answer and validates their identities.
func AwaitLiveAnswer(ctx context.Context, parentDone <-chan struct{}, parentErr func() error, events <-chan signaling.Message, deviceID int64, deadlineError func() error) (LiveNegotiation, error) {
	state := LiveNegotiation{Heartbeat: signaling.DefaultHeartbeatInterval}
	created := false
	for state.AnswerSDP == "" || state.SignalID == "" {
		select {
		case m := <-events:
			switch m.Method {
			case protocol.MethodSessionCreated:
				var body generatedsignaling.SessionCreatedBody
				if err := json.Unmarshal(m.Body, &body); err != nil {
					return state, ringerrors.NewConnectionError("invalid session_created response", err)
				}
				if int64(body.DoorbotId) != deviceID || body.SessionId == "" {
					return state, ringerrors.NewConnectionError("invalid session_created response", nil)
				}
				if created && (body.SessionId != state.SignalID || (m.RIID != "" && state.RIID != "" && m.RIID != state.RIID)) {
					return state, ringerrors.NewConnectionError("conflicting session_created response", nil)
				}
				created = true
				state.SignalID = body.SessionId
				if m.RIID != "" {
					state.RIID = m.RIID
				}
			case protocol.MethodSDP:
				if !created {
					return state, ringerrors.NewConnectionError("SDP answer before session_created", nil)
				}
				var err error
				state, err = acceptLiveAnswer(state, m, deviceID)
				if err != nil {
					return state, err
				}
			case protocol.MethodClose:
				return state, ringerrors.NewClosedError("signaling peer closed during negotiation")
			}
		case <-ctx.Done():
			return state, ringerrors.NewConnectionError("signaling negotiation failed", deadlineError())
		case <-parentDone:
			return state, parentErr()
		}
	}
	return state, nil
}

func acceptLiveAnswer(state LiveNegotiation, message signaling.Message, deviceID int64) (LiveNegotiation, error) {
	var envelope map[string]json.RawMessage
	var info map[string]json.RawMessage
	if err := json.Unmarshal(message.Body, &envelope); err != nil {
		return state, ringerrors.NewConnectionError("invalid SDP answer", err)
	}
	if err := json.Unmarshal(envelope["session_info"], &info); err != nil {
		return state, ringerrors.NewConnectionError("invalid SDP answer", err)
	}
	if raw, present := info["ping_interval"]; present {
		var seconds int
		if err := json.Unmarshal(raw, &seconds); err != nil {
			return state, ringerrors.NewConnectionError("invalid negotiated heartbeat interval", err)
		}
		if seconds <= 0 || seconds > int(signaling.MaxHeartbeatInterval/time.Second) {
			return state, ringerrors.NewConnectionError("invalid negotiated heartbeat interval", nil)
		}
		state.Heartbeat = time.Duration(seconds) * time.Second
	}
	var body generatedsignaling.LiveAnswerBody
	if err := json.Unmarshal(message.Body, &body); err != nil {
		return state, ringerrors.NewConnectionError("invalid SDP answer", err)
	}
	if int64(body.DoorbotId) != deviceID || body.ReservedType != protocol.SDPTypeAnswer || body.Sdp == "" || body.SessionInfo == nil {
		return state, ringerrors.NewConnectionError("invalid SDP answer", nil)
	}
	if state.SignalID != "" && body.SessionId != state.SignalID {
		return state, ringerrors.NewConnectionError("SDP signaling session mismatch", nil)
	}
	state.SignalID, state.AnswerSDP, state.ControlID = body.SessionId, body.Sdp, body.SessionInfo.SessionId
	if message.RIID != "" {
		state.RIID = message.RIID
	}
	return state, nil
}
