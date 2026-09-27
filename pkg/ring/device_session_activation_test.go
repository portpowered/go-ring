package ring

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/signaling"
)

func TestActivationReportsEachFailedStage(t *testing.T) {
	for _, test := range []struct {
		name      string
		failSend  int
		canceled  bool
		wantError string
	}{
		{name: "activation", canceled: true, wantError: "signaling send canceled"},
		{name: "microphone", failSend: 1, wantError: "failed to set microphone"},
		{name: "stream options", failSend: 2, wantError: "failed to set stream options"},
	} {
		t.Run(test.name, func(t *testing.T) {
			connection, _ := replayConnection(t)
			sends := 0
			core, err := signaling.NewSession(context.Background(), signaling.SessionConfig{
				DeviceID: 7, DialogID: "dialog", SignalID: "signal", ControlID: "control", Heartbeat: time.Minute,
				Send: func(context.Context, signaling.Message) error {
					sends++
					if sends == test.failSend {
						return signaling.ErrClosed
					}
					return nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = core.Close() })
			session := &DeviceSession{dialogID: "dialog", riid: "route", core: core}
			ctx := context.Background()
			if test.canceled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			err = connection.activateDeviceSession(ctx, session, 7, "signal", StartDeviceSessionRequest{})
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("activation error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestDeviceSessionTerminalBeforeCameraStarted(t *testing.T) {
	connection := &SignalingConnection{done: make(chan struct{})}
	session := &DeviceSession{done: make(chan struct{}), terminal: signaling.ErrClosed}
	close(session.done)
	err := connection.waitForCameraStarted(context.Background(), context.Background(), nil, session, nil)
	if !errors.Is(err, signaling.ErrClosed) {
		t.Fatalf("terminal error = %v, want closed", err)
	}
}

func TestMustJSONRejectsUnsupportedValue(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("unsupported JSON value did not panic")
		}
	}()
	_ = mustJSON(make(chan int))
}
