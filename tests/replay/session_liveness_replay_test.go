package replay_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/signaling"
)

func recordedHeartbeatPair(t *testing.T) (signaling.Message, signaling.Message) {
	t.Helper()
	var ping signaling.Message
	for _, row := range loadConversation(t, "flow-402.json").Messages {
		if row.Direction == "client_to_server" && row.Payload.Method == "ping" {
			ping = row.Payload
		}
		if ping.Method != "" && row.Direction == "server_to_client" && row.Payload.Method == "pong" && row.Payload.DialogID == ping.DialogID {
			return ping, row.Payload
		}
	}
	t.Fatal("capture has no matched ping/pong pair")
	return signaling.Message{}, signaling.Message{}
}

func newHeartbeatReplaySession(t *testing.T, ping signaling.Message, clock *recordedClock, out chan signaling.Message) *signaling.Session {
	t.Helper()
	var identity struct {
		DeviceID  int64  `json:"doorbot_id"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(ping.Body, &identity); err != nil {
		t.Fatal(err)
	}
	session, err := signaling.NewSession(context.Background(), signaling.SessionConfig{
		DeviceID: identity.DeviceID, DialogID: ping.DialogID, SignalID: identity.SessionID,
		ControlID: "control-fixture", Heartbeat: 10 * time.Second, Clock: clock,
		Send: func(_ context.Context, msg signaling.Message) error { out <- msg; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestRecordedHeartbeatVirtualHourDoesNotRenewSession(t *testing.T) {
	ping, pong := recordedHeartbeatPair(t)
	clock := newRecordedClock()
	out := make(chan signaling.Message, 2)
	session := newHeartbeatReplaySession(t, ping, clock, out)
	for elapsed := 10 * time.Second; elapsed < signaling.MaxSessionAge; elapsed += 10 * time.Second {
		clock.advance()
		actual := recordedNextMessage(t, out)
		if actual.Method != "ping" || actual.DialogID != ping.DialogID {
			t.Fatalf("virtual heartbeat = %+v", actual)
		}
		if err := session.Handle(pong); err != nil {
			t.Fatal(err)
		}
	}
	clock.advance()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := session.Wait(ctx); !errors.Is(err, signaling.ErrExpired) {
		t.Fatalf("virtual hour terminated with %v", err)
	}
}

func TestRecordedWrongIdentityPongDoesNotPreventTimeout(t *testing.T) {
	ping, pong := recordedHeartbeatPair(t)
	clock := newRecordedClock()
	out := make(chan signaling.Message, 2)
	session := newHeartbeatReplaySession(t, ping, clock, out)
	pong.DialogID = "unrelated-dialog"
	for range 2 {
		clock.advance()
		_ = recordedNextMessage(t, out)
		if err := session.Handle(pong); err != nil {
			t.Fatal(err)
		}
	}
	clock.advance()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := session.Wait(ctx); !errors.Is(err, signaling.ErrHeartbeat) {
		t.Fatalf("foreign pong refreshed session: %v", err)
	}
}

func TestRecordedPTZReplyMutationAndCancellation(t *testing.T) {
	pair := recordedRPCPair(t, "PTZ.Tilt.Step", nil)
	request := pair.request
	controlID := request["command"].(map[string]any)["params"].(map[string]any)["sessionId"].(string)
	for _, scenario := range []string{"wrong control identity", "protocol error", "missing result", "caller cancellation"} {
		t.Run(scenario, func(t *testing.T) {
			out := make(chan signaling.Message, 1)
			session, err := signaling.NewSession(context.Background(), signaling.SessionConfig{
				DeviceID: int64(request["doorbot_id"].(float64)), DialogID: "replay-dialog",
				SignalID: request["session_id"].(string), ControlID: controlID,
				Heartbeat: 10 * time.Second, Clock: newRecordedClock(),
				Send: func(_ context.Context, msg signaling.Message) error { out <- msg; return nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = session.Close() }()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, callErr := session.Call(ctx, "PTZ.Tilt.Step", map[string]any{"direction": "UP"})
				result <- callErr
			}()
			sent := recordedNextMessage(t, out)
			var sentBody map[string]any
			if err := json.Unmarshal(sent.Body, &sentBody); err != nil {
				t.Fatal(err)
			}
			commandID := sentBody["command"].(map[string]any)["id"]
			var reply map[string]any
			encoded, _ := json.Marshal(pair.reply)
			if err := json.Unmarshal(encoded, &reply); err != nil {
				t.Fatal(err)
			}
			command := reply["command"].(map[string]any)
			command["id"] = commandID
			message := signaling.Message{Method: "rpc", DialogID: "replay-dialog"}
			apply := func() error { message.Body, _ = json.Marshal(reply); return session.Handle(message) }
			switch scenario {
			case "wrong control identity":
				command["result"].(map[string]any)["sessionId"] = "foreign-control"
				if err := apply(); err != nil || session.Pending() != 1 {
					t.Fatalf("foreign PTZ result = %v, pending %d", err, session.Pending())
				}
				command["result"].(map[string]any)["sessionId"] = controlID
				if err := apply(); err != nil {
					t.Fatal(err)
				}
			case "protocol error":
				delete(command, "result")
				command["error"] = map[string]any{"code": -32602, "message": "invalid direction"}
				if err := apply(); err != nil {
					t.Fatal(err)
				}
			case "missing result":
				delete(command, "result")
				if err := apply(); err == nil {
					t.Fatal("empty PTZ acknowledgement accepted")
				}
				cancel()
			case "caller cancellation":
				cancel()
			}
			select {
			case err := <-result:
				if scenario == "wrong control identity" && err != nil {
					t.Fatal(err)
				}
				if scenario == "protocol error" {
					var rpcErr *signaling.RPCError
					if !errors.As(err, &rpcErr) || rpcErr.Code != -32602 {
						t.Fatalf("PTZ protocol error = %v", err)
					}
				}
				if (scenario == "missing result" || scenario == "caller cancellation") && err == nil {
					t.Fatal("canceled call succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("PTZ call did not settle")
			}
			if scenario == "caller cancellation" {
				if err := apply(); err != nil {
					t.Fatal(err)
				}
			}
			if session.Pending() != 0 {
				t.Fatal("pending PTZ command leaked")
			}
		})
	}
}
