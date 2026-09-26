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
	"github.com/portpowered/go-ring/internal/signaling"
	dependencywebsocket "github.com/portpowered/go-ring/pkg/dependencies/websocket"
)

func TestSignalingWriteDeadlineInterruptsBlockedSocketWrite(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	serverDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			close(serverDone)
			return
		}
		defer conn.Close()
		time.Sleep(250 * time.Millisecond) // Deliberately leave the client's large write undrained.
		_, _, _ = conn.ReadMessage()
		close(serverDone)
	}))
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	s := &SignalingConnection{conn: ws, done: done}
	s.writer = dependencywebsocket.NewSignalingWriter(done, s.writeFrame, nil)
	go s.writer.Run()
	body := json.RawMessage("\"" + strings.Repeat("x", 8<<20) + "\"")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	err = s.send(ctx, signaling.Message{Method: "large_payload", Body: body})
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked write error = %v", err)
	}
	close(done)
	_ = ws.Close()
	select {
	case <-s.writer.Finished():
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

func (failingSignalingDialer) DialContext(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error) {
	return nil, nil, errors.New("secret-ticket-must-not-escape")
}

func TestSignalingDialerOptionAndFailureAreSafe(t *testing.T) {
	if _, err := NewClient(WithWebSocketDialer(nil)); err == nil {
		t.Fatal("nil signaling dialer was accepted")
	}
	tickets := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ticket":"private"}`)) }))
	defer tickets.Close()
	c, err := NewClient(WithAccessToken("token"), WithHTTPClient(tickets.Client()), WithEndpoints(Endpoints{SolutionsBaseURL: tickets.URL}), WithSignalingWebSocketURL("wss://host.invalid/?token={token}"), WithWebSocketDialer(failingSignalingDialer{}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.OpenSignaling(context.Background(), OpenSignalingRequest{})
	if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "secret-ticket") {
		t.Fatalf("dial failure leaked ticket details: %v", err)
	}
}
