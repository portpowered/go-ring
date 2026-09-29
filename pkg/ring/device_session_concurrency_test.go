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

type deviceSessionCommandEnvelope struct {
	Command deviceSessionCommand `json:"command"`
}

type deviceSessionCommand struct {
	ID     string                 `json:"id"`
	Params deviceSessionPTZParams `json:"params"`
}

type deviceSessionPTZParams struct {
	Direction string  `json:"direction"`
	Speed     float64 `json:"speed"`
}

// A failed command must not erase the next acknowledged movement. In particular,
// StopPTZ must still know which direction to stop after competing callers race.
func TestContinuousPTZFailureKeepsLaterMovement(t *testing.T) {
	t.Parallel()

	for _, axis := range []PTZAxis{PanAxis, TiltAxis} {
		t.Run(string(axis), func(t *testing.T) {
			t.Parallel()
			verifyContinuousPTZFailureKeepsLaterMovement(t, axis)
		})
	}
}

func verifyContinuousPTZFailureKeepsLaterMovement(t *testing.T, axis PTZAxis) {
	t.Helper()

	writes := make(chan signaling.Message, 3)

	core, err := signaling.NewSession(context.Background(), signaling.SessionConfig{
		DeviceID: 7, DialogID: "dialog", SignalID: "signal", ControlID: "control",
		Heartbeat: time.Minute,
		Send: func(_ context.Context, message signaling.Message) error {
			writes <- message

			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = core.Close() })

	connection := &SignalingConnection{done: make(chan struct{}), sessions: make(map[string]*DeviceSession)}
	session := &DeviceSession{
		connection: connection,
		core:       core,
		dialogID:   "dialog",
		deviceID:   7,
		signalID:   "signal",
		movement:   make(map[PTZAxis]string),
		done:       make(chan struct{}),
	}

	connection.sessions[session.dialogID] = session
	go session.watch(context.Background())

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	t.Cleanup(cancelFirst)

	firstDone := make(chan error, 1)

	go func() { firstDone <- sendContinuousPTZMove(firstCtx, session, axis, true) }()

	first := nextPTZWrite(t, writes)
	secondDone := make(chan error, 1)

	go func() { secondDone <- sendContinuousPTZMove(context.Background(), session, axis, false) }()

	cancelFirst()

	err = <-firstDone
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("first move error = %v", err)
	}

	second := nextPTZWrite(t, writes)
	if first.Method != protocol.MethodRPC || second.Method != protocol.MethodRPC {
		t.Fatalf("movement methods = %s, %s", first.Method, second.Method)
	}

	replyPTZSuccess(t, core, second)

	err = <-secondDone
	if err != nil {
		t.Fatal(err)
	}

	stopDone := make(chan error, 1)

	go func() {
		_, stopErr := session.StopPTZ(context.Background(), StopPTZRequest{Axis: axis})
		stopDone <- stopErr
	}()

	stop := nextPTZWrite(t, writes)

	var body deviceSessionCommandEnvelope

	err = json.Unmarshal(stop.Body, &body)
	if err != nil {
		t.Fatal(err)
	}

	want := expectedPTZStopDirection(axis)
	if body.Command.Params.Direction != want || body.Command.Params.Speed != 0 {
		t.Fatalf("safety stop = %+v, want direction %s at zero speed", body.Command.Params, want)
	}

	replyPTZSuccess(t, core, stop)

	err = <-stopDone
	if err != nil {
		t.Fatal(err)
	}
}

func sendContinuousPTZMove(ctx context.Context, session *DeviceSession, axis PTZAxis, first bool) error {
	if axis == PanAxis {
		direction := PanRight
		if first {
			direction = PanLeft
		}

		_, err := session.PanContinuous(ctx, PanContinuousRequest{Direction: direction, Speed: 0.5})

		return err
	}

	direction := TiltDown
	if first {
		direction = TiltUp
	}

	_, err := session.TiltContinuous(ctx, TiltContinuousRequest{Direction: direction, Speed: 0.5})

	return err
}

func expectedPTZStopDirection(axis PTZAxis) string {
	if axis == TiltAxis {
		return "DOWN"
	}

	return "RIGHT"
}

