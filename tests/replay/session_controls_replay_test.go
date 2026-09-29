package replay_test

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/pkg/ring"
)

const (
	sessionIDField = "sessionId"
	timestampField = "timestamp"
)

type capturedRPCPair struct {
	request map[string]any
	reply   map[string]any
}

type recordedSessionEnvelope struct {
	Body recordedSessionBody `json:"body"`
}

type recordedSessionBody struct {
	Info recordedSessionInfo `json:"session_info"`
}

type recordedSessionInfo struct {
	SessionID string `json:"session_id"`
}

func recordedRPCPair(t *testing.T, method string, speed *float64) capturedRPCPair {
	t.Helper()

	var (
		pending   map[string]any
		commandID string
	)

	for _, row := range loadConversation(t, "flow-402.json").Messages {
		if row.Payload.Method != "rpc" {
			continue
		}

		var frame map[string]any

		err := json.Unmarshal(row.Payload.Body, &frame)
		if err != nil {
			t.Fatal(err)
		}

		command := replayObjectField(t, frame, "command")
		if row.Direction == capturedClientToServerDirection {
			if command["method"] != method {
				continue
			}

			params := replayObjectField(t, command, "params")
			if speed != nil && params["speed"] != *speed {
				continue
			}

			pending = frame
			commandID = replayStringField(t, command, "id")

			continue
		}

		if pending != nil && command["id"] == commandID {
			return capturedRPCPair{request: pending, reply: frame}
		}
	}

	t.Fatalf("missing captured %session command/reply pair", method)

	return capturedRPCPair{}
}

func startRecordedSession(t *testing.T, conn *ring.SignalingConnection, offer string) *ring.DeviceSession {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)

	session, err := conn.StartDeviceSession(ctx, ring.StartDeviceSessionRequest{
		DeviceID: "1001", Offer: ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offer},
		VideoEnabled: true, ICEMode: ring.ICETrickle,
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = session.Close(); _ = conn.Close(); cancel() })

	return session
}

func serveRecordedNegotiation(
	t *testing.T,
	connection *websocket.Conn,
	dialog string,
	captured map[string]json.RawMessage,
	heartbeat bool,
) bool {
	t.Helper()

	for _, method := range []string{"session_created", "sdp"} {
		frame := recordedSessionFrame(t, captured[method], dialog)

		if heartbeat && method == "sdp" {
			body := replayObjectField(t, frame, "body")
			info := replayObjectField(t, body, "session_info")
			info["ping_interval"] = float64(1)
		}

		err := connection.WriteJSON(frame)
		if err != nil {
			t.Error(err)

			return false
		}
	}

	if !readActivation(connection) {
		t.Error("recorded session activation missing")

		return false
	}

	err := connection.WriteJSON(recordedSessionFrame(t, captured["camera_started"], dialog))
	if err != nil {
		t.Error(err)

		return false
	}

	return true
}

func replayPTZReply(
	t *testing.T,
	connection *websocket.Conn,
	dialog string,
	pair capturedRPCPair,
	signalID string,
	controlID string,
) {
	t.Helper()

	var sent map[string]any

	err := connection.ReadJSON(&sent)
	if err != nil {
		t.Error(err)

		return
	}

	if sent["method"] != "rpc" {
		t.Errorf("expected PTZ RPC, got %v", sent["method"])

		return
	}

	body := replayObjectField(t, sent, "body")
	command := replayObjectField(t, body, "command")

	want := replayObjectField(t, pair.request, "command")
	if command["method"] != want["method"] {
		t.Errorf("PTZ method = %v, want %v", command["method"], want["method"])

		return
	}

	params := replayObjectField(t, command, "params")

	wantParams := replayObjectField(t, want, "params")
	for key, value := range wantParams {
		if key == timestampField || key == sessionIDField {
			continue
		}

		if params[key] != value {
			t.Errorf("PTZ %session = %v, want %v", key, params[key], value)
		}
	}

	if params[sessionIDField] != controlID {
		t.Error("PTZ control session ID does not match captured session")
	}

	reply := map[string]any{"method": "rpc", "dialog_id": dialog, "body": pair.reply}
	pair.reply["doorbot_id"], pair.reply["session_id"] = float64(1001), signalID
	replyCommand := replayObjectField(t, pair.reply, "command")
	replyCommand["id"] = command["id"]

	result := replayObjectField(t, replyCommand, "result")
	result[sessionIDField] = controlID

	err = connection.WriteJSON(reply)
	if err != nil {
		t.Error(err)
	}
}

