package ring_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

const (
	eventDialerTestURL        = "wss://events.example.invalid/stream"
	eventDialerTestToken      = "event-test-token"
	eventDialerTestHardwareID = "event-test-hardware"
)

type eventDialerTestError struct{}

func (eventDialerTestError) Error() string { return "event dial stopped by test" }

type eventDialerCapture struct {
	url     string
	headers http.Header
}

func (dialer *eventDialerCapture) DialContext(
	_ context.Context,
	url string,
	headers http.Header,
) (*websocket.Conn, *http.Response, error) {
	dialer.url = url
	dialer.headers = headers.Clone()

	return nil, nil, eventDialerTestError{}
}

func TestWebSocketDialerOptionCoversEventConnection(t *testing.T) {
	t.Parallel()

	dialer := &eventDialerCapture{url: "", headers: nil}

	client, err := ring.NewClient(
		ring.WithEventWebSocketURL(eventDialerTestURL),
		ring.WithWebSocketDialer(dialer),
	)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = client.Close() })

	_, err = client.ConnectEvents(context.Background(), ring.ConnectEventsRequest{
		Auth: ring.AuthContext{AccessToken: eventDialerTestToken, HardwareID: eventDialerTestHardwareID},
	})
	if err == nil {
		t.Fatal("event connection unexpectedly succeeded")
	}

	if dialer.url != eventDialerTestURL {
		t.Fatalf("event dial URL = %q, want %q", dialer.url, eventDialerTestURL)
	}

	if got := dialer.headers.Get("Authorization"); got != "Bearer "+eventDialerTestToken {
		t.Fatalf("authorization header = %q", got)
	}

	if got := dialer.headers.Get("Hardware_id"); got != eventDialerTestHardwareID {
		t.Fatalf("hardware header = %q", got)
	}
}

func TestWithEventWebSocketURLRequiresWebSocketOrigin(t *testing.T) {
	t.Parallel()

	for _, rawURL := range []string{
		"https://events.example.invalid/stream",
		"wss:///stream",
		"",
	} {
		t.Run(rawURL, func(t *testing.T) {
			t.Parallel()

			_, err := ring.NewClient(ring.WithEventWebSocketURL(rawURL))
			if err == nil {
				t.Fatalf("event URL %q was accepted", rawURL)
			}
		})
	}

	client, err := ring.NewClient(ring.WithEventWebSocketURL(eventDialerTestURL))
	if err != nil {
		t.Fatalf("valid event URL was rejected: %v", err)
	}

	_ = client.Close()
}

type blockingEventDialer struct {
	started chan struct{}
	release chan struct{}
}

func (dialer *blockingEventDialer) DialContext(
	ctx context.Context,
	url string,
	headers http.Header,
) (*websocket.Conn, *http.Response, error) {
	close(dialer.started)

	select {
	case <-dialer.release:
	case <-ctx.Done():
		return nil, nil, ringapimodels.NewConnectionError("event test dial canceled", ctx.Err())
	}

	conn, response, err := websocket.DefaultDialer.DialContext(ctx, url, headers)
	if err != nil {
		return conn, response, ringapimodels.NewConnectionError("event test websocket dial failed", err)
	}

	return conn, response, nil
}

func eventWebSocketServer(t *testing.T) (string, <-chan struct{}) {
	t.Helper()

	peerClosed := make(chan struct{})
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		conn, err := upgrader.Upgrade(responseWriter, request, nil)
		if err != nil {
			close(peerClosed)

			return
		}

		defer func() {
			_ = conn.Close()

			close(peerClosed)
		}()

		_, _, _ = conn.ReadMessage()
	}))
	t.Cleanup(server.Close)

	return "ws" + strings.TrimPrefix(server.URL, "http") + "/events", peerClosed
}

func TestClientCloseDoesNotOwnEventConnection(t *testing.T) {
	t.Parallel()

	connected := make(chan struct{})
	sendEvent := make(chan struct{})
	peerClosed := make(chan struct{})
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		conn, err := upgrader.Upgrade(responseWriter, request, nil)
		if err != nil {
			close(peerClosed)

			return
		}

		defer func() {
			_ = conn.Close()

			close(peerClosed)
		}()

		close(connected)
		<-sendEvent

		_ = conn.WriteJSON(map[string]any{
			"kind": "motion", "device_id": 42, "timestamp": "2026-09-29T00:00:00Z",
		})
		_, _, _ = conn.ReadMessage()
	}))
	t.Cleanup(server.Close)
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/events"

	client, err := ring.NewClient(ring.WithEventWebSocketURL(url))
	if err != nil {
		t.Fatal(err)
	}

	eventConnection, err := client.ConnectEvents(context.Background(), ring.ConnectEventsRequest{
		Auth: ring.AuthContext{AccessToken: eventDialerTestToken, HardwareID: eventDialerTestHardwareID},
	})
	if err != nil {
		_ = client.Close()

		t.Fatal(err)
	}

	select {
	case <-connected:
	case <-time.After(time.Second):
		_ = eventConnection.Close()

		t.Fatal("event socket did not connect")
	}

	err = client.Close()
	if err != nil {
		t.Fatal(err)
	}

	close(sendEvent)

	event, err := eventConnection.Receive()
	if err != nil {
		t.Fatalf("event receive after client close = %v", err)
	}

	if event.DeviceID != 42 {
		t.Fatalf("event device ID = %d, want 42", event.DeviceID)
	}

	err = eventConnection.Close()
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-peerClosed:
	case <-time.After(time.Second):
		t.Fatal("event connection close left the socket open")
	}
}

func TestConnectEventsAvailableAfterClientClose(t *testing.T) {
	t.Parallel()

	dialer := &eventDialerCapture{url: "", headers: nil}

	client, err := ring.NewClient(
		ring.WithEventWebSocketURL(eventDialerTestURL),
		ring.WithWebSocketDialer(dialer),
	)
	if err != nil {
		t.Fatal(err)
	}

	_ = client.Close()

	_, err = client.ConnectEvents(context.Background(), ring.ConnectEventsRequest{
		Auth: ring.AuthContext{AccessToken: eventDialerTestToken, HardwareID: ""},
	})
	if !ringapimodels.IsConnectionError(err) {
		t.Fatalf("event connect after client close = %v, want dial failure", err)
	}

	if dialer.url != eventDialerTestURL {
		t.Fatalf("event dial URL after client close = %q, want %q", dialer.url, eventDialerTestURL)
	}
}

func TestConnectEventsDialIsCanceledByRequestContext(t *testing.T) {
	t.Parallel()

	url, _ := eventWebSocketServer(t)
	dialer := &blockingEventDialer{started: make(chan struct{}), release: make(chan struct{})}

	client, err := ring.NewClient(
		ring.WithEventWebSocketURL(url),
		ring.WithWebSocketDialer(dialer),
	)
	if err != nil {
		t.Fatal(err)
	}

	connectResult := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())

	defer cancel()

	go func() {
		connection, connectErr := client.ConnectEvents(ctx, ring.ConnectEventsRequest{
			Auth: ring.AuthContext{AccessToken: eventDialerTestToken, HardwareID: ""},
		})
		if connection != nil {
			_ = connection.Close()
		}

		connectResult <- connectErr
	}()

	select {
	case <-dialer.started:
	case <-time.After(time.Second):
		_ = client.Close()

		t.Fatal("event dial did not start")
	}

	cancel()

	select {
	case err := <-connectResult:
		if !ringapimodels.IsConnectionError(err) {
			t.Fatalf("event connect after context cancellation = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("event connect remained blocked after context cancellation")
	}
}
