package replay_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/pkg/ring"
)

const recordedHaltWait = 5 * time.Second

// recordedHaltCase is one PTZ halt notice the peer sends on the live session, built from the
// recorded PTZ.Pan.Halted command, and what the public session must report for it.
type recordedHaltCase struct {
	name      string
	method    string
	direction string
	wrapped   bool
	want      ring.PTZHalt
	wantHalt  bool
}

// Ring tells the session that a turn reached the end of an axis with an rpc notice. flow-402 holds
// PTZ.Pan.Halted unwrapped. A live camera wraps the same command as command.message and sends
// PTZ.Tilt.Halted the same way (seen 2026-11-13). Each notice must reach the public session as an
// event that PTZHalt reads, and the session must stay live after them.
func TestRecordedPTZHaltsReachTheLiveSession(t *testing.T) {
	t.Parallel()

	offer, captured := recordedLiveView(t)
	halt := recordedPTZHaltCommand(t)
	signalID := recordedSignalID(t, captured["session_created"])

	var answer recordedSessionEnvelope

	err := json.Unmarshal(captured["sdp"], &answer)
	if err != nil {
		t.Fatal(err)
	}

	controlID := answer.Body.Info.SessionID
	cases := recordedHaltCases(controlID)

	conn := identityPeer(t, func(connection *websocket.Conn, dialog string) {
		if !serveRecordedNegotiation(t, connection, dialog, captured, false) {
			return
		}

		for _, haltCase := range cases {
			frame := map[string]any{
				"method":    "rpc",
				"dialog_id": dialog,
				"body": map[string]any{
					"doorbot_id": 1001,
					"session_id": signalID,
					"command":    recordedHaltCommandFor(t, halt, haltCase, controlID),
				},
			}

			if !writeRecordedNegotiationFrame(t, connection, frame) {
				return
			}
		}

		_, _, _ = connection.ReadMessage()
	})
	session := startRecordedSession(t, conn, offer)

	ctx, cancel := context.WithTimeout(context.Background(), recordedHaltWait)
	defer cancel()

	for _, haltCase := range cases {
		event := receiveRecordedRPCEvent(t, ctx, session)

		got, ok := event.PTZHalt()
		if ok != haltCase.wantHalt || got != haltCase.want {
			t.Errorf("%s: PTZHalt = %#v, %t; want %#v, %t", haltCase.name, got, ok, haltCase.want, haltCase.wantHalt)
		}
	}

	if session.State() != ring.SessionActive {
		t.Fatalf("session state after PTZ halts = %v", session.State())
	}
}

func recordedHaltCases(controlID string) []recordedHaltCase {
	halt := func(axis ring.PTZAxis, direction string) ring.PTZHalt {
		return ring.PTZHalt{
			Axis: axis, Direction: ring.PTZDirection(direction), Reason: ring.PTZHaltLimitReached,
			SessionID: controlID,
		}
	}

	return []recordedHaltCase{
		{
			name: "recorded pan halt", method: "PTZ.Pan.Halted", direction: "RIGHT", wrapped: false,
			want: halt(ring.PanAxis, "RIGHT"), wantHalt: true,
		},
		{
			name: "wrapped pan halt", method: "PTZ.Pan.Halted", direction: "LEFT", wrapped: true,
			want: halt(ring.PanAxis, "LEFT"), wantHalt: true,
		},
		{
			name: "wrapped tilt halt", method: "PTZ.Tilt.Halted", direction: "DOWN", wrapped: true,
			want: halt(ring.TiltAxis, "DOWN"), wantHalt: true,
		},
		{
			name: "tilt halt", method: "PTZ.Tilt.Halted", direction: "UP", wrapped: false,
			want: halt(ring.TiltAxis, "UP"), wantHalt: true,
		},
		{
			name: "tilt halt naming a pan end", method: "PTZ.Tilt.Halted", direction: "LEFT", wrapped: true,
			want: ring.PTZHalt{Axis: "", Direction: "", Reason: "", SessionID: ""}, wantHalt: false,
		},
	}
}

// recordedPTZHaltCommand returns the command of the PTZ.Pan.Halted notice recorded in flow-402,
// the recording recordedLiveView negotiates with.
func recordedPTZHaltCommand(t *testing.T) json.RawMessage {
	t.Helper()

	for _, row := range loadConversation(t, "flow-402.json").Messages {
		if row.Direction != capturedServerToClientDirection || row.Payload.Method != "rpc" {
			continue
		}

		var body struct {
			Command json.RawMessage `json:"command"`
		}

		err := json.Unmarshal(row.Payload.Body, &body)
		if err != nil {
			t.Fatal(err)
		}

		var command recordedRPCCommand

		err = json.Unmarshal(body.Command, &command)
		if err != nil {
			t.Fatal(err)
		}

		if command.Method == "PTZ.Pan.Halted" {
			return body.Command
		}
	}

	t.Fatal("flow-402 has no recorded PTZ.Pan.Halted notice")

	return nil
}

// recordedHaltCommandFor rebinds the recorded halt command to the case's method and direction and
// to the negotiated control session. A wrapped case nests it as command.message the way a live
// camera sends it.
func recordedHaltCommandFor(
	t *testing.T,
	recorded json.RawMessage,
	haltCase recordedHaltCase,
	controlID string,
) map[string]any {
	t.Helper()

	command := decodeReplayObject(t, recorded)
	command["method"] = haltCase.method

	params := replayObjectField(t, command, "params")
	params["direction"] = haltCase.direction
	params[sessionIDField] = controlID

	if haltCase.wrapped {
		return map[string]any{"destination": "app", "message": command}
	}

	return command
}

func receiveRecordedRPCEvent(t *testing.T, ctx context.Context, session *ring.DeviceSession) *ring.SessionEvent {
	t.Helper()

	for {
		event, err := session.Receive(ctx)
		if err != nil {
			t.Fatalf("session ended before the PTZ halt notice arrived: %v", err)
		}

		if event.Method == "rpc" {
			return event
		}
	}
}
