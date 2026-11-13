package ring_test

import (
	"testing"

	"github.com/portpowered/go-ring/pkg/ring"
)

func TestSessionEventPTZHaltReadsRecordedPanHalt(t *testing.T) {
	t.Parallel()

	// The command of the PTZ.Pan.Halted frame recorded in flow-21.
	event := ring.SessionEvent{Method: "rpc", Body: []byte(`{"doorbot_id":1000,"session_id":"session-2",` +
		`"command":{"id":"command-23","jsonrpc":"2.0","method":"PTZ.Pan.Halted","params":` +
		`{"direction":"LEFT","reason":"LIMIT_REACHED","sessionId":"session-4","timestamp":2929592333000.0,"version":1.0}}}`)}

	halt, ok := event.PTZHalt()
	if !ok {
		t.Fatal("PTZHalt did not read the recorded halt")
	}

	want := ring.PTZHalt{
		Axis: ring.PanAxis, Direction: ring.PanLeft, Reason: ring.PTZHaltLimitReached, SessionID: "session-4",
	}
	if halt != want {
		t.Fatalf("PTZHalt = %#v, want %#v", halt, want)
	}
}

func TestSessionEventPTZHaltIgnoresOtherEvents(t *testing.T) {
	t.Parallel()

	for name, event := range map[string]ring.SessionEvent{
		"camera started": {Method: "camera_started", Body: []byte(`{"doorbot_id":1000,"session_id":"s"}`)},
		"other rpc": {Method: "rpc", Body: []byte(`{"command":{"jsonrpc":"2.0","id":"c",` +
			`"method":"PTZ.Pan.Step","params":{"direction":"LEFT"}}}`)},
		"bad direction": {Method: "rpc", Body: []byte(`{"command":{"jsonrpc":"2.0","id":"c",` +
			`"method":"PTZ.Pan.Halted","params":{"direction":"UP","reason":"LIMIT_REACHED"}}}`)},
		"not json": {Method: "rpc", Body: []byte(`{`)},
	} {
		if _, ok := event.PTZHalt(); ok {
			t.Errorf("%s: PTZHalt reported a halt", name)
		}
	}
}
