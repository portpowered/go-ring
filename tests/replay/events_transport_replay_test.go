package replay_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/internal/ringerrors"
	dependencywebsocket "github.com/portpowered/go-ring/pkg/dependencies/websocket"
	"github.com/stretchr/testify/require"
)

const syntheticAccountEventAuthorization = "Bearer test_token"

func TestAccountEventTransportReplaysFrameBeforePeerClose(t *testing.T) {
	t.Parallel()

	data, err := accountEventFixtureFS.ReadFile("fixtures/events/synthetic/paired/account-event.json")
	require.NoError(t, err)

	var fixture accountEventFixture

	require.NoError(t, json.Unmarshal(data, &fixture))
	require.Equal(t, http.StatusSwitchingProtocols, fixture.Handshake.Response.Status)
	require.Len(t, fixture.Frames, 1)

	requestSeen := make(chan *http.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestSeen <- request.Clone(request.Context())

		if request.Method != fixture.Handshake.Request.Method ||
			request.URL.EscapedPath() != fixture.Handshake.Request.EscapedPath ||
			request.Header.Get("Authorization") != syntheticAccountEventAuthorization {
			http.Error(writer, "event handshake does not match synthetic pair", http.StatusBadRequest)

			return
		}

		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

		connection, upgradeErr := upgrader.Upgrade(writer, request, nil)
		if upgradeErr != nil {
			return
		}

		defer func() { _ = connection.Close() }()

		writeErr := connection.WriteJSON(fixture.Frames[0].Payload)
		if writeErr != nil {
			return
		}
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)

	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + fixture.Handshake.Request.EscapedPath
	connection, err := dependencywebsocket.OpenEvents(ctx, wsURL, "test_token", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })

	handshake := <-requestSeen
	require.Equal(t, fixture.Handshake.Request.Method, handshake.Method)
	require.Equal(t, fixture.Handshake.Request.EscapedPath, handshake.URL.EscapedPath())
	require.Equal(t, "Bearer test_token", handshake.Header.Get("Authorization"))

	event, err := connection.Receive()
	require.NoError(t, err)
	require.Equal(t, fixture.Frames[0].Payload.Kind, event["kind"])
	require.InDelta(t, float64(fixture.Frames[0].Payload.DeviceID), event["device_id"], 0)
	require.Equal(t, fixture.Frames[0].Payload.Source, event["source"])

	_, err = connection.Receive()
	require.Error(t, err)
	require.True(t, ringerrors.IsConnectionError(err), "peer close follows the queued synthetic frame")
	require.NoError(t, connection.Close())
}
