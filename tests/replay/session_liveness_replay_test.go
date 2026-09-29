package replay_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/signaling"
)

const (
	wrongControlIdentityScenario = "wrong control identity"
	protocolErrorScenario        = "protocol error"
	missingResultScenario        = "missing result"
	callerCancellationScenario   = "caller cancellation"
)

func recordedHeartbeatPair(t *testing.T) (signaling.Message, signaling.Message) {
	t.Helper()

	var ping signaling.Message

	for _, row := range loadConversation(t, "flow-402.json").Messages {
		if row.Direction == capturedClientToServerDirection && row.Payload.Method == "ping" {
			ping = row.Payload
		}

		if ping.Method != "" && row.Direction == capturedServerToClientDirection && row.Payload.Method == "pong" &&
			row.Payload.DialogID == ping.DialogID {
			return ping, row.Payload
		}
	}

	t.Fatal("capture has no matched ping/pong pair")

	return signaling.Message{}, signaling.Message{}
}

func newHeartbeatReplaySession(
	t *testing.T,
	ping signaling.Message,
	clock *recordedClock,
	out chan signaling.Message,
) *signaling.Session {
	t.Helper()

	var identity struct {
		DeviceID  int64  `json:"doorbot_id"`
		SessionID string `json:"session_id"`
	}

	{
		err := json.Unmarshal(ping.Body, &identity)
		if err != nil {
			t.Fatal(err)
		}
	}

	session, err := signaling.NewSession(context.Background(), signaling.SessionConfig{
		DeviceID: identity.DeviceID, DialogID: ping.DialogID, SignalID: identity.SessionID,
		ControlID: "control-fixture", Heartbeat: 10 * time.Second, Clock: clock,
		Send: func(_ context.Context, msg signaling.Message) error {
			out <- msg

			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = session.Close() })

	return session
}

func TestRecordedHeartbeatVirtualHourDoesNotRenewSession(t *testing.T) {
	t.Parallel()

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

		err := session.Handle(pong)
		if err != nil {
			t.Fatal(err)
		}
	}

	clock.advance()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)

	err := session.Wait(ctx)

	if !errors.Is(err, signaling.ErrExpired) {
		t.Fatalf("virtual hour terminated with %v", err)
	}
}

func TestRecordedWrongIdentityPongDoesNotPreventTimeout(t *testing.T) {
	t.Parallel()

	ping, pong := recordedHeartbeatPair(t)
	clock := newRecordedClock()
	out := make(chan signaling.Message, 2)
	session := newHeartbeatReplaySession(t, ping, clock, out)
	pong.DialogID = "unrelated-dialog"

	for range 2 {
		clock.advance()

		_ = recordedNextMessage(t, out)

		err := session.Handle(pong)
		if err != nil {
			t.Fatal(err)
		}
	}

	clock.advance()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)

	err := session.Wait(ctx)

	if !errors.Is(err, signaling.ErrHeartbeat) {
		t.Fatalf("foreign pong refreshed session: %v", err)
	}
}

func TestRecordedPTZReplyMutationAndCancellation(t *testing.T) {
	t.Parallel()

	pair := recordedRPCPair(t, "PTZ.Tilt.Step", nil)
	request := pair.request
	requestParams := replayObjectField(t, replayObjectField(t, request, "command"), "params")
	controlID := replayStringField(t, requestParams, sessionIDField)

	for _, scenario := range []string{
		wrongControlIdentityScenario,
		protocolErrorScenario,
		missingResultScenario,
		callerCancellationScenario,
	} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			runRecordedPTZReplyScenario(t, request, pair.reply, controlID, scenario)
		})
	}
}

func runRecordedPTZReplyScenario(
	t *testing.T,
	request, capturedReply map[string]any,
	controlID, scenario string,
) {
	t.Helper()

	out := make(chan signaling.Message, 1)
	session := newPTZReplaySession(t, request, controlID, out)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)

	result := make(chan error, 1)

	go func() {
		_, err := session.Call(ctx, "PTZ.Tilt.Step", map[string]any{"direction": "UP"})
		result <- err
	}()

	sent := recordedNextMessage(t, out)
	sentBody := decodeReplayObject(t, sent.Body)
	commandID := replayObjectField(t, sentBody, "command")["id"]
	reply := correlatedPTZReply(t, capturedReply, commandID)
	apply := func() error { return handlePTZReply(t, session, reply) }

	applyRecordedPTZScenario(t, session, reply, apply, controlID, scenario, cancel)
	assertRecordedPTZCallResult(t, awaitPTZCallResult(t, result), scenario)

	if scenario == callerCancellationScenario {
		err := apply()
		if err != nil {
			t.Fatal(err)
		}
	}

	if session.Pending() != 0 {
		t.Fatal("pending PTZ command leaked")
	}
}

