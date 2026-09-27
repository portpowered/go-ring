package ring

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/signaling"
)

// Exercise public wrapper cleanup while PTZ calls and core failure race.
func TestDeviceSessionAdversarialCloseStress(t *testing.T) {
	const iterations = 100
	for iteration := 0; iteration < iterations; iteration++ {
		var core *signaling.Session
		var err error
		core, err = signaling.NewSession(context.Background(), signaling.SessionConfig{
			DeviceID: 7, DialogID: "dialog", SignalID: "signal", ControlID: "control", Heartbeat: time.Minute,
			Send: func(_ context.Context, message signaling.Message) error {
				if message.Method != protocol.MethodRPC {
					return nil
				}
				var request deviceSessionCommandEnvelope
				if decodeErr := json.Unmarshal(message.Body, &request); decodeErr != nil {
					return decodeErr
				}
				body, encodeErr := json.Marshal(map[string]any{"doorbot_id": 7, "session_id": "signal", "command": map[string]any{"jsonrpc": "2.0", "id": request.Command.ID, "result": map[string]any{"sessionId": "control"}}})
				if encodeErr != nil {
					return encodeErr
				}
				go func() { _ = core.Handle(signaling.Message{Method: protocol.MethodRPC, DialogID: "dialog", Body: body}) }()
				return nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		connection := &SignalingConnection{done: make(chan struct{}), sessions: make(map[string]*DeviceSession)}
		session := &DeviceSession{connection: connection, core: core, dialogID: "dialog", deviceID: 7, signalID: "signal", movement: make(map[PTZAxis]string), done: make(chan struct{})}
		connection.sessions[session.dialogID] = session
		go session.watch(context.Background())
		start := make(chan struct{})
		var workers sync.WaitGroup
		workers.Add(6)
		go func() {
			defer workers.Done()
			<-start
			_, _ = session.PanContinuous(context.Background(), PanContinuousRequest{Direction: PanLeft, Speed: 0.5})
		}()
		go func() {
			defer workers.Done()
			<-start
			_, _ = session.PanContinuous(context.Background(), PanContinuousRequest{Direction: PanRight, Speed: 0.5})
		}()
		go func() {
			defer workers.Done()
			<-start
			_, _ = session.TiltContinuous(context.Background(), TiltContinuousRequest{Direction: TiltUp, Speed: 0.5})
		}()
		go func() {
			defer workers.Done()
			<-start
			_, _ = session.StopPTZ(context.Background(), StopPTZRequest{Axis: PanAxis})
		}()
		go func() { defer workers.Done(); <-start; _ = session.Close() }()
		go func() { defer workers.Done(); <-start; core.Fail(signaling.ErrBackpressure) }()
		close(start)
		finished := make(chan struct{})
		go func() { workers.Wait(); close(finished) }()
		select {
		case <-finished:
		case <-time.After(2 * time.Second):
			t.Fatalf("iteration %d deadlocked", iteration)
		}
		waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = session.Wait(waitCtx)
		cancel()
		if pending := core.Pending(); pending != 0 {
			t.Fatalf("iteration %d retained %d RPC waiters", iteration, pending)
		}
		connection.mu.Lock()
		_, retained := connection.sessions[session.dialogID]
		connection.mu.Unlock()
		if retained {
			t.Fatalf("iteration %d retained closed session", iteration)
		}
	}
}
