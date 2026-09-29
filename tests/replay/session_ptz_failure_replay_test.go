package replay_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/pkg/ring"
)

const (
	panContinuousMethod  = "PTZ.Pan.Continuous"
	tiltContinuousMethod = "PTZ.Tilt.Continuous"
)

// An unanswered captured PTZ command must release its tracked movement so a
// later stop cannot accidentally address a movement the device never accepted.
func TestRecordedPTZUnansweredMovementClearsTrackedState(t *testing.T) {
	t.Parallel()

	offer, captured := recordedLiveView(t)

	for _, axis := range []ring.PTZAxis{ring.PanAxis, ring.TiltAxis} {
		t.Run(string(axis), func(t *testing.T) {
			t.Parallel()
			assertUnansweredMovementClearsTrackedState(t, offer, captured, axis)
		})
	}
}

func assertUnansweredMovementClearsTrackedState(
	t *testing.T,
	offer string,
	captured map[string]json.RawMessage,
	axis ring.PTZAxis,
) {
	t.Helper()

	connection := identityPeer(t, func(socket *websocket.Conn, dialog string) {
		if !serveRecordedNegotiation(t, socket, dialog, captured, false) {
			return
		}

		assertUnansweredMovementFrame(t, socket, axis)
	})
	session := startRecordedSession(t, connection, offer)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := sendContinuousMovement(ctx, session, axis)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unanswered movement = %v", err)
	}

	_, stopErr := session.StopPTZ(context.Background(), ring.StopPTZRequest{Axis: axis})
	if stopErr == nil {
		t.Fatal("unconfirmed movement remained tracked")
	}
}

func assertUnansweredMovementFrame(t *testing.T, socket *websocket.Conn, axis ring.PTZAxis) {
	t.Helper()

	var request map[string]any

	err := socket.ReadJSON(&request)
	if err != nil {
		t.Error(err)

		return
	}

	if request["method"] != "rpc" {
		t.Errorf("movement frame = %v", request["method"])

		return
	}

	requestBody, ok := request["body"].(map[string]any)
	if !ok {
		t.Errorf("movement request body has type %T", request["body"])

		return
	}

	command, ok := requestBody["command"].(map[string]any)
	if !ok {
		t.Errorf("movement command has type %T", requestBody["command"])

		return
	}

	want := panContinuousMethod
	if axis == ring.TiltAxis {
		want = tiltContinuousMethod
	}

	if command["method"] != want {
		t.Errorf("movement method = %v", command["method"])
	}

	_, _, readErr := socket.ReadMessage()
	if readErr != nil {
		return
	}
}

func sendContinuousMovement(ctx context.Context, session *ring.DeviceSession, axis ring.PTZAxis) error {
	if axis == ring.PanAxis {
		_, err := session.PanContinuous(ctx, ring.PanContinuousRequest{Direction: ring.PanLeft, Speed: 0.5})

		return wrapReplayTestError("send continuous movement", err)
	}

	_, err := session.TiltContinuous(ctx, ring.TiltContinuousRequest{Direction: ring.TiltUp, Speed: 0.5})

	return wrapReplayTestError("send continuous movement", err)
}
