package replay_test

import (
	"context"
	"embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"github.com/stretchr/testify/require"
)

//go:embed fixtures/events/synthetic/paired/account-event.json
var accountEventFixtureFS embed.FS

type accountEventFixture struct {
	Provenance string                    `json:"provenance"`
	Handshake  accountEventPairHandshake `json:"handshake"`
	Frames     []accountEventPairFrame   `json:"frames"`
}

type accountEventPairHandshake struct {
	Request  accountEventPairRequest  `json:"request"`
	Response accountEventPairResponse `json:"response"`
}

type accountEventPairRequest struct {
	Method      string            `json:"method"`
	Origin      string            `json:"origin"`
	EscapedPath string            `json:"escaped_path"`
	Headers     map[string]string `json:"headers"`
}

type accountEventPairResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
}

type accountEventPairFrame struct {
	Direction string                  `json:"direction"`
	Payload   accountEventPairPayload `json:"payload"`
}

type accountEventPairPayload struct {
	Kind      string `json:"kind"`
	DeviceID  int64  `json:"device_id"`
	Timestamp string `json:"timestamp"`
	Source    string `json:"source"`
}

type accountEventHandshake struct {
	status  int
	headers http.Header
}

type accountEventDialer struct {
	websocket.Dialer

	responses chan accountEventHandshake
}

func (d *accountEventDialer) DialContext(
	ctx context.Context, wsURL string, headers http.Header,
) (*websocket.Conn, *http.Response, error) {
	conn, response, err := d.Dialer.DialContext(ctx, wsURL, headers)
	if response != nil {
		d.responses <- accountEventHandshake{status: response.StatusCode, headers: response.Header.Clone()}
	}

	if err != nil {
		return conn, response, ringapimodels.NewConnectionError("dial event replay", err)
	}

	return conn, response, nil
}

// TestAccountEventPairedReplay consumes both sides of the synthetic handshake
// and the server event frame through the public client.
func TestAccountEventPairedReplay(t *testing.T) {
	t.Parallel()

	data, err := accountEventFixtureFS.ReadFile("fixtures/events/synthetic/paired/account-event.json")
	require.NoError(t, err)

	var fixture accountEventFixture

	require.NoError(t, json.Unmarshal(data, &fixture))
	require.Equal(t, "synthetic", fixture.Provenance)
	require.Len(t, fixture.Frames, 1)
	require.Equal(t, "server_to_client", fixture.Frames[0].Direction)
	require.Equal(t, http.StatusSwitchingProtocols, fixture.Handshake.Response.Status)

	requests := make(chan *http.Request, 1)

	var handshakeCount atomic.Int32

	var expectedHost string

	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if handshakeCount.Add(1) != 1 {
			http.Error(responseWriter, "duplicate event handshake", http.StatusBadRequest)

			return
		}

		requests <- request.Clone(request.Context())

		if request.Method != fixture.Handshake.Request.Method ||
			request.URL.EscapedPath() != fixture.Handshake.Request.EscapedPath ||
			request.Host != expectedHost ||
			request.Header.Get("Authorization") != strings.ReplaceAll(
				fixture.Handshake.Request.Headers["Authorization"], "$accessToken", "test_token",
			) {
			http.Error(responseWriter, "event handshake does not match paired fixture", http.StatusBadRequest)

			return
		}

		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

		conn, upgradeErr := upgrader.Upgrade(responseWriter, request, nil)
		if upgradeErr != nil {
			return
		}

		defer func() { _ = conn.Close() }()

		_ = conn.WriteJSON(fixture.Frames[0].Payload)

		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)
	expectedHost = strings.TrimPrefix(server.URL, "http://")

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + fixture.Handshake.Request.EscapedPath
	dialer := &accountEventDialer{
		Dialer:    websocket.Dialer{},
		responses: make(chan accountEventHandshake, 1),
	}
	client, err := ring.NewClient(ring.WithEventWebSocketURL(wsURL), ring.WithWebSocketDialer(dialer))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, err := client.ConnectEvents(ctx, ring.ConnectEventsRequest{
		Auth: ring.AuthContext{AccessToken: "test_token", HardwareID: ""},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	request := <-requests
	require.Equal(t, fixture.Handshake.Request.Method, request.Method)
	require.Equal(t, fixture.Handshake.Request.EscapedPath, request.URL.EscapedPath())
	require.Equal(t, "$configuredEventOrigin", fixture.Handshake.Request.Origin)
	require.Equal(t, strings.TrimPrefix(server.URL, "http://"), request.Host)

	for key, value := range fixture.Handshake.Request.Headers {
		require.Equal(t, strings.ReplaceAll(value, "$accessToken", "test_token"), request.Header.Get(key))
	}

	response := <-dialer.responses
	require.Equal(t, fixture.Handshake.Response.Status, response.status)

	for key, value := range fixture.Handshake.Response.Headers {
		require.True(t, strings.EqualFold(value, response.headers.Get(key)))
	}

	event, err := conn.Receive()
	require.NoError(t, err)
	require.Equal(t, fixture.Frames[0].Payload.Kind, event.Kind)
	require.Equal(t, fixture.Frames[0].Payload.DeviceID, event.DeviceID)
	require.Equal(t, fixture.Frames[0].Payload.Timestamp, event.Timestamp)
	require.Equal(t, fixture.Frames[0].Payload.Source, event.Data["source"])
	require.Equal(t, int32(1), handshakeCount.Load())
}
