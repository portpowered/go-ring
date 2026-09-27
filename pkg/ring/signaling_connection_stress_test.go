package ring

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/internal/signaling"
	dependencywebsocket "github.com/portpowered/go-ring/pkg/dependencies/websocket"
)

// Race connection death, explicit closes, child heartbeat, and a full push
// queue. Whichever termination wins, every child must publish completion.
func TestSignalingConnectionAdversarialTerminationStress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = peer.Close() }()
		for {
			if _, _, err := peer.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	const iterations = 40
	for iteration := 0; iteration < iterations; iteration++ {
		ws, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		client := &Client{signalingConnections: make(map[*SignalingConnection]struct{})}
		connection := &SignalingConnection{client: client, conn: ws, ctx: ctx, cancel: cancel, done: make(chan struct{}), readerDone: make(chan struct{}), pending: make(map[string]chan signaling.Message), sessions: make(map[string]*DeviceSession), playbacks: make(map[string]*PlaybackSession), pushes: make(map[string]*PushSubscription), channels: make(map[string]chan signaling.Message)}
		connection.writer = dependencywebsocket.NewSignalingWriter(connection.done, connection.writeFrame, connection.fail)
		client.signalingConnections[connection] = struct{}{}
		playback := &PlaybackSession{connection: connection, dialog: "playback", id: "playback-id", events: make(chan signaling.Message, 1), done: make(chan struct{})}
		playback.lastPong.Store(time.Now().UnixNano())
		push := &PushSubscription{connection: connection, dialog: "push", id: "push-id", events: make(chan signaling.Message, 1), done: make(chan struct{})}
		connection.playbacks[playback.dialog] = playback
		connection.pushes[push.dialog] = push
		connection.channels[playback.dialog] = playback.events
		connection.channels[push.dialog] = push.events
		go connection.readLoop()
		go connection.writer.Run()
		start := make(chan struct{})
		var workers sync.WaitGroup
		workers.Add(7)
		go func() { defer workers.Done(); <-start; _ = connection.Close() }()
		go func() { defer workers.Done(); <-start; connection.fail(signaling.ErrBackpressure) }()
		go func() { defer workers.Done(); <-start; _ = playback.Close() }()
		go func() { defer workers.Done(); <-start; _ = push.Close() }()
		go func() { defer workers.Done(); <-start; playback.keepalive(context.Background(), time.Millisecond) }()
		go func() { defer workers.Done(); <-start; push.heartbeatAt(context.Background(), time.Millisecond) }()
		go func() {
			defer workers.Done()
			<-start
			for i := 0; i < 4; i++ {
				connection.route(signaling.Message{Method: "push_event", DialogID: push.dialog})
			}
		}()
		close(start)
		finished := make(chan struct{})
		go func() { workers.Wait(); close(finished) }()
		select {
		case <-finished:
		case <-time.After(2 * time.Second):
			t.Fatalf("iteration %d deadlocked", iteration)
		}
		select {
		case <-playback.done:
		default:
			t.Fatalf("iteration %d playback did not terminate", iteration)
		}
		select {
		case <-push.done:
		default:
			t.Fatalf("iteration %d push did not terminate", iteration)
		}
		connection.mu.Lock()
		remaining := len(connection.playbacks) + len(connection.pushes) + len(connection.channels)
		connection.mu.Unlock()
		if remaining != 0 {
			t.Fatalf("iteration %d retained %d child dialogs", iteration, remaining)
		}
		cancel()
	}
}