func newPTZReplaySession(
	t *testing.T,
	request map[string]any,
	controlID string,
	out chan<- signaling.Message,
) *signaling.Session {
	t.Helper()

	session, err := signaling.NewSession(context.Background(), signaling.SessionConfig{
		DeviceID: int64(replayFloatField(t, request, "doorbot_id")), DialogID: "replay-dialog",
		SignalID: replayStringField(t, request, "session_id"), ControlID: controlID,
		Heartbeat: 10 * time.Second, Clock: newRecordedClock(),
		Send: func(_ context.Context, msg signaling.Message) error {
			out <- msg

			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = session.Close() })

	return session
}

func decodeReplayObject(t *testing.T, encoded []byte) map[string]any {
	t.Helper()

	var value map[string]any

	err := json.Unmarshal(encoded, &value)
	if err != nil {
		t.Fatal(err)
	}

	return value
}

func correlatedPTZReply(t *testing.T, captured map[string]any, commandID any) map[string]any {
	t.Helper()

	encoded, err := json.Marshal(captured)
	if err != nil {
		t.Fatal(err)
	}

	reply := decodeReplayObject(t, encoded)
	replayObjectField(t, reply, "command")["id"] = commandID

	return reply
}

func handlePTZReply(t *testing.T, session *signaling.Session, reply map[string]any) error {
	t.Helper()

	encoded, err := json.Marshal(reply)
	if err != nil {
		t.Fatal(err)
	}

	err = session.Handle(signaling.Message{Method: "rpc", DialogID: "replay-dialog", Body: encoded})
	if err != nil {
		return wrapReplayTestError("handle replay PTZ reply", err)
	}

	return nil
}

func applyRecordedPTZScenario(
	t *testing.T,
	session *signaling.Session,
	reply map[string]any,
	apply func() error,
	controlID, scenario string,
	cancel context.CancelFunc,
) {
	t.Helper()

	command := replayObjectField(t, reply, "command")

	switch scenario {
	case wrongControlIdentityScenario:
		applyForeignControlReply(t, session, command, apply, controlID)
	case protocolErrorScenario:
		delete(command, "result")
		command["error"] = map[string]any{"code": -32602, "message": "invalid direction"}

		err := apply()
		if err != nil {
			t.Fatal(err)
		}
	case missingResultScenario:
		delete(command, "result")

		err := apply()
		if err == nil {
			t.Fatal("empty PTZ acknowledgement accepted")
		}

		cancel()
	case callerCancellationScenario:
		cancel()
	}
}

func applyForeignControlReply(
	t *testing.T,
	session *signaling.Session,
	command map[string]any,
	apply func() error,
	controlID string,
) {
	t.Helper()

	result := replayObjectField(t, command, "result")
	result[sessionIDField] = "foreign-control"

	err := apply()
	if err != nil || session.Pending() != 1 {
		t.Fatalf("foreign PTZ result = %v, pending %d", err, session.Pending())
	}

	result[sessionIDField] = controlID

	err = apply()
	if err != nil {
		t.Fatal(err)
	}
}

func awaitPTZCallResult(t *testing.T, result <-chan error) error {
	t.Helper()

	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal("PTZ call did not settle")

		return nil
	}
}

func assertRecordedPTZCallResult(t *testing.T, err error, scenario string) {
	t.Helper()

	switch scenario {
	case wrongControlIdentityScenario:
		if err != nil {
			t.Fatal(err)
		}
	case protocolErrorScenario:
		var rpcErr *signaling.RPCError
		if !errors.As(err, &rpcErr) || rpcErr.Code != -32602 {
			t.Fatalf("PTZ protocol error = %v", err)
		}
	case missingResultScenario, callerCancellationScenario:
		if err == nil {
			t.Fatal("canceled call succeeded")
		}
	}
}
