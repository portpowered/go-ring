package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/portpowered/go-ring/internal/protocol"
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
	for state.AnswerSDP == "" || state.SignalID == "" {
		select {
		case m := <-events:
			switch m.Method {
			case protocol.MethodSessionCreated:
				var body generatedsignaling.SessionCreatedBody
				if json.Unmarshal(m.Body, &body) != nil || int64(body.DoorbotId) != deviceID || body.SessionId == "" {
					return state, fmt.Errorf("invalid session_created response")
				}
				state.SignalID, state.RIID = body.SessionId, m.RIID
			case protocol.MethodSDP:
				var err error
				state, err = acceptLiveAnswer(state, m, deviceID)
				if err != nil {
					return state, err
				}
			case protocol.MethodClose:
				return state, fmt.Errorf("signaling peer closed during negotiation")
			}
		case <-ctx.Done():
			return state, fmt.Errorf("signaling negotiation failed: %w", deadlineError())
		case <-parentDone:
			return state, parentErr()
		}
	}
	return state, nil
}

func acceptLiveAnswer(state LiveNegotiation, message signaling.Message, deviceID int64) (LiveNegotiation, error) {
	var envelope map[string]json.RawMessage
	var info map[string]json.RawMessage
	if json.Unmarshal(message.Body, &envelope) != nil || json.Unmarshal(envelope["session_info"], &info) != nil {
		return state, fmt.Errorf("invalid SDP answer")
	}
	if raw, present := info["ping_interval"]; present {
		var seconds int
		if json.Unmarshal(raw, &seconds) != nil || seconds <= 0 || seconds > int(signaling.MaxHeartbeatInterval/time.Second) {
			return state, fmt.Errorf("invalid negotiated heartbeat interval")
		}
		state.Heartbeat = time.Duration(seconds) * time.Second
	}
	var body generatedsignaling.LiveAnswerBody
	if json.Unmarshal(message.Body, &body) != nil || int64(body.DoorbotId) != deviceID || body.ReservedType != protocol.SDPTypeAnswer || body.Sdp == "" || body.SessionInfo == nil {
		return state, fmt.Errorf("invalid SDP answer")
	}
	if state.SignalID != "" && body.SessionId != state.SignalID {
		return state, fmt.Errorf("SDP signaling session mismatch")
	}
	state.SignalID, state.AnswerSDP, state.ControlID = body.SessionId, body.Sdp, body.SessionInfo.SessionId
	if message.RIID != "" {
		state.RIID = message.RIID
	}
	return state, nil
}
