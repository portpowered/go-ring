package replay_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/signaling"
	"github.com/portpowered/go-ring/internal/testkit/replay"
)

// PTZ has no Python sender at the pinned revision. Replay its recorded requests,
// replies and notifications as an intentional extension, with explicit ID and
// timestamp bindings instead of requiring byte equality of generated values.
func TestRecordedPTZConversations(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"flow-21.json", "flow-402.json"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			replayRecordedPTZConversation(t, name)
		})
	}
}

type recordedPTZConversation struct {
	sessions      map[string]*signaling.Session
	out           chan signaling.Message
	ids           map[string]string
	pending       map[string]chan error
	calls         int
	results       int
	notifications int
}

func replayRecordedPTZConversation(t *testing.T, name string) {
	t.Helper()

	state := &recordedPTZConversation{
		sessions:      make(map[string]*signaling.Session),
		out:           make(chan signaling.Message, 64),
		ids:           make(map[string]string),
		pending:       make(map[string]chan error),
		calls:         0,
		results:       0,
		notifications: 0,
	}

	for _, row := range loadConversation(t, name).Messages {
		state.replayMessage(t, row)
	}

	state.assertComplete(t, name)
}

func (state *recordedPTZConversation) replayMessage(t *testing.T, row recordedMessage) {
	t.Helper()

	message := row.Payload
	if message.Method != "rpc" {
		return
	}

	var body recordedSessionRPC

	err := json.Unmarshal(message.Body, &body)
	if err != nil {
		t.Fatal(err)
	}

	if row.Direction == capturedClientToServerDirection {
		state.replayCall(t, message, body)

		return
	}

	session := state.sessions[message.DialogID]
	if session == nil {
		t.Fatal("recorded RPC event lacks active session")
	}

	if body.Command.Method == "" {
		state.replayResult(t, session, message, body)

		return
	}

	state.replayNotification(t, session, message)
}

func (state *recordedPTZConversation) replayCall(t *testing.T, message signaling.Message, body recordedSessionRPC) {
	t.Helper()

	session := state.sessions[message.DialogID]
	if session == nil {
		control, ok := body.Command.Params[sessionIDField].(string)
		if !ok {
			t.Fatal("missing control ID")
		}

		session = newRecordedPTZSession(t, body, message.DialogID, control, state.out)
		state.sessions[message.DialogID] = session
	}

	params := recordedPTZParams(body.Command.Params)
	done := make(chan error, 1)
	state.pending[body.Command.ID] = done

	go func(method string, arguments map[string]any) {
		_, err := session.Call(context.Background(), method, arguments)
		done <- err
	}(body.Command.Method, params)

	actual := recordedNextMessage(t, state.out)
	sent := decodeReplayObject(t, actual.Body)
	command := replayObjectField(t, sent, "command")
	state.ids[body.Command.ID] = replayStringField(t, command, "id")
	command["id"] = body.Command.ID
	replayObjectField(t, command, "params")[timestampField] = body.Command.Params[timestampField]

	encoded, err := json.Marshal(sent)
	if err != nil {
		t.Fatal(err)
	}

	if !replay.SemanticEqual(encoded, message.Body) || actual.DialogID != message.DialogID {
		t.Fatalf("outbound %s differs from recording after explicit bindings", body.Command.Method)
	}

	state.calls++
}

func newRecordedPTZSession(
	t *testing.T,
	body recordedSessionRPC,
	dialogID, controlID string,
	out chan<- signaling.Message,
) *signaling.Session {
	t.Helper()

	session, err := signaling.NewSession(context.Background(), signaling.SessionConfig{
		DeviceID: body.DeviceID, DialogID: dialogID, SignalID: body.SessionID,
		ControlID: controlID, Heartbeat: 10 * time.Second, MaxAge: 0, Clock: newRecordedClock(),
		Send: func(_ context.Context, outgoing signaling.Message) error {
			out <- outgoing

			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		err := session.Close()
		if err != nil {
			t.Error(err)
		}
	})

	return session
}

func recordedPTZParams(captured map[string]any) map[string]any {
	params := make(map[string]any, len(captured))

	for name, value := range captured {
		if name == sessionIDField || name == timestampField || name == "version" {
			continue
		}

		params[name] = value
	}

	return params
}

func (state *recordedPTZConversation) replayResult(
	t *testing.T,
	session *signaling.Session,
	message signaling.Message,
	body recordedSessionRPC,
) {
	t.Helper()

	response := decodeReplayObject(t, message.Body)
	replayObjectField(t, response, "command")["id"] = state.ids[body.Command.ID]
	message.Body = encodeReplayJSON(t, response)

	err := session.Handle(message)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-state.pending[body.Command.ID]:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("recorded reply not correlated")
	}

	delete(state.pending, body.Command.ID)

	state.results++
}

func (state *recordedPTZConversation) replayNotification(
	t *testing.T,
	session *signaling.Session,
	message signaling.Message,
) {
	t.Helper()

	err := session.Handle(message)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	event, err := session.Receive(ctx)
	if err != nil || event.Method != "rpc" {
		t.Fatal("missing PTZ event", err)
	}

	state.notifications++
}