func TestFailedReplacementPTZKeepsAcknowledgedMovement(t *testing.T) {
	t.Parallel()

	writes := make(chan signaling.Message, 3)

	core, err := signaling.NewSession(context.Background(), signaling.SessionConfig{
		DeviceID: 7, DialogID: "dialog", SignalID: "signal", ControlID: "control", Heartbeat: time.Minute,
		Send: func(_ context.Context, m signaling.Message) error {
			writes <- m

			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = core.Close() })

	connection := &SignalingConnection{done: make(chan struct{}), sessions: make(map[string]*DeviceSession)}
	session := &DeviceSession{
		connection: connection,
		core:       core,
		dialogID:   "dialog",
		deviceID:   7,
		signalID:   "signal",
		movement:   make(map[PTZAxis]string),
		done:       make(chan struct{}),
	}

	connection.sessions[session.dialogID] = session

	go session.watch(context.Background())

	firstDone := make(chan error, 1)

	go func() {
		_, err := session.PanContinuous(context.Background(), PanContinuousRequest{Direction: PanLeft, Speed: 0.5})
		firstDone <- err
	}()

	first := nextPTZWrite(t, writes)
	replyPTZSuccess(t, core, first)

	{
		err := <-firstDone
		if err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	secondDone := make(chan error, 1)

	go func() {
		_, err := session.PanContinuous(ctx, PanContinuousRequest{Direction: PanRight, Speed: 0.5})
		secondDone <- err
	}()

	_ = nextPTZWrite(t, writes)

	cancel()

	{
		err := <-secondDone
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("replacement error = %v", err)
		}
	}

	stopDone := make(chan error, 1)

	go func() {
		_, err := session.StopPTZ(context.Background(), StopPTZRequest{Axis: PanAxis})
		stopDone <- err
	}()

	stop := nextPTZWrite(t, writes)

	var body deviceSessionCommandEnvelope

	{
		err := json.Unmarshal(stop.Body, &body)
		if err != nil {
			t.Fatal(err)
		}
	}

	if body.Command.Params.Direction != "LEFT" || body.Command.Params.Speed != 0 {
		t.Fatalf("safety stop = %+v", body.Command.Params)
	}

	replyPTZSuccess(t, core, stop)

	{
		err := <-stopDone
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestCloseWaitsForInFlightPTZAndSendsSafetyStop(t *testing.T) {
	t.Parallel()

	writes := make(chan signaling.Message, 3)

	core, err := signaling.NewSession(context.Background(), signaling.SessionConfig{
		DeviceID: 7, DialogID: "dialog", SignalID: "signal", ControlID: "control", Heartbeat: time.Minute,
		Send: func(_ context.Context, m signaling.Message) error {
			writes <- m

			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = core.Close() })

	connection := &SignalingConnection{done: make(chan struct{}), sessions: make(map[string]*DeviceSession)}
	session := &DeviceSession{
		connection: connection,
		core:       core,
		dialogID:   "dialog",
		deviceID:   7,
		signalID:   "signal",
		movement:   make(map[PTZAxis]string),
		done:       make(chan struct{}),
	}

	connection.sessions[session.dialogID] = session

	go session.watch(context.Background())

	moveDone := make(chan error, 1)

	go func() {
		_, err := session.PanContinuous(context.Background(), PanContinuousRequest{Direction: PanRight, Speed: 0.5})
		moveDone <- err
	}()

	move := nextPTZWrite(t, writes)
	closeDone := make(chan error, 1)

	go func() { closeDone <- session.Close() }()

	select {
	case premature := <-writes:
		t.Fatalf("close sent %s before the pending move was acknowledged", premature.Method)
	default:
	}

	replyPTZSuccess(t, core, move)

	{
		err := <-moveDone
		if err != nil {
			t.Fatal(err)
		}
	}

	stop := nextPTZWrite(t, writes)

	var body deviceSessionCommandEnvelope

	{
		err := json.Unmarshal(stop.Body, &body)
		if err != nil {
			t.Fatal(err)
		}
	}

	if body.Command.Params.Direction != "RIGHT" || body.Command.Params.Speed != 0 {
		t.Fatalf("close safety stop = %+v", body.Command.Params)
	}

	replyPTZSuccess(t, core, stop)

	if final := nextPTZWrite(t, writes); final.Method != protocol.MethodClose {
		t.Fatalf("final method = %s", final.Method)
	}

	{
		err := <-closeDone
		if err != nil {
			t.Fatal(err)
		}
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

	var command deviceSessionCommandEnvelope
	{
		err := json.Unmarshal(request.Body, &command)
		if err != nil {
			t.Fatal(err)
		}
	}

	response, err := json.Marshal(
		map[string]any{
			"doorbot_id": 7,
			"session_id": "signal",
			"command": map[string]any{
				"jsonrpc": "2.0",
				"id":      command.Command.ID,
				"result":  map[string]any{"sessionId": "control"},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	{
		err := core.Handle(signaling.Message{Method: protocol.MethodRPC, DialogID: "dialog", Body: response})
		if err != nil {
			t.Fatal(err)
		}
	}
}
