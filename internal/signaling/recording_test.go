package signaling

import (
	"context"
	"encoding/json"
	"fmt"
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

// Each recorded command and its acknowledgement is a separate replay case.
// The stream test above still checks cross-command ordering and notifications.
func TestRecordedPTZCommandsIndividually(t *testing.T) {
	for _, name := range []string{"flow-21.json", "flow-402.json"} {
		recording := loadConversation(t, name)
		replies := map[string]Message{}
		for _, row := range recording.Messages {
			if row.Direction != "server_to_client" || row.Payload.Method != "rpc" {
				continue
			}
			var body struct {
				Command struct {
					ID     string          `json:"id"`
					Result json.RawMessage `json:"result"`
				} `json:"command"`
			}
			if err := json.Unmarshal(row.Payload.Body, &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Command.Result) > 0 {
				replies[body.Command.ID] = row.Payload
			}
		}
		count := 0
		for _, row := range recording.Messages {
			if row.Direction != "client_to_server" || row.Payload.Method != "rpc" {
				continue
			}
			var body struct {
				DeviceID int64  `json:"doorbot_id"`
				SignalID string `json:"session_id"`
				Command  struct {
					ID     string         `json:"id"`
					Method string         `json:"method"`
					Params map[string]any `json:"params"`
				} `json:"command"`
			}
			if err := json.Unmarshal(row.Payload.Body, &body); err != nil {
				t.Fatal(err)
			}
			reply, ok := replies[body.Command.ID]
			if !ok {
				t.Fatalf("%s command %s has no recorded result", name, body.Command.ID)
			}
			count++
			t.Run(fmt.Sprintf("%s/%s/%02d", name, body.Command.Method, count), func(t *testing.T) {
				control, ok := body.Command.Params["sessionId"].(string)
				if !ok {
					t.Fatal("missing control session ID")
				}
				out := make(chan Message, 1)
				s, err := NewSession(context.Background(), SessionConfig{DeviceID: body.DeviceID, DialogID: row.Payload.DialogID, SignalID: body.SignalID, ControlID: control, Heartbeat: 10 * time.Second, Clock: newClock(), Send: func(_ context.Context, m Message) error { out <- m; return nil }})
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				params := map[string]any{}
				for k, v := range body.Command.Params {
					if k != "sessionId" && k != "timestamp" && k != "version" {
						params[k] = v
					}
				}
				done := make(chan error, 1)
				go func() { _, err := s.Call(context.Background(), body.Command.Method, params); done <- err }()
				actual := nextMessage(t, out)
				var actualBody struct {
					Command struct {
						ID     string         `json:"id"`
						Method string         `json:"method"`
						Params map[string]any `json:"params"`
					} `json:"command"`
				}
				if err := json.Unmarshal(actual.Body, &actualBody); err != nil {
					t.Fatal(err)
				}
				if actual.Method != "rpc" || actual.DialogID != row.Payload.DialogID || actualBody.Command.Method != body.Command.Method {
					t.Fatalf("wrong PTZ request: %+v", actual)
				}
				for k, want := range params {
					if actualBody.Command.Params[k] != want {
						t.Fatalf("%s = %v, want %v", k, actualBody.Command.Params[k], want)
					}
				}
				if actualBody.Command.Params["sessionId"] != control {
					t.Fatal("PTZ control session ID changed")
				}
				var replyBody map[string]any
				if err := json.Unmarshal(reply.Body, &replyBody); err != nil {
					t.Fatal(err)
				}
				replyBody["command"].(map[string]any)["id"] = actualBody.Command.ID
				reply.Body, _ = json.Marshal(replyBody)
				if err := s.Handle(reply); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(time.Second):
					t.Fatal("PTZ result not correlated")
				}
				if s.Pending() != 0 {
					t.Fatal("pending PTZ call leaked")
				}
			})
		}
		if count == 0 {
			t.Fatalf("%s had no recorded PTZ commands", name)
		}
	}
}

// Captured ping/pong pairs exercise the timer and identity path one pair at a
// time; the separate virtual-hour test checks the hard 60-minute expiry.
func TestRecordedHeartbeatPairsIndividually(t *testing.T) {
	for _, name := range []string{"flow-21.json", "flow-402.json"} {
		pending := map[string]Message{}
		count := 0
		for _, row := range loadConversation(t, name).Messages {
			m := row.Payload
			if m.Method == "ping" && row.Direction == "client_to_server" {
				pending[m.DialogID] = m
				continue
			}
			if m.Method != "pong" || row.Direction != "server_to_client" {
				continue
			}
			ping, ok := pending[m.DialogID]
			if !ok {
				continue
			}
			delete(pending, m.DialogID)
			count++
			t.Run(fmt.Sprintf("%s/pair-%02d", name, count), func(t *testing.T) {
				var body struct {
					DeviceID int64  `json:"doorbot_id"`
					SignalID string `json:"session_id"`
				}
				if err := json.Unmarshal(ping.Body, &body); err != nil {
					t.Fatal(err)
				}
				clock := newClock()
				out := make(chan Message, 1)
				s, err := NewSession(context.Background(), SessionConfig{DeviceID: body.DeviceID, DialogID: ping.DialogID, SignalID: body.SignalID, ControlID: "control-fixture", Heartbeat: 10 * time.Second, Clock: clock, Send: func(_ context.Context, msg Message) error { out <- msg; return nil }})
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				clock.advance(10 * time.Second)
				actual := nextMessage(t, out)
				if actual.Method != "ping" || actual.DialogID != ping.DialogID || !replay.SemanticEqual(actual.Body, ping.Body) {
					t.Fatalf("ping differs from capture: %+v", actual)
				}
				if err := s.Handle(m); err != nil {
					t.Fatal(err)
				}
				if err := s.Send(context.Background(), "mic_enable", map[string]any{"enabled": true}); err != nil {
					t.Fatalf("matching pong did not keep session active: %v", err)
				}
			})
		}
		if count == 0 {
			t.Fatalf("%s has no recorded heartbeat pairs", name)
		}
	}
}

// Incoming trickle ICE is an event on the matching signaling session, even
// when the capture's ICE belongs to a playback dialog rather than live_view.
func TestRecordedRemoteICEIndividually(t *testing.T) {
	for _, name := range []string{"flow-21.json", "flow-402.json"} {
		count := 0
		for _, row := range loadConversation(t, name).Messages {
			m := row.Payload
			if row.Direction != "server_to_client" || m.Method != "ice" {
				continue
			}
			count++
			t.Run(fmt.Sprintf("%s/candidate-%02d", name, count), func(t *testing.T) {
				var body struct {
					DeviceID   int64  `json:"doorbot_id"`
					SignalID   string `json:"session_id"`
					Candidate  string `json:"ice"`
					MLineIndex int    `json:"mlineindex"`
				}
				if err := json.Unmarshal(m.Body, &body); err != nil {
					t.Fatal(err)
				}
				if body.Candidate == "" {
					t.Fatal("empty recorded ICE candidate")
				}
				s, err := NewSession(context.Background(), SessionConfig{DeviceID: body.DeviceID, DialogID: m.DialogID, SignalID: body.SignalID, ControlID: "control-fixture", Heartbeat: 10 * time.Second, Clock: newClock(), Send: func(context.Context, Message) error { return nil }})
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				if err := s.Handle(m); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				event, err := s.Receive(ctx)
				if err != nil || event.Method != "ice" || !replay.SemanticEqual(event.Body, m.Body) {
					t.Fatalf("remote ICE event = %+v, %v", event, err)
				}
			})
		}
		if count == 0 {
			t.Fatalf("%s has no captured remote ICE", name)
		}
	}
}
