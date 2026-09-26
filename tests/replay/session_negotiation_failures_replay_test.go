package replay_test

import (
	"context"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/pkg/ring"
)

func TestRecordedSessionNegotiationFailureVariants(t *testing.T) {
	offer, recorded := recordedLiveView(t)
	for _, scenario := range []string{
		"wrong session-created device", "empty signaling identity", "wrong SDP device",
		"mismatched signaling identity", "missing control identity", "same control and signaling identity",
		"invalid answer SDP", "remote close before answer", "remote close before camera start",
		"invalid RPC before camera start", "camera start timeout", "socket closes before answer",
	} {
		t.Run(scenario, func(t *testing.T) {
			conn := identityPeer(t, func(c *websocket.Conn, dialog string) {
				created := recordedSessionFrame(t, recorded["session_created"], dialog)
				answer := recordedSessionFrame(t, recorded["sdp"], dialog)
				createdBody := created["body"].(map[string]any)
				answerBody := answer["body"].(map[string]any)
				info := answerBody["session_info"].(map[string]any)
				switch scenario {
				case "wrong session-created device":
					createdBody["doorbot_id"] = float64(999)
				case "empty signaling identity":
					createdBody["session_id"] = ""
				case "wrong SDP device":
					answerBody["doorbot_id"] = float64(999)
				case "mismatched signaling identity":
					answerBody["session_id"] = "another-session"
				case "missing control identity":
					info["session_id"] = ""
				case "same control and signaling identity":
					info["session_id"] = createdBody["session_id"]
				case "invalid answer SDP":
					answerBody["sdp"] = "not SDP"
				}
				if scenario == "remote close before answer" {
					_ = c.WriteJSON(map[string]any{"method": "close", "dialog_id": dialog, "body": createdBody})
					return
				}
				if err := c.WriteJSON(created); err != nil {
					t.Error(err)
					return
				}
				if scenario == "socket closes before answer" || scenario == "wrong session-created device" || scenario == "empty signaling identity" {
					return
				}
				if err := c.WriteJSON(answer); err != nil {
					t.Error(err)
					return
				}
				if scenario != "remote close before camera start" && scenario != "invalid RPC before camera start" && scenario != "camera start timeout" {
					return
				}
				if !readActivation(c) {
					t.Error("activation missing before camera-start failure")
					return
				}
				switch scenario {
				case "remote close before camera start":
					_ = c.WriteJSON(map[string]any{"method": "close", "dialog_id": dialog, "body": createdBody})
				case "invalid RPC before camera start":
					_ = c.WriteJSON(map[string]any{"method": "rpc", "dialog_id": dialog, "body": map[string]any{"doorbot_id": 1001, "session_id": createdBody["session_id"], "command": "bad"}})
				case "camera start timeout":
					_, _, _ = c.ReadMessage()
				}
			})
			defer conn.Close()
			wait := 2 * time.Second
			if scenario == "camera start timeout" {
				wait = 150 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), wait)
			defer cancel()
			_, err := conn.StartDeviceSession(ctx, ring.StartDeviceSessionRequest{DeviceID: "1001", Offer: ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offer}, ICEMode: ring.ICETrickle})
			if err == nil {
				t.Fatal("invalid recorded negotiation variant accepted")
			}
		})
	}
}

func TestRecordedSessionRejectsInvalidStartPolicy(t *testing.T) {
	offer, _ := recordedLiveView(t)
	for _, tc := range []ring.StartDeviceSessionRequest{
		{DeviceID: "1001", Offer: ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offer}, ICEMode: "unknown"},
		{DeviceID: "1001", Offer: ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offer}, MaxAge: -time.Second},
		{DeviceID: "1001", Offer: ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offer}, MaxAge: 61 * time.Minute},
	} {
		conn := identityPeer(t, func(c *websocket.Conn, _ string) { _, _, _ = c.ReadMessage() })
		if _, err := conn.StartDeviceSession(context.Background(), tc); err == nil {
			t.Fatalf("invalid session policy accepted: %+v", tc)
		}
		_ = conn.Close()
	}
}