func TestRecordedConnectionPTZResponses(t *testing.T) {
	t.Parallel()

	offer, captured := recordedLiveView(t)

	var answer recordedSessionEnvelope

	err := json.Unmarshal(captured["sdp"], &answer)
	if err != nil {
		t.Fatal(err)
	}

	controlID := answer.Body.Info.SessionID

	moveSpeed, stopSpeed := 0.5, 0.0
	for _, tc := range []struct {
		name  string
		pairs []capturedRPCPair
		call  func(context.Context, *ring.DeviceSession) error
	}{
		{
			"tilt step",
			[]capturedRPCPair{recordedRPCPair(t, "PTZ.Tilt.Step", nil)},
			func(ctx context.Context, session *ring.DeviceSession) error {
				result, err := session.TiltStep(ctx, ring.TiltStepRequest{Direction: ring.TiltUp})
				if err == nil && result.SessionID != controlID {
					return testReplayErrorf("tilt result session = %q", result.SessionID)
				}

				return wrapReplayTestError("replay recorded tilt step", err)
			}},
		{
			"continuous movement and stop",
			[]capturedRPCPair{
				recordedRPCPair(t, "PTZ.Pan.Continuous", &moveSpeed),
				recordedRPCPair(t, "PTZ.Pan.Continuous", &stopSpeed),
			},
			func(ctx context.Context, session *ring.DeviceSession) error {
				{
					_, err := session.PanContinuous(
						ctx,
						ring.PanContinuousRequest{Direction: ring.PanRight, Speed: moveSpeed},
					)
					if err != nil {
						return wrapReplayTestError("replay continuous pan movement", err)
					}
				}
				_, err := session.StopPTZ(ctx, ring.StopPTZRequest{Axis: ring.PanAxis})

				return wrapReplayTestError("replay pan stop", err)
			},
		},
		{
			"tilt movement and stop",
			[]capturedRPCPair{
				recordedRPCPair(t, "PTZ.Tilt.Continuous", &moveSpeed),
				recordedRPCPair(t, "PTZ.Tilt.Continuous", &stopSpeed),
			},
			func(ctx context.Context, session *ring.DeviceSession) error {
				{
					_, err := session.TiltContinuous(
						ctx,
						ring.TiltContinuousRequest{Direction: ring.TiltUp, Speed: moveSpeed},
					)
					if err != nil {
						return wrapReplayTestError("replay continuous tilt movement", err)
					}
				}
				_, err := session.StopPTZ(ctx, ring.StopPTZRequest{Axis: ring.TiltAxis})

				return wrapReplayTestError("replay tilt stop", err)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			conn := identityPeer(t, func(connection *websocket.Conn, dialog string) {
				if !serveRecordedNegotiation(t, connection, dialog, captured, false) {
					return
				}

				for _, pair := range tc.pairs {
					replayPTZReply(t, connection, dialog, pair, recordedSignalID(t, captured["session_created"]), controlID)
				}

				_, _, _ = connection.ReadMessage()
			})
			session := startRecordedSession(t, conn, offer)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			t.Cleanup(cancel)

			err := tc.call(ctx, session)
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRecordedSessionRejectsInvalidControlsBeforeWire(t *testing.T) {
	t.Parallel()

	offer, captured := recordedLiveView(t)
	conn := identityPeer(t, func(connection *websocket.Conn, dialog string) {
		if !serveRecordedNegotiation(t, connection, dialog, captured, false) {
			return
		}

		var frame map[string]any

		err := connection.ReadJSON(&frame)
		if err == nil && frame["method"] != "close" {
			t.Errorf("invalid control reached wire: %v", frame["method"])
		}
	})
	session := startRecordedSession(t, conn, offer)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"bad pan direction", func() error {
			_, err := session.PanStep(ctx, ring.PanStepRequest{Direction: "SIDEWAYS"})

			return wrapReplayTestError("validate invalid pan step", err)
		}},
		{"bad tilt direction", func() error {
			_, err := session.TiltStep(ctx, ring.TiltStepRequest{Direction: "SIDEWAYS"})

			return wrapReplayTestError("validate invalid tilt step", err)
		}},
		{"negative pan speed", func() error {
			_, err := session.PanContinuous(ctx, ring.PanContinuousRequest{Direction: ring.PanRight, Speed: -1})

			return wrapReplayTestError("validate negative pan speed", err)
		}},
		{"nonfinite pan speed", func() error {
			_, err := session.PanContinuous(ctx, ring.PanContinuousRequest{Direction: ring.PanRight, Speed: math.NaN()})

			return wrapReplayTestError("validate nonfinite pan speed", err)
		}},
		{"bad pan movement direction", func() error {
			_, err := session.PanContinuous(ctx, ring.PanContinuousRequest{Direction: "SIDEWAYS", Speed: 0.5})

			return wrapReplayTestError("validate invalid pan movement direction", err)
		}},
		{"negative tilt speed", func() error {
			_, err := session.TiltContinuous(ctx, ring.TiltContinuousRequest{Direction: ring.TiltUp, Speed: -1})

			return wrapReplayTestError("validate negative tilt speed", err)
		}},
		{"nonfinite tilt speed", func() error {
			_, err := session.TiltContinuous(ctx, ring.TiltContinuousRequest{Direction: ring.TiltUp, Speed: math.Inf(1)})

			return wrapReplayTestError("validate nonfinite tilt speed", err)
		}},
		{"pan speed above normalized range", func() error {
			_, err := session.PanContinuous(ctx, ring.PanContinuousRequest{Direction: ring.PanLeft, Speed: 1.01})

			return wrapReplayTestError("validate pan speed above normalized range", err)
		}},
		{"tilt speed above normalized range", func() error {
			_, err := session.TiltContinuous(ctx, ring.TiltContinuousRequest{Direction: ring.TiltUp, Speed: 1.01})

			return wrapReplayTestError("validate tilt speed above normalized range", err)
		}},
		{"bad tilt movement direction", func() error {
			_, err := session.TiltContinuous(ctx, ring.TiltContinuousRequest{Direction: "SIDEWAYS", Speed: 0.5})

			return wrapReplayTestError("validate invalid tilt movement direction", err)
		}},
		{"stop without movement", func() error {
			_, err := session.StopPTZ(ctx, ring.StopPTZRequest{Axis: ring.PanAxis})

			return wrapReplayTestError("validate stop without pan movement", err)
		}},
		{
			"empty ICE candidate",
			func() error {
				return session.SendICE(ctx, ring.ICECandidateRequest{MID: "0", MLineIndex: 0})
			},
		},
		{"unknown ICE MID", func() error {
			return session.SendICE(ctx, ring.ICECandidateRequest{
				Candidate:  "candidate:synthetic",
				MID:        "unknown",
				MLineIndex: 0,
			})
		}},
	} {
		err := tc.call()
		if err == nil {
			t.Errorf("invalid control %q accepted", tc.name)
		}
	}
}

func TestRecordedConnectionPingPong(t *testing.T) {
	t.Parallel()

	offer, captured := recordedLiveView(t)

	var pong map[string]any

	for _, row := range loadConversation(t, "flow-402.json").Messages {
		if row.Direction == capturedServerToClientDirection && row.Payload.Method == "pong" {
			err := json.Unmarshal(row.Payload.Body, &pong)
			if err != nil {
				t.Fatal(err)
			}

			break
		}
	}

	if pong == nil {
		t.Fatal("capture has no pong")
	}

	conn := identityPeer(t, func(connection *websocket.Conn, dialog string) {
		if !serveRecordedNegotiation(t, connection, dialog, captured, true) {
			return
		}

		for range 2 {
			var ping map[string]any

			err := connection.ReadJSON(&ping)
			if err != nil {
				t.Error(err)

				return
			}

			if ping["method"] != "ping" ||
				replayStringField(t, replayObjectField(t, ping, "body"), "session_id") !=
					recordedSignalID(t, captured["session_created"]) {
				t.Errorf("unexpected heartbeat: %v", ping)

				return
			}

			pong["doorbot_id"], pong["session_id"] = float64(1001), recordedSignalID(t, captured["session_created"])

			err = connection.WriteJSON(map[string]any{"method": "pong", "dialog_id": dialog, "body": pong})
			if err != nil {
				t.Error(err)

				return
			}
		}

		_, _, _ = connection.ReadMessage()
	})
	session := startRecordedSession(t, conn, offer)

	time.Sleep(2200 * time.Millisecond)

	if session.State() != ring.SessionActive {
		t.Fatalf("session closed despite recorded pong replies: %session", session.State())
	}
}
