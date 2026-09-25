package system_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/ring"
)

type portableSessionCase struct {
	Case    string          `json:"case"`
	Message json.RawMessage `json:"message"`
}

func portableSessionCases(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	rows, err := replay.LoadCases[portableSessionCase](filepath.Join("..", "porting-fixtures", "session-variants.json"))
	if err != nil {
		t.Fatal(err)
	}
	cases := make(map[string]json.RawMessage, len(rows))
	for _, row := range rows {
		cases[row.Case] = row.Message
	}
	return cases
}

func recordedLiveView(t *testing.T) (string, map[string]json.RawMessage) {
	t.Helper()
	r, err := replay.LoadSessionRecording(filepath.Join("..", "recordings", "sessions", "flow-402.json"))
	if err != nil {
		t.Fatal(err)
	}
	var dialog, offer string
	messages := map[string]json.RawMessage{}
	for _, row := range r.Messages {
		var envelope struct {
			Method string          `json:"method"`
			Dialog string          `json:"dialog_id"`
			Body   json.RawMessage `json:"body"`
		}
		if err := json.Unmarshal(row.Payload, &envelope); err != nil {
			t.Fatal(err)
		}
		if dialog == "" && envelope.Method == "live_view" && row.Direction == "client_to_server" {
			dialog = envelope.Dialog
			var body struct {
				SDP string `json:"sdp"`
			}
			if err := json.Unmarshal(envelope.Body, &body); err != nil {
				t.Fatal(err)
			}
			offer = body.SDP
		}
		if envelope.Dialog == dialog && row.Direction == "server_to_client" {
			switch envelope.Method {
			case "session_created", "sdp", "camera_started":
				messages[envelope.Method] = row.Payload
			}
		}
	}
	if offer == "" || len(messages) != 3 {
		t.Fatalf("incomplete recorded live_view: offer=%t messages=%d", offer != "", len(messages))
	}
	return offer, messages
}

// The real captured SDP and session envelopes are replayed independently of
// controls. Only the runtime dialog/device IDs are rebound for the local peer.
func recordedSessionFrame(t *testing.T, raw json.RawMessage, dialog string) map[string]any {
	t.Helper()
	var frame map[string]any
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatal(err)
	}
	frame["dialog_id"] = dialog
	body := frame["body"].(map[string]any)
	body["doorbot_id"] = float64(1001)
	return frame
}

func TestRecordedLiveViewBehaviors(t *testing.T) {
	offer, captured := recordedLiveView(t)
	variants := portableSessionCases(t)
	for _, scenario := range []string{"establishment", "outbound ICE", "remote ICE", "remote termination"} {
		t.Run(scenario, func(t *testing.T) {
			conn := identityPeer(t, func(c *websocket.Conn, dialog string) {
				for _, method := range []string{"session_created", "sdp"} {
					if err := c.WriteJSON(recordedSessionFrame(t, captured[method], dialog)); err != nil {
						t.Error(err)
						return
					}
				}
				if !readActivation(c) {
					t.Error("activation, microphone, or stream options missing")
					return
				}
				if err := c.WriteJSON(recordedSessionFrame(t, captured["camera_started"], dialog)); err != nil {
					t.Error(err)
					return
				}
				switch scenario {
				case "outbound ICE":
					var sent struct {
						Method string `json:"method"`
						Body   struct {
							ICE   string `json:"ice"`
							MID   string `json:"mid"`
							Index int    `json:"mlineindex"`
						} `json:"body"`
					}
					if err := c.ReadJSON(&sent); err != nil || sent.Method != "ice" || sent.Body.ICE != "candidate:01 synthetic" || sent.Body.MID != "0" || sent.Body.Index != 0 {
						t.Errorf("outbound ICE = %+v, %v", sent, err)
						return
					}
				case "remote ICE", "remote termination":
					var msg map[string]any
					if err := json.Unmarshal(variants[map[string]string{"remote ICE": "remote-ice", "remote termination": "remote-close"}[scenario]], &msg); err != nil {
						t.Error(err)
						return
					}
					msg["dialog_id"] = dialog
					body := msg["body"].(map[string]any)
					body["doorbot_id"] = float64(1001)
					body["session_id"] = recordedSignalID(t, captured["session_created"])
					if err := c.WriteJSON(msg); err != nil {
						t.Error(err)
						return
					}
				}
				for {
					var msg map[string]any
					if c.ReadJSON(&msg) != nil || msg["method"] == "close" {
						return
					}
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			session, err := conn.StartDeviceSession(ctx, ring.StartDeviceSessionRequest{DeviceID: "1001", Offer: ring.SessionDescription{Type: "offer", SDP: offer}, VideoEnabled: true, ICEMode: ring.ICETrickle})
			if err != nil {
				t.Fatal(err)
			}
			if session.Answer().Type != "answer" || session.Answer().SDP == "" {
				t.Fatal("captured answer was not exposed")
			}
			switch scenario {
			case "outbound ICE":
				if err := session.SendICE(ctx, ring.ICECandidateRequest{Candidate: "candidate:01 synthetic", MID: "0", MLineIndex: 0}); err != nil {
					t.Fatal(err)
				}
			case "remote ICE":
				for {
					event, err := session.Receive(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if event.Method == "ice" {
						break
					}
				}
			case "remote termination":
				if err := session.Wait(ctx); !errors.Is(err, ring.ErrSessionClosed) || session.State() != ring.SessionClosed {
					t.Fatalf("remote close = %v, state %s", err, session.State())
				}
			default:
				if session.State() != ring.SessionActive {
					t.Fatal("session not active")
				}
			}
			_ = session.Close()
			_ = conn.Close()
		})
	}
}

func recordedSignalID(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var frame struct {
		Body struct {
			SessionID string `json:"session_id"`
		} `json:"body"`
	}
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatal(err)
	}
	return frame.Body.SessionID
}
