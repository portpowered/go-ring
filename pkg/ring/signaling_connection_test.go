package ring

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/signaling"
	dependencywebsocket "github.com/portpowered/go-ring/pkg/dependencies/websocket"
	"github.com/portpowered/go-ring/pkg/generatedsignaling"
)

type signalingTestError string

func (failure signalingTestError) Error() string { return string(failure) }

type signalingWriteObserver struct {
	net.Conn

	trackWrites atomic.Bool
	started     chan struct{}
	startOnce   sync.Once
}

func (conn *signalingWriteObserver) Write(data []byte) (int, error) {
	if conn.trackWrites.Load() {
		conn.startOnce.Do(func() { close(conn.started) })
	}

	written, err := conn.Conn.Write(data)
	if err != nil {
		return written, signalingTestTransportError{cause: err}
	}

	return written, nil
}

type signalingTestTransportError struct{ cause error }

func (failure signalingTestTransportError) Error() string {
	return "signaling test transport: " + failure.cause.Error()
}

func (failure signalingTestTransportError) Unwrap() error { return failure.cause }

func newBlockedSignalingWebSocket(t *testing.T) (*websocket.Conn, *signalingWriteObserver) {
	t.Helper()

	const socketWriteBufferBytes = 4 << 10

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	allowPeerRead := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		defer func() { _ = conn.Close() }()

		<-allowPeerRead

		_, _, _ = conn.ReadMessage()
	}))

	observedConn := &signalingWriteObserver{
		Conn:        nil,
		started:     make(chan struct{}),
		trackWrites: atomic.Bool{},
		startOnce:   sync.Once{},
	}

	var ws *websocket.Conn

	t.Cleanup(func() {
		close(allowPeerRead)

		if ws != nil {
			_ = ws.Close()
		}

		serverClosed := make(chan struct{})

		go func() {
			server.Close()
			close(serverClosed)
		}()

		select {
		case <-serverClosed:
		case <-time.After(time.Second):
			t.Error("local peer did not exit during cleanup")
		}
	})

	var netDialer net.Dialer

	dialer := &websocket.Dialer{
		HandshakeTimeout: time.Second,
		NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := netDialer.DialContext(ctx, network, address)
			if err != nil {
				return nil, signalingTestTransportError{cause: err}
			}

			observedConn.Conn = conn

			return observedConn, nil
		},
	}
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	ws, response, err := dialer.Dial(wsURL, nil)

	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		t.Fatal(err)
	}

	tcpConn, ok := observedConn.Conn.(*net.TCPConn)
	if !ok {
		t.Fatal("local signaling connection is not TCP")
	}

	err = tcpConn.SetWriteBuffer(socketWriteBufferBytes)
	if err != nil {
		t.Fatalf("set small client socket write buffer: %v", err)
	}

	observedConn.trackWrites.Store(true)

	return ws, observedConn
}

func TestSignalingWriteDeadlineInterruptsBlockedSocketWrite(t *testing.T) {
	t.Parallel()

	ws, observedConn := newBlockedSignalingWebSocket(t)

	done := make(chan struct{})
	signalingConnection := &SignalingConnection{conn: ws, done: done}

	signalingConnection.writer = dependencywebsocket.NewSignalingWriter(done, signalingConnection.writeFrame, nil)

	go signalingConnection.writer.Run()

	body, err := json.Marshal(generatedsignaling.LiveViewBody{
		DoorbotId:            1000,
		Sdp:                  strings.Repeat("x", 1<<20),
		StreamOptions:        nil,
		ReservedType:         "",
		AdditionalProperties: nil,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sendDone := make(chan error, 1)

	go func() {
		sendDone <- signalingConnection.send(ctx, signaling.Message{
			Method: protocol.MethodLiveView, DialogID: "synthetic-dialog", Body: body,
		})
	}()

	select {
	case <-observedConn.started:
	case <-time.After(time.Second):
		t.Fatal("writer did not start the WebSocket frame write")
	}

	select {
	case err := <-sendDone:
		t.Fatalf("send completed before cancellation: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()

	select {
	case err := <-sendDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled write error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("send remained blocked after cancellation")
	}

	close(done)

	select {
	case <-signalingConnection.writer.Finished():
	case <-time.After(time.Second):
		t.Fatal("writer remained blocked after canceled socket write")
	}
}

type failingSignalingDialer struct{}

func (failingSignalingDialer) DialContext(
	context.Context,
	string,
	http.Header,
) (*websocket.Conn, *http.Response, error) {
	return nil, nil, signalingTestError("secret-ticket-must-not-escape")
}

func TestSignalingDialerOptionAndFailureAreSafe(t *testing.T) {
	t.Parallel()

	{
		_, err := NewClient(WithWebSocketDialer(nil))
		if err == nil {
			t.Fatal("nil signaling dialer was accepted")
		}
	}

	tickets := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ticket":"private"}`)) },
		),
	)
	t.Cleanup(tickets.Close)

	signalingConnection, err := NewClient(
		WithHTTPClient(tickets.Client()),
		WithEndpoints(Endpoints{SolutionsBaseURL: tickets.URL}),
		WithSignalingWebSocketURL("wss://host.invalid/?token={token}"),
		WithWebSocketDialer(failingSignalingDialer{}),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = signalingConnection.OpenSignaling(
		context.Background(),
		OpenSignalingRequest{Auth: AuthContext{AccessToken: "token", HardwareID: ""}},
	)
	if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "secret-ticket") {
		t.Fatalf("dial failure leaked ticket details: %v", err)
	}
}
