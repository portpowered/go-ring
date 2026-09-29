package ring

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestSignalingWriteDeadlineInterruptsBlockedSocketWrite(t *testing.T) {
	t.Parallel()

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	serverDone := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			close(serverDone)

			return
		}

		defer func() { _ = conn.Close() }()

		time.Sleep(250 * time.Millisecond) // Deliberately leave the client's large write undrained.

		_, _, _ = conn.ReadMessage()

		close(serverDone)
	}))
	t.Cleanup(server.Close)

	url := "ws" + strings.TrimPrefix(server.URL, "http")

	ws, response, err := websocket.DefaultDialer.Dial(url, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	signalingConnection := &SignalingConnection{conn: ws, done: done}

	signalingConnection.writer = dependencywebsocket.NewSignalingWriter(done, signalingConnection.writeFrame, nil)

	go signalingConnection.writer.Run()

	body, err := json.Marshal(generatedsignaling.LiveViewBody{
		DoorbotId:            1000,
		Sdp:                  strings.Repeat("x", 8<<20),
		StreamOptions:        nil,
		ReservedType:         "",
		AdditionalProperties: nil,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	err = signalingConnection.send(ctx, signaling.Message{
		Method: protocol.MethodLiveView, DialogID: "synthetic-dialog", Body: body,
	})

	cancel()

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked write error = %v", err)
	}

	close(done)

	_ = ws.Close()

	select {
	case <-signalingConnection.writer.Finished():
	case <-time.After(time.Second):
		t.Fatal("writer remained blocked after canceled socket write")
	}

	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("local peer did not observe closed socket")
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
