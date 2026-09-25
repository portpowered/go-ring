package replay_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/pkg/ring"
)

type capturedRPCPair struct {
	request map[string]any
	reply   map[string]any
}

func recordedRPCPair(t *testing.T, method string, speed *float64) capturedRPCPair {
	t.Helper()
	var pending map[string]any
	var commandID string
	for _, row := range loadConversation(t, "flow-402.json").Messages {
		if row.Payload.Method != "rpc" {
			continue
		}
		var frame map[string]any
		if err := json.Unmarshal(row.Payload.Body, &frame); err != nil {
			t.Fatal(err)
		}
		command := frame["command"].(map[string]any)
		if row.Direction == "client_to_server" {
			if command["method"] != method {
				continue
			}
			params := command["params"].(map[string]any)
			if speed != nil && params["speed"] != *speed {
				continue
			}
			pending, commandID = frame, command["id"].(string)
			continue
		}
		if pending != nil && command["id"] == commandID {
			return capturedRPCPair{request: pending, reply: frame}
		}
	}
	t.Fatalf("missing captured %s command/reply pair", method)
	return capturedRPCPair{}
}

func startRecordedSession(t *testing.T, conn *ring.SignalingConnection, offer string) *ring.DeviceSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	session, err := conn.StartDeviceSession(ctx, ring.StartDeviceSessionRequest{
		DeviceID: "1001", Offer: ring.SessionDescription{Type: "offer", SDP: offer},
		VideoEnabled: true, ICEMode: ring.ICETrickle,
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close(); _ = conn.Close(); cancel() })
	return session
}

func serveRecordedNegotiation(t *testing.T, c *websocket.Conn, dialog string, captured map[string]json.RawMessage, heartbeat bool) bool {
	t.Helper()
	for _, method := range []string{"session_created", "sdp"} {
		frame := recordedSessionFrame(t, captured[method], dialog)
		if heartbeat && method == "sdp" {
			frame["body"].(map[string]any)["session_info"].(map[string]any)["ping_interval"] = float64(1)
		}
		if err := c.WriteJSON(frame); err != nil {
			t.Error(err)
			return false
		}
	}
	if !readActivation(c) {
		t.Error("recorded session activation missing")
		return false
	}
	if err := c.WriteJSON(recordedSessionFrame(t, captured["camera_started"], dialog)); err != nil {
		t.Error(err)
		return false
	}
	return true
}

func replayPTZReply(t *testing.T, c *websocket.Conn, dialog string, pair capturedRPCPair, signalID, controlID string) {
	t.Helper()
	var sent map[string]any
	if err := c.ReadJSON(&sent); err != nil {
		t.Error(err)
		return
	}
	if sent["method"] != "rpc" {
		t.Errorf("expected PTZ RPC, got %v", sent["method"])
		return
	}
	body := sent["body"].(map[string]any)
	command := body["command"].(map[string]any)
	want := pair.request["command"].(map[string]any)
	if command["method"] != want["method"] {
		t.Errorf("PTZ method = %v, want %v", command["method"], want["method"])
		return
	}
	params := command["params"].(map[string]any)
	for key, value := range want["params"].(map[string]any) {
		if key == "timestamp" || key == "sessionId" {
			continue
		}
		if params[key] != value {
			t.Errorf("PTZ %s = %v, want %v", key, params[key], value)
		}
	}
	if params["sessionId"] != controlID {
		t.Error("PTZ control session ID does not match captured session")
	}
	reply := map[string]any{"method": "rpc", "dialog_id": dialog, "body": pair.reply}
	pair.reply["doorbot_id"], pair.reply["session_id"] = float64(1001), signalID
	pair.reply["command"].(map[string]any)["id"] = command["id"]
	pair.reply["command"].(map[string]any)["result"].(map[string]any)["sessionId"] = controlID
	if err := c.WriteJSON(reply); err != nil {
		t.Error(err)
	}
}

func TestRecordedConnectionPTZResponses(t *testing.T) {
	offer, captured := recordedLiveView(t)
	var answer struct {
		Body struct {
			Info struct {
				SessionID string `json:"session_id"`
			} `json:"session_info"`
		} `json:"body"`
	}
	if err := json.Unmarshal(captured["sdp"], &answer); err != nil {
		t.Fatal(err)
	}
	controlID := answer.Body.Info.SessionID
	moveSpeed, stopSpeed := 0.5, 0.0
	for _, tc := range []struct {
		name  string
		pairs []capturedRPCPair
		call  func(context.Context, *ring.DeviceSession) error
	}{
		{"tilt step", []capturedRPCPair{recordedRPCPair(t, "PTZ.Tilt.Step", nil)}, func(ctx context.Context, s *ring.DeviceSession) error {
			result, err := s.TiltStep(ctx, ring.TiltStepRequest{Direction: ring.TiltUp})
			if err == nil && result.SessionID != controlID {
				return fmt.Errorf("tilt result session = %q", result.SessionID)
			}
			return err
		}},
		{"continuous movement and stop", []capturedRPCPair{recordedRPCPair(t, "PTZ.Pan.Continuous", &moveSpeed), recordedRPCPair(t, "PTZ.Pan.Continuous", &stopSpeed)}, func(ctx context.Context, s *ring.DeviceSession) error {
			if _, err := s.PanContinuous(ctx, ring.PanContinuousRequest{Direction: ring.PanRight, Speed: moveSpeed}); err != nil {
				return err
			}
			_, err := s.StopPTZ(ctx, ring.StopPTZRequest{Axis: ring.PanAxis})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := identityPeer(t, func(c *websocket.Conn, dialog string) {
				if !serveRecordedNegotiation(t, c, dialog, captured, false) {
					return
				}
				for _, pair := range tc.pairs {
					replayPTZReply(t, c, dialog, pair, recordedSignalID(t, captured["session_created"]), controlID)
				}
				_, _, _ = c.ReadMessage()
			})
			session := startRecordedSession(t, conn, offer)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := tc.call(ctx, session); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRecordedConnectionPingPong(t *testing.T) {
	offer, captured := recordedLiveView(t)
	var pong map[string]any
	for _, row := range loadConversation(t, "flow-402.json").Messages {
		if row.Direction == "server_to_client" && row.Payload.Method == "pong" {
			if err := json.Unmarshal(row.Payload.Body, &pong); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if pong == nil {
		t.Fatal("capture has no pong")
	}
	conn := identityPeer(t, func(c *websocket.Conn, dialog string) {
		if !serveRecordedNegotiation(t, c, dialog, captured, true) {
			return
		}
		for range 2 {
			var ping map[string]any
			if err := c.ReadJSON(&ping); err != nil {
				t.Error(err)
				return
			}
			if ping["method"] != "ping" || ping["body"].(map[string]any)["session_id"] != recordedSignalID(t, captured["session_created"]) {
				t.Errorf("unexpected heartbeat: %v", ping)
				return
			}
			pong["doorbot_id"], pong["session_id"] = float64(1001), recordedSignalID(t, captured["session_created"])
			if err := c.WriteJSON(map[string]any{"method": "pong", "dialog_id": dialog, "body": pong}); err != nil {
				t.Error(err)
				return
			}
		}
		_, _, _ = c.ReadMessage()
	})
	session := startRecordedSession(t, conn, offer)
	time.Sleep(2200 * time.Millisecond)
	if session.State() != ring.SessionActive {
		t.Fatalf("session closed despite recorded pong replies: %s", session.State())
	}
}
