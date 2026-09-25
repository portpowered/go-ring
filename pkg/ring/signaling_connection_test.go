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

func TestSignalingSendCanCancelWhileWaitingForWriter(t *testing.T) {
	c := &SignalingConnection{done: make(chan struct{}), writeGate: make(chan struct{}, 1)}
	c.writeGate <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := c.send(ctx, signaling.Message{Method: "queued"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued send error = %v, want deadline exceeded", err)
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
	c, err := NewClient(WithAccessToken("token"), WithHTTPClient(tickets.Client()), WithEndpoints(Endpoints{SolutionsBaseURL: tickets.URL}), WithRTCWebSocketURL("wss://host.invalid/?token={token}"), WithWebSocketDialer(failingSignalingDialer{}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.OpenSignaling(context.Background(), OpenSignalingRequest{})
	if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "secret-ticket") {
		t.Fatalf("dial failure leaked ticket details: %v", err)
	}
}
