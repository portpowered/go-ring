package replay_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/pkg/ring"
)

func TestOpenContextClosesIdleSignalingSocket(t *testing.T) {
	t.Parallel()

	tickets := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"ticket":"fixture"}`)) },
		),
	)
	t.Cleanup(tickets.Close)

	remoteClosed := make(chan struct{}, 1)
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		websocketConn, upgradeErr := up.Upgrade(w, r, nil)
		if upgradeErr != nil {
			return
		}

		defer func() { _ = websocketConn.Close() }()

		_, _, _ = websocketConn.ReadMessage()

		remoteClosed <- struct{}{}
	}))
	t.Cleanup(ws.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	url := "ws" + strings.TrimPrefix(ws.URL, "http")

	client, err := ring.NewClient(
		ring.WithHTTPClient(tickets.Client()),
		ring.WithEndpoints(ring.Endpoints{
			SolutionsBaseURL: tickets.URL,
			OAuthBaseURL:     "",
			APIBaseURL:       "",
			SignalingURL:     "",
		}),
		ring.WithSignalingWebSocketURL(url),
	)
	if err != nil {
		t.Fatal(err)
	}

	conn, err := client.OpenSignaling(
		ctx,
		ring.OpenSignalingRequest{Auth: ring.AuthContext{AccessToken: "test-token", HardwareID: ""}},
	)
	if err != nil {
		t.Fatal(err)
	}

	cancel()

	done := make(chan error, 1)

	go func() { done <- conn.Close() }()

	select {
	case closeErr := <-done:
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	case <-time.After(time.Second):
		t.Fatal("context cancellation left socket open")
	}

	select {
	case <-remoteClosed:
	case <-time.After(time.Second):
		t.Fatal("peer did not observe cancellation")
	}
}
