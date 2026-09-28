package ring

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/signaling"
	"github.com/portpowered/go-ring/internal/testkit/replay"
	dependencywebsocket "github.com/portpowered/go-ring/pkg/dependencies/websocket"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

func capturedLiveFrame(t *testing.T, direction, method string) signaling.Message {
	t.Helper()
	recording, err := replay.LoadSessionRecording(filepath.Join("..", "..", "tests", "replay", "fixtures", "signaling", "historical", "flow-21.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range recording.Messages {
		var message signaling.Message
		if err := json.Unmarshal(row.Payload, &message); err != nil {
			t.Fatal(err)
		}
		if row.Direction == direction && message.DialogID == "dialog-3" && message.Method == method {
			return message
		}
	}
	t.Fatalf("missing captured live-view %s %s", direction, method)
	return signaling.Message{}
}

func TestStartDeviceSessionReportsActivationFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	writes := make(chan signaling.Message, 8)
	connection := &SignalingConnection{
		ctx: ctx, cancel: cancel, done: make(chan struct{}),
		channels: make(map[string]chan signaling.Message), pending: make(map[string]chan signaling.Message),
		sessions: make(map[string]*DeviceSession), playbacks: make(map[string]*PlaybackSession),
	}
	connection.writer = dependencywebsocket.NewSignalingWriter(connection.done, func(_ context.Context, message signaling.Message) error {
		if message.Method == protocol.MethodActivateSession {
			return signaling.ErrClosed
		}
		writes <- message
		return nil
	}, nil)
	go connection.writer.Run()
	t.Cleanup(func() { close(connection.done); cancel(); <-connection.writer.Finished() })
	var offer struct {
		SDP string `json:"sdp"`
	}
	if err := json.Unmarshal(capturedLiveFrame(t, "client_to_server", protocol.MethodLiveView).Body, &offer); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := connection.StartDeviceSession(ctx, StartDeviceSessionRequest{DeviceID: "1000", Offer: SessionDescription{Type: SDPTypeOffer, SDP: offer.SDP}})
		result <- err
	}()
	request := <-writes
	for _, method := range []string{protocol.MethodSessionCreated, protocol.MethodSDP} {
		response := capturedLiveFrame(t, "server_to_client", method)
		response.DialogID = request.DialogID
		connection.route(response)
	}
	select {
	case err := <-result:
		if !ringapimodels.IsClosedError(err) || !errors.Is(err, signaling.ErrClosed) {
			t.Fatalf("activation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("activation failure did not terminate negotiation")
	}
}

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
