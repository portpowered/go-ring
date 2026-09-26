package ring

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/signaling"
)

// A failed command must not erase the next acknowledged movement. In particular,
// StopPTZ must still know which direction to stop after competing callers race.
func TestContinuousPTZFailureKeepsLaterMovement(t *testing.T) {
	for _, axis := range []PTZAxis{PanAxis, TiltAxis} {
		t.Run(string(axis), func(t *testing.T) {
			writes := make(chan signaling.Message, 3)
			core, err := signaling.NewSession(context.Background(), signaling.SessionConfig{
				DeviceID: 7, DialogID: "dialog", SignalID: "signal", ControlID: "control",
				Heartbeat: time.Minute,
				Send:      func(_ context.Context, m signaling.Message) error { writes <- m; return nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = core.Close() })
			connection := &SignalingConnection{done: make(chan struct{}), sessions: make(map[string]*DeviceSession)}
			s := &DeviceSession{connection: connection, core: core, dialogID: "dialog", deviceID: 7, signalID: "signal", movement: make(map[PTZAxis]string), done: make(chan struct{})}
			connection.sessions[s.dialogID] = s
			go s.watch()

			move := func(ctx context.Context, first bool) error {
				if axis == PanAxis {
					direction := PanRight
					if first {
						direction = PanLeft
					}
					_, err := s.PanContinuous(ctx, PanContinuousRequest{Direction: direction, Speed: 0.5})
					return err
				}
				direction := TiltDown
				if first {
					direction = TiltUp
				}
				_, err := s.TiltContinuous(ctx, TiltContinuousRequest{Direction: direction, Speed: 0.5})
				return err
			}
			firstCtx, cancelFirst := context.WithCancel(context.Background())
			defer cancelFirst()
			firstDone := make(chan error, 1)
			go func() { firstDone <- move(firstCtx, true) }()
			first := nextPTZWrite(t, writes)
			secondDone := make(chan error, 1)
			go func() { secondDone <- move(context.Background(), false) }()
			cancelFirst()
			if err := <-firstDone; !errors.Is(err, context.Canceled) {
				t.Fatalf("first move error = %v", err)
			}
			second := nextPTZWrite(t, writes)
			if first.Method != protocol.MethodRPC || second.Method != protocol.MethodRPC {
				t.Fatalf("movement methods = %s, %s", first.Method, second.Method)
			}
			replyPTZSuccess(t, core, second)
			if err := <-secondDone; err != nil {
				t.Fatal(err)
			}
			stopDone := make(chan error, 1)
			go func() { _, err := s.StopPTZ(context.Background(), StopPTZRequest{Axis: axis}); stopDone <- err }()
			stop := nextPTZWrite(t, writes)
			var body struct {
				Command struct {
					Params struct {
						Direction string  `json:"direction"`
						Speed     float64 `json:"speed"`
					} `json:"params"`
				} `json:"command"`
			}
			if err := json.Unmarshal(stop.Body, &body); err != nil {
				t.Fatal(err)
			}
			want := "RIGHT"
			if axis == TiltAxis {
				want = "DOWN"
			}
			if body.Command.Params.Direction != want || body.Command.Params.Speed != 0 {
				t.Fatalf("safety stop = %+v, want direction %s at zero speed", body.Command.Params, want)
			}
			replyPTZSuccess(t, core, stop)
			if err := <-stopDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFailedReplacementPTZKeepsAcknowledgedMovement(t *testing.T) {
	writes := make(chan signaling.Message, 3)
	core, err := signaling.NewSession(context.Background(), signaling.SessionConfig{
		DeviceID: 7, DialogID: "dialog", SignalID: "signal", ControlID: "control", Heartbeat: time.Minute,
		Send: func(_ context.Context, m signaling.Message) error { writes <- m; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	connection := &SignalingConnection{done: make(chan struct{}), sessions: make(map[string]*DeviceSession)}
	s := &DeviceSession{connection: connection, core: core, dialogID: "dialog", deviceID: 7, signalID: "signal", movement: make(map[PTZAxis]string), done: make(chan struct{})}
	connection.sessions[s.dialogID] = s
	go s.watch()
	firstDone := make(chan error, 1)
	go func() {
		_, err := s.PanContinuous(context.Background(), PanContinuousRequest{Direction: PanLeft, Speed: 0.5})
		firstDone <- err
	}()
	first := nextPTZWrite(t, writes)
	replyPTZSuccess(t, core, first)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	secondDone := make(chan error, 1)
	go func() {
		_, err := s.PanContinuous(ctx, PanContinuousRequest{Direction: PanRight, Speed: 0.5})
		secondDone <- err
	}()
	_ = nextPTZWrite(t, writes)
	cancel()
	if err := <-secondDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("replacement error = %v", err)
	}
	stopDone := make(chan error, 1)
	go func() { _, err := s.StopPTZ(context.Background(), StopPTZRequest{Axis: PanAxis}); stopDone <- err }()
	stop := nextPTZWrite(t, writes)
	var body struct {
		Command struct {
			Params struct {
				Direction string  `json:"direction"`
				Speed     float64 `json:"speed"`
			} `json:"params"`
		} `json:"command"`
	}
	if err := json.Unmarshal(stop.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Command.Params.Direction != "LEFT" || body.Command.Params.Speed != 0 {
		t.Fatalf("safety stop = %+v", body.Command.Params)
	}
	replyPTZSuccess(t, core, stop)
	if err := <-stopDone; err != nil {
		t.Fatal(err)
	}
}

func TestCloseWaitsForInFlightPTZAndSendsSafetyStop(t *testing.T) {
	writes := make(chan signaling.Message, 3)
	core, err := signaling.NewSession(context.Background(), signaling.SessionConfig{
		DeviceID: 7, DialogID: "dialog", SignalID: "signal", ControlID: "control", Heartbeat: time.Minute,
		Send: func(_ context.Context, m signaling.Message) error { writes <- m; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	connection := &SignalingConnection{done: make(chan struct{}), sessions: make(map[string]*DeviceSession)}
	s := &DeviceSession{connection: connection, core: core, dialogID: "dialog", deviceID: 7, signalID: "signal", movement: make(map[PTZAxis]string), done: make(chan struct{})}
	connection.sessions[s.dialogID] = s
	go s.watch()
	moveDone := make(chan error, 1)
	go func() {
		_, err := s.PanContinuous(context.Background(), PanContinuousRequest{Direction: PanRight, Speed: 0.5})
		moveDone <- err
	}()
	move := nextPTZWrite(t, writes)
	closeDone := make(chan error, 1)
	go func() { closeDone <- s.Close() }()
	select {
	case premature := <-writes:
		t.Fatalf("close sent %s before the pending move was acknowledged", premature.Method)
	default:
	}
	replyPTZSuccess(t, core, move)
	if err := <-moveDone; err != nil {
		t.Fatal(err)
	}
	stop := nextPTZWrite(t, writes)
	var body struct {
		Command struct {
			Params struct {
				Direction string  `json:"direction"`
				Speed     float64 `json:"speed"`
			} `json:"params"`
		} `json:"command"`
	}
	if err := json.Unmarshal(stop.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Command.Params.Direction != "RIGHT" || body.Command.Params.Speed != 0 {
		t.Fatalf("close safety stop = %+v", body.Command.Params)
	}
	replyPTZSuccess(t, core, stop)
	if final := nextPTZWrite(t, writes); final.Method != protocol.MethodClose {
		t.Fatalf("final method = %s", final.Method)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
}

func nextPTZWrite(t *testing.T, writes <-chan signaling.Message) signaling.Message {
	t.Helper()
	select {
	case m := <-writes:
		return m
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for PTZ write")
		return signaling.Message{}
	}
}

func replyPTZSuccess(t *testing.T, core *signaling.Session, request signaling.Message) {
	t.Helper()
	var command struct {
		Command struct {
			ID string `json:"id"`
		} `json:"command"`
	}
	if err := json.Unmarshal(request.Body, &command); err != nil {
		t.Fatal(err)
	}
	response, err := json.Marshal(map[string]any{"doorbot_id": 7, "session_id": "signal", "command": map[string]any{"jsonrpc": "2.0", "id": command.Command.ID, "result": map[string]any{"sessionId": "control"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := core.Handle(signaling.Message{Method: protocol.MethodRPC, DialogID: "dialog", Body: response}); err != nil {
		t.Fatal(err)
	}
}