func (state *recordedPTZConversation) assertComplete(t *testing.T, name string) {
	t.Helper()

	if state.calls == 0 || state.calls != state.results || len(state.pending) != 0 || state.notifications == 0 {
		t.Fatalf(
			"incomplete PTZ replay: calls=%d results=%d notifications=%d pending=%d",
			state.calls,
			state.results,
			state.notifications,
			len(state.pending),
		)
	}

	for _, session := range state.sessions {
		if session.Pending() != 0 {
			t.Fatalf("%s has a pending RPC", name)
		}
	}
}

// Each recorded command and its acknowledgement is a separate replay case.
// The stream test above still checks cross-command ordering and notifications.
func TestRecordedPTZCommandsIndividually(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"flow-21.json", "flow-402.json"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			replayRecordedPTZCommands(t, name)
		})
	}
}

func replayRecordedPTZCommands(t *testing.T, name string) {
	t.Helper()

	recording := loadConversation(t, name)
	replies := recordedPTZReplies(t, recording)
	count := 0

	for _, row := range recording.Messages {
		if row.Direction != capturedClientToServerDirection || row.Payload.Method != "rpc" {
			continue
		}

		var body recordedSignalRPC

		err := json.Unmarshal(row.Payload.Body, &body)
		if err != nil {
			t.Fatal(err)
		}

		reply, ok := replies[body.Command.ID]
		if !ok {
			t.Fatalf("%s command %s has no recorded result", name, body.Command.ID)
		}

		count++
		t.Run(fmt.Sprintf("%s/%s/%02d", name, body.Command.Method, count), func(t *testing.T) {
			t.Parallel()

			replayRecordedPTZCommand(t, row.Payload, body, reply)
		})
	}

	if count == 0 {
		t.Fatalf("%s had no recorded PTZ commands", name)
	}
}

func recordedPTZReplies(t *testing.T, recording recordedMessages) map[string]signaling.Message {
	t.Helper()

	replies := make(map[string]signaling.Message)

	for _, row := range recording.Messages {
		if row.Direction != capturedServerToClientDirection || row.Payload.Method != "rpc" {
			continue
		}

		var body recordedRPCResult

		err := json.Unmarshal(row.Payload.Body, &body)
		if err != nil {
			t.Fatal(err)
		}

		if len(body.Command.Result) > 0 {
			replies[body.Command.ID] = row.Payload
		}
	}

	return replies
}

func replayRecordedPTZCommand(
	t *testing.T,
	request signaling.Message,
	body recordedSignalRPC,
	reply signaling.Message,
) {
	t.Helper()

	control := replayStringField(t, body.Command.Params, sessionIDField)
	out := make(chan signaling.Message, 1)
	session := newRecordedPTZSession(t, recordedSessionRPC{
		DeviceID:  body.DeviceID,
		SessionID: body.SignalID,
		Command:   recordedRPCCommand{ID: "", Method: "", Params: nil},
	}, request.DialogID, control, out)
	params := recordedPTZParams(body.Command.Params)
	done := make(chan error, 1)

	go func() {
		_, err := session.Call(context.Background(), body.Command.Method, params)
		done <- err
	}()

	actual := recordedNextMessage(t, out)
	actualBody := decodeRecordedRPCBody(t, actual.Body)
	assertRecordedPTZRequest(t, request, body, actual, actualBody, params, control)

	reboundReply := rebindRecordedPTZReply(t, reply, actualBody.Command.ID)

	err := session.Handle(reboundReply)
	if err != nil {
		t.Fatal(err)
	}

	awaitRecordedPTZResult(t, done)

	if session.Pending() != 0 {
		t.Fatal("pending PTZ call leaked")
	}
}

func decodeRecordedRPCBody(t *testing.T, encoded []byte) recordedRPCBody {
	t.Helper()

	var body recordedRPCBody

	err := json.Unmarshal(encoded, &body)
	if err != nil {
		t.Fatal(err)
	}

	return body
}

func assertRecordedPTZRequest(
	t *testing.T,
	request signaling.Message,
	captured recordedSignalRPC,
	actual signaling.Message,
	body recordedRPCBody,
	params map[string]any,
	control string,
) {
	t.Helper()

	if actual.Method != "rpc" || actual.DialogID != request.DialogID ||
		body.Command.Method != captured.Command.Method {
		t.Fatalf("wrong PTZ request: %+v", actual)
	}

	for name, want := range params {
		if body.Command.Params[name] != want {
			t.Fatalf("%s = %v, want %v", name, body.Command.Params[name], want)
		}
	}

	if body.Command.Params[sessionIDField] != control {
		t.Fatal("PTZ control session ID changed")
	}
}

func rebindRecordedPTZReply(t *testing.T, reply signaling.Message, commandID string) signaling.Message {
	t.Helper()

	replyBody := decodeReplayObject(t, reply.Body)
	replayObjectField(t, replyBody, "command")["id"] = commandID
	reply.Body = encodeReplayJSON(t, replyBody)

	return reply
}

func awaitRecordedPTZResult(t *testing.T, result <-chan error) {
	t.Helper()

	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("PTZ result not correlated")
	}
}

func encodeReplayJSON(t *testing.T, value any) []byte {
	t.Helper()

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}

	return encoded
}
