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
	peerDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer peer.Close()
		<-peerDone
	}))
	defer server.Close()
	defer close(peerDone)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &Client{signalingConnections: make(map[*SignalingConnection]struct{})}
	c := &SignalingConnection{client: client, conn: conn, ctx: ctx, cancel: cancel, done: make(chan struct{}), sessions: make(map[string]*DeviceSession), playbacks: make(map[string]*PlaybackSession), pushes: make(map[string]*PushSubscription), channels: make(map[string]chan signaling.Message)}
	client.signalingConnections[c] = struct{}{}
	playback := &PlaybackSession{connection: c, dialog: "playback", events: make(chan signaling.Message), done: make(chan struct{})}
	push := &PushSubscription{connection: c, dialog: "push", events: make(chan signaling.Message), done: make(chan struct{})}
	c.playbacks[playback.dialog] = playback
	c.pushes[push.dialog] = push
	c.channels[playback.dialog] = playback.events
	c.channels[push.dialog] = push.events
	cause := signaling.ErrHeartbeat
	c.fail(cause)
	deadline, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if _, err := playback.Receive(deadline); !errors.Is(err, cause) {
		t.Fatalf("playback error = %v", err)
	}
	if _, err := push.Receive(deadline); !errors.Is(err, cause) {
		t.Fatalf("push error = %v", err)
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
	if len(c.playbacks) != 0 || len(c.pushes) != 0 || len(c.channels) != 0 {
		t.Fatal("child dialogs retained after connection failure")
	}
	if len(client.signalingConnections) != 0 {
		t.Fatal("failed connection retained by client")
	}
}
