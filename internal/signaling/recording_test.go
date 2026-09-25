package signaling

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/testkit/replay"
)

type recordedMessages struct {
	Messages []struct {
		Direction string  `json:"direction"`
		Payload   Message `json:"payload"`
	} `json:"messages"`
}

func loadConversation(t *testing.T, name string) recordedMessages {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "test", "recordings", "sessions", name))
	if err != nil {
		t.Fatal(err)
	}
	var recording recordedMessages
	if err = json.Unmarshal(b, &recording); err != nil {
		t.Fatal(err)
	}
	return recording
}

// Python RingWebRtcStream supplies the SDP/ICE baseline. These assertions run
// the corresponding shapes from the Android conversations through our parser,
// including multiple same-kind media sections absent from the old Go example.
func TestRecordedSDPOfferAnswers(t *testing.T) {
	for _, name := range []string{"flow-21.json", "flow-402.json"} {
		t.Run(name, func(t *testing.T) {
			offers := map[string]string{}
			answers := 0
			for _, row := range loadConversation(t, name).Messages {
				m := row.Payload
				var body struct {
					SDP string `json:"sdp"`
				}
				if err := json.Unmarshal(m.Body, &body); err != nil {
					t.Fatal(err)
				}
				if body.SDP == "" {
					continue
				}
				if row.Direction == "client_to_server" {
					if _, err := ParseSDP(body.SDP); err != nil {
						t.Fatalf("%s offer: %v", m.Method, err)
					}
					offers[m.DialogID] = body.SDP
					continue
				}
				offer, ok := offers[m.DialogID]
				if !ok {
					t.Fatal("answer without corresponding offer")
				}
				if _, err := NormalizeAnswer(offer, body.SDP); err != nil {
					t.Fatalf("answer: %v", err)
				}
				answers++
			}
			if answers == 0 {
				t.Fatal("recording exercised no offer/answer pairs")
			}
		})
	}
}

// PTZ has no Python sender at the pinned revision. Replay its recorded requests,
// replies and notifications as an intentional extension, with explicit ID and
// timestamp bindings instead of requiring byte equality of generated values.
func TestRecordedPTZConversations(t *testing.T) {
	for _, name := range []string{"flow-21.json", "flow-402.json"} {
		t.Run(name, func(t *testing.T) {
			sessions := map[string]*Session{}
			out := make(chan Message, 64)
			ids := map[string]string{}
			pending := map[string]chan error{}
			calls, results, notifications := 0, 0, 0
			for _, row := range loadConversation(t, name).Messages {
				m := row.Payload
				if m.Method != "rpc" {
					continue
				}
				var body struct {
					DeviceID  int64  `json:"doorbot_id"`
					SessionID string `json:"session_id"`
					Command   struct {
						ID     string         `json:"id"`
						Method string         `json:"method"`
						Params map[string]any `json:"params"`
					} `json:"command"`
				}
				if err := json.Unmarshal(m.Body, &body); err != nil {
					t.Fatal(err)
				}
				s := sessions[m.DialogID]
				if row.Direction == "client_to_server" {
					if s == nil {
						control, ok := body.Command.Params["sessionId"].(string)
						if !ok {
							t.Fatal("missing control ID")
						}
						var err error
						s, err = NewSession(context.Background(), SessionConfig{DeviceID: body.DeviceID, DialogID: m.DialogID, SignalID: body.SessionID, ControlID: control, Heartbeat: 10 * time.Second, Clock: newClock(), Send: func(_ context.Context, m Message) error { out <- m; return nil }})
						if err != nil {
							t.Fatal(err)
						}
						sessions[m.DialogID] = s
						t.Cleanup(func() { s.Close() })
					}
					params := map[string]any{}
					for k, v := range body.Command.Params {
						if k != "sessionId" && k != "timestamp" && k != "version" {
							params[k] = v
						}
					}
					done := make(chan error, 1)
					pending[body.Command.ID] = done
					go func(method string) { _, err := s.Call(context.Background(), method, params); done <- err }(body.Command.Method)
					actual := nextMessage(t, out)
					var sent map[string]any
					if err := json.Unmarshal(actual.Body, &sent); err != nil {
						t.Fatal(err)
					}
					command := sent["command"].(map[string]any)
					ids[body.Command.ID] = command["id"].(string)
					command["id"] = body.Command.ID
					command["params"].(map[string]any)["timestamp"] = body.Command.Params["timestamp"]
					encoded, _ := json.Marshal(sent)
					if !replay.SemanticEqual(encoded, m.Body) || actual.DialogID != m.DialogID {
						t.Fatalf("outbound %s differs from recording after explicit bindings", body.Command.Method)
					}
					calls++
					continue
				}
				if s == nil {
					t.Fatal("recorded RPC event lacks active session")
				}
				if body.Command.Method == "" {
					var response map[string]any
					_ = json.Unmarshal(m.Body, &response)
					response["command"].(map[string]any)["id"] = ids[body.Command.ID]
					m.Body, _ = json.Marshal(response)
					if err := s.Handle(m); err != nil {
						t.Fatal(err)
					}
					select {
					case err := <-pending[body.Command.ID]:
						if err != nil {
							t.Fatal(err)
						}
					case <-time.After(time.Second):
						t.Fatal("recorded reply not correlated")
					}
					delete(pending, body.Command.ID)
					results++
				} else {
					if err := s.Handle(m); err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					event, err := s.Receive(ctx)
					cancel()
					if err != nil || event.Method != "rpc" {
						t.Fatal("missing PTZ event", err)
					}
					notifications++
				}
			}
			if calls == 0 || calls != results || len(pending) != 0 || notifications == 0 {
				t.Fatalf("incomplete PTZ replay: calls=%d results=%d notifications=%d pending=%d", calls, results, notifications, len(pending))
			}
			for _, s := range sessions {
				if s.Pending() != 0 {
					t.Fatal("pending RPC leaked")
				}
			}
		})
	}
}
