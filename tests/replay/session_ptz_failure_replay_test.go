package replay_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/pkg/ring"
)

// An unanswered captured PTZ command must release its tracked movement so a
// later stop cannot accidentally address a movement the device never accepted.
func TestRecordedPTZUnansweredMovementClearsTrackedState(t *testing.T) {
	offer, captured := recordedLiveView(t)
	for _, axis := range []ring.PTZAxis{ring.PanAxis, ring.TiltAxis} {
		t.Run(string(axis), func(t *testing.T) {
			conn := identityPeer(t, func(c *websocket.Conn, dialog string) {
				if !serveRecordedNegotiation(t, c, dialog, captured, false) {
					return
				}
				var request map[string]any
				if err := c.ReadJSON(&request); err != nil {
					t.Error(err)
					return
				}
				if request["method"] != "rpc" {
					t.Errorf("movement frame = %v", request["method"])
					return
				}
				command := request["body"].(map[string]any)["command"].(map[string]any)
				want := "PTZ.Pan.Continuous"
				if axis == ring.TiltAxis {
					want = "PTZ.Tilt.Continuous"
				}
				if command["method"] != want {
					t.Errorf("movement method = %v", command["method"])
				}
				_, _, _ = c.ReadMessage()
			})
			session := startRecordedSession(t, conn, offer)
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			var err error
			if axis == ring.PanAxis {
				_, err = session.PanContinuous(ctx, ring.PanContinuousRequest{Direction: ring.PanLeft, Speed: 0.5})
			} else {
				_, err = session.TiltContinuous(ctx, ring.TiltContinuousRequest{Direction: ring.TiltUp, Speed: 0.5})
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("unanswered movement = %v", err)
			}
			if _, err := session.StopPTZ(context.Background(), ring.StopPTZRequest{Axis: axis}); err == nil {
				t.Fatal("unconfirmed movement remained tracked")
			}
		})
	}
}
