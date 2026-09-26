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
				var body liveAnswerBody
				if json.Unmarshal(m.Body, &body) != nil || body.DeviceID != deviceID || body.Type != protocol.SDPTypeAnswer || body.SDP == "" {
					return state, fmt.Errorf("invalid SDP answer")
				}
				if state.SignalID != "" && body.SessionID != state.SignalID {
					return state, fmt.Errorf("SDP signaling session mismatch")
				}
				state.SignalID, state.AnswerSDP, state.ControlID = body.SessionID, body.SDP, body.Info.SessionID
				if len(body.Info.PingInterval) > 0 {
					var seconds int
					if json.Unmarshal(body.Info.PingInterval, &seconds) != nil || seconds <= 0 || seconds > int(signaling.MaxHeartbeatInterval/time.Second) {
						return state, fmt.Errorf("invalid negotiated heartbeat interval")
					}
					state.Heartbeat = time.Duration(seconds) * time.Second
				}
				if m.RIID != "" {
					state.RIID = m.RIID
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
