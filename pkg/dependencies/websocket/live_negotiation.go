package websocket

import (
	"context"
	"encoding/json"
	"time"

	"github.com/portpowered/go-ring/internal/generatedsignaling"
	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/ringerrors"
	"github.com/portpowered/go-ring/internal/signaling"
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
func AwaitLiveAnswer(
	ctx context.Context,
	parentDone <-chan struct{},
	parentErr func() error,
	events <-chan signaling.Message,
	deviceID int64,
	deadlineError func() error,
) (LiveNegotiation, error) {
	state := LiveNegotiation{
		SignalID:  "",
		RIID:      "",
		AnswerSDP: "",
		ControlID: "",
		Heartbeat: signaling.DefaultHeartbeatInterval,
	}
	created := false

	for state.AnswerSDP == "" || state.SignalID == "" {
		select {
		case message := <-events:
			nextState, nextCreated, err := acceptLiveNegotiationMessage(state, created, message, deviceID)
			if err != nil {
				return nextState, err
			}

			state, created = nextState, nextCreated
		case <-ctx.Done():
			return state, ringerrors.NewConnectionError("signaling negotiation failed", deadlineError())
		case <-parentDone:
			return state, parentErr()
		}
	}

	return state, nil
}

func acceptLiveNegotiationMessage(
	state LiveNegotiation,
	created bool,
	message signaling.Message,
	deviceID int64,
) (LiveNegotiation, bool, error) {
	switch message.Method {
	case protocol.MethodSessionCreated:
		return acceptSessionCreated(state, created, message, deviceID)
	case protocol.MethodSDP:
		if !created {
			return state, created, ringerrors.NewConnectionError("SDP answer before session_created", nil)
		}

		state, err := acceptLiveAnswer(state, message, deviceID)

		return state, created, err
	case protocol.MethodClose:
		return state, created, ringerrors.NewClosedError("signaling peer closed during negotiation")
	default:
		return state, created, nil
	}
}

func acceptSessionCreated(
	state LiveNegotiation,
	created bool,
	message signaling.Message,
	deviceID int64,
) (LiveNegotiation, bool, error) {
	var body generatedsignaling.SessionCreatedBody

	err := json.Unmarshal(message.Body, &body)
	if err != nil {
		return state, created, ringerrors.NewConnectionError("invalid session_created response", err)
	}

	if int64(body.DoorbotId) != deviceID || body.SessionId == "" {
		return state, created, ringerrors.NewConnectionError("invalid session_created response", nil)
	}

	if created && (body.SessionId != state.SignalID ||
		(message.RIID != "" && state.RIID != "" && message.RIID != state.RIID)) {
		return state, created, ringerrors.NewConnectionError("conflicting session_created response", nil)
	}

	state.SignalID = body.SessionId
	if message.RIID != "" {
		state.RIID = message.RIID
	}

	return state, true, nil
}

func acceptLiveAnswer(state LiveNegotiation, message signaling.Message, deviceID int64) (LiveNegotiation, error) {
	var (
		envelope map[string]json.RawMessage
		info     map[string]json.RawMessage
	)

	err := json.Unmarshal(message.Body, &envelope)
	if err != nil {
		return state, ringerrors.NewConnectionError("invalid SDP answer", err)
	}

	err = json.Unmarshal(envelope["session_info"], &info)
	if err != nil {
		return state, ringerrors.NewConnectionError("invalid SDP answer", err)
	}

	if raw, present := info["ping_interval"]; present {
		var seconds int

		err := json.Unmarshal(raw, &seconds)
		if err != nil {
			return state, ringerrors.NewConnectionError("invalid negotiated heartbeat interval", err)
		}

		if seconds <= 0 || seconds > int(signaling.MaxHeartbeatInterval/time.Second) {
			return state, ringerrors.NewConnectionError("invalid negotiated heartbeat interval", nil)
		}

		state.Heartbeat = time.Duration(seconds) * time.Second
	}

	var body generatedsignaling.LiveAnswerBody

	err = json.Unmarshal(message.Body, &body)
	if err != nil {
		return state, ringerrors.NewConnectionError("invalid SDP answer", err)
	}

	if int64(body.DoorbotId) != deviceID || body.ReservedType != protocol.SDPTypeAnswer || body.Sdp == "" ||
		body.SessionInfo == nil {
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
