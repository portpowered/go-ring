package unit

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"github.com/stretchr/testify/require"
)

func TestListenReturnsContextErrorWhenCancelledWhileWaiting(t *testing.T) {
	upgraded := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		close(upgraded)
		defer conn.Close()
		_, _, _ = conn.ReadMessage()
	}))
	t.Cleanup(server.Close)
	client, err := ring.NewClient(ring.WithAccessToken("test_token"), ring.WithEventWebSocketURL("ws"+strings.TrimPrefix(server.URL, "http")))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- client.Listen(ctx, func(*ringapimodels.Event) error {
			t.Error("callback invoked without an event")
			return nil
		})
	}()
	select {
	case <-upgraded:
	case <-time.After(time.Second):
		t.Fatal("Listen did not establish its event connection")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("Listen did not return after context cancellation")
	}
}

func TestEventStreamSkipsMalformedJSONAndReportsPeerReadFailure(t *testing.T) {
	upgraded := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		close(upgraded)
		defer conn.Close()
		_ = conn.WriteMessage(websocket.TextMessage, []byte("{"))
		_ = conn.WriteJSON(map[string]any{"kind": "motion", "device_id": 321, "timestamp": "t"})
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"))
	}))
	t.Cleanup(server.Close)
	client, err := ring.NewClient(ring.WithAccessToken("test_token"), ring.WithEventWebSocketURL("ws"+strings.TrimPrefix(server.URL, "http")))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := client.ConnectEvents(ctx)
	require.NoError(t, err)
	defer conn.Close()
	select {
	case <-upgraded:
	case <-ctx.Done():
		t.Fatal("event stream did not connect")
	}
	event, err := conn.Receive()
	require.NoError(t, err)
	require.Equal(t, ringapimodels.EventKind("motion"), event.Kind)
	_, err = conn.Receive()
	require.True(t, ringapimodels.IsConnectionError(err), "peer read failure should be surfaced")
}

func TestDeviceHistoryEscapesKindQueryValue(t *testing.T) {
	client, mockTransport := newTestClientWithToken("test-token")
	defer client.Close()
	mockTransport.SetResponseWithBody("GET", "/clients_api/doorbots/987652/history", 200, []any{})

	_, err := client.GetDeviceHistory(context.Background(), ring.GetDeviceHistoryRequest{
		DeviceID: "987652",
		Limit:    10,
		Kind:     "motion&limit=0",
	})
	require.NoError(t, err)
	requests := mockTransport.GetRequests()
	require.Len(t, requests, 1)
	parsed, err := url.Parse(requests[0].URL)
	require.NoError(t, err)
	require.Equal(t, "10", parsed.Query().Get("limit"))
	require.Equal(t, "motion&limit=0", parsed.Query().Get("kind"))
	require.Len(t, parsed.Query(), 2)
}

func TestRecordingHistoryPropagatesHTTPAndDecodeErrors(t *testing.T) {
	t.Run("HTTP status", func(t *testing.T) {
		client, mockTransport := newTestClientWithToken("test-token")
		defer client.Close()
		mockTransport.SetResponseWithBody("GET", "/clients_api/doorbots/987652/history", 404, map[string]string{"error": "missing"})
		_, err := client.GetDeviceHistory(context.Background(), ring.GetDeviceHistoryRequest{DeviceID: "987652"})
		require.True(t, ringapimodels.IsHTTPError(err))
		require.True(t, ringapimodels.IsHTTPStatusCode(err, 404))
	})

	t.Run("malformed response", func(t *testing.T) {
		client, mockTransport := newTestClientWithToken("test-token")
		defer client.Close()
		mockTransport.SetResponse("GET", "/clients_api/doorbots/987652/history", &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"recordings":[]}`)),
		})
		_, err := client.GetDeviceHistory(context.Background(), ring.GetDeviceHistoryRequest{DeviceID: "987652"})
		require.True(t, ringapimodels.IsBadRequestError(err))
	})
}

func TestGetDeviceHistoryMapsDoorbotIdentityAndRecordingFields(t *testing.T) {
	client, mockTransport := newTestClientWithToken("test-token")
	defer client.Close()
	mockTransport.SetResponseWithBody("GET", "/clients_api/doorbots/987652/history", 200, []any{
		map[string]any{
			"id": 42, "kind": "motion", "answered": true, "created_at": "2026-01-01T00:00:00Z",
			"doorbot": map[string]any{"id": 987652, "description": "Camera", "type": "stickup_cam"},
		},
	})

	history, err := client.GetDeviceHistory(context.Background(), ring.GetDeviceHistoryRequest{DeviceID: "987652", Limit: 1})
	require.NoError(t, err)
	require.Len(t, history.Recordings, 1)
	require.Equal(t, int64(42), history.Recordings[0].ID)
	require.Equal(t, int64(987652), history.Recordings[0].DeviceID)
	require.Equal(t, "motion", history.Recordings[0].Kind)
	require.True(t, history.Recordings[0].Answered)
	require.Equal(t, "2026-01-01T00:00:00Z", history.Recordings[0].CreatedAt)
}
