package ring

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/internal/signaling"
)

func TestConnectionFailureTerminatesEveryChild(t *testing.T) {
	t.Parallel()

	peerDone := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}

		defer func() { _ = peer.Close() }()

		<-peerDone
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(peerDone) })

	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	connection := &SignalingConnection{
		conn:      conn,
		ctx:       ctx,
		cancel:    cancel,
		done:      make(chan struct{}),
		sessions:  make(map[string]*DeviceSession),
		playbacks: make(map[string]*PlaybackSession),
		pushes:    make(map[string]*PushSubscription),
		channels:  make(map[string]chan signaling.Message),
	}
	playback := &PlaybackSession{
		connection: connection,
		dialog:     "playback",
		events:     make(chan signaling.Message),
		done:       make(chan struct{}),
	}
	push := &PushSubscription{
		connection: connection,
		dialog:     "push",
		events:     make(chan signaling.Message),
		done:       make(chan struct{}),
	}
	connection.playbacks[playback.dialog] = playback
	connection.pushes[push.dialog] = push
	connection.channels[playback.dialog] = playback.events
	connection.channels[push.dialog] = push.events
	cause := signaling.ErrHeartbeat
	connection.fail(cause)

	deadline, stop := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(stop)

	{
		_, err := playback.Receive(deadline)
		if !errors.Is(err, cause) {
			t.Fatalf("playback error = %v", err)
		}
	}

	{
		_, err := push.Receive(deadline)
		if !errors.Is(err, cause) {
			t.Fatalf("push error = %v", err)
		}
	}

	select {
	case <-playback.done:
	default:
		t.Fatal("playback termination was not published")
	}

	select {
	case <-push.done:
	default:
		t.Fatal("push termination was not published")
	}

	if len(connection.playbacks) != 0 || len(connection.pushes) != 0 || len(connection.channels) != 0 {
		t.Fatal("child dialogs retained after connection failure")
	}
}
