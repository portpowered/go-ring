package unit

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
	"github.com/stretchr/testify/require"
)

func eventClient(t *testing.T, send bool) *ring.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test_token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if send {
			_ = conn.WriteJSON(map[string]any{"kind": "motion", "device_id": 987652, "timestamp": "2026-01-01T00:00:00Z"})
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	client, err := ring.NewClient(ring.WithAccessToken("test_token"),
		ring.WithEventWebSocketURL("ws"+strings.TrimPrefix(server.URL, "http")))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestEventsReceiveAndClose(t *testing.T) {
	client := eventClient(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := client.ConnectEvents(ctx)
	require.NoError(t, err)
	defer conn.Close()
	event, err := conn.Receive()
	require.NoError(t, err)
	require.Equal(t, ringapimodels.EventKind("motion"), event.Kind)
	require.Equal(t, int64(987652), event.DeviceID)
	require.Equal(t, "2026-01-01T00:00:00Z", event.Timestamp)
	done := make(chan error, 1)
	go func() { done <- conn.Close() }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Close blocked on an idle peer")
	}
	require.NoError(t, conn.Close())
	_, err = conn.Receive()
	require.True(t, ringapimodels.IsClosedError(err))
}

func TestEventsCancellationInterruptsIdleConnection(t *testing.T) {
	client := eventClient(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := client.ConnectEvents(ctx)
	require.NoError(t, err)
	defer conn.Close()
	cancel()
	done := make(chan error, 1)
	go func() { _, err := conn.Receive(); done <- err }()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("Receive did not observe cancellation")
	}
}

func TestListenReturnsCallbackError(t *testing.T) {
	client := eventClient(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	expected := context.DeadlineExceeded
	err := client.Listen(ctx, func(event *ringapimodels.Event) error {
		require.Equal(t, int64(987652), event.DeviceID)
		return expected
	})
	require.ErrorIs(t, err, expected)
}

func TestEventsRejectInvalidEndpoint(t *testing.T) {
	client, err := ring.NewClient(ring.WithAccessToken("test_token"), ring.WithEventWebSocketURL(":invalid"))
	require.NoError(t, err)
	defer client.Close()
	_, err = client.ConnectEvents(context.Background())
	require.True(t, ringapimodels.IsConnectionError(err))
}
