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
	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"github.com/stretchr/testify/require"
)

type defaultEventReplayDialer struct {
	websocket.Dialer

	target    string
	urls      chan string
	responses chan accountEventHandshake
}

func (dialer *defaultEventReplayDialer) DialContext(
	ctx context.Context,
	wsURL string,
	headers http.Header,
) (*websocket.Conn, *http.Response, error) {
	dialer.urls <- wsURL

	connection, response, err := dialer.Dialer.DialContext(ctx, dialer.target, headers)
	if response != nil {
		dialer.responses <- accountEventHandshake{status: response.StatusCode, headers: response.Header.Clone()}
	}

	if err != nil {
		return connection, response, ringapimodels.NewConnectionError("dial synthetic account-event replay", err)
	}

	return connection, response, nil
}

func TestDefaultEventEndpointUsesPairedSyntheticHandshake(t *testing.T) {
	t.Parallel()

	fixture := loadAccountEventFixture(t)

	requests := make(chan *http.Request, 1)
	server := newAccountEventReplayServer(t, fixture, requests)
	dialer := &defaultEventReplayDialer{
		Dialer:    websocket.Dialer{},
		target:    "ws" + strings.TrimPrefix(server.URL, "http") + fixture.Handshake.Request.EscapedPath,
		urls:      make(chan string, 1),
		responses: make(chan accountEventHandshake, 1),
	}
	client, err := ring.NewClient(ring.WithWebSocketDialer(dialer))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	connection, err := client.ConnectEvents(context.Background(), ring.ConnectEventsRequest{
		Auth: ring.AuthContext{AccessToken: "test_token", HardwareID: ""},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })

	require.Equal(t, protocol.ExperimentalEventWebSocketURL, <-dialer.urls)

	request := <-requests

	require.Equal(t, fixture.Handshake.Request.Method, request.Method)
	require.Equal(t, fixture.Handshake.Request.EscapedPath, request.URL.EscapedPath())
	require.Equal(t, "Bearer test_token", request.Header.Get("Authorization"))

	response := <-dialer.responses

	require.Equal(t, fixture.Handshake.Response.Status, response.status)

	for name, value := range fixture.Handshake.Response.Headers {
		require.True(t, strings.EqualFold(value, response.headers.Get(name)))
	}

	event, err := connection.Receive()
	require.NoError(t, err)
	require.Equal(t, fixture.Frames[0].Payload.Kind, event.Kind)
	require.Equal(t, fixture.Frames[0].Payload.DeviceID, event.DeviceID)
	require.Equal(t, fixture.Frames[0].Payload.Source, event.Data["source"])
	require.NoError(t, connection.Close())

	_, err = connection.Receive()
	require.True(t, ringapimodels.IsClosedError(err))
}

func TestListenReturnsPeerCloseAfterPairedAccountEvent(t *testing.T) {
	t.Parallel()

	fixture := loadAccountEventFixture(t)
	requests := make(chan *http.Request, 1)
	server := newAccountEventReplayServer(t, fixture, requests)
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + fixture.Handshake.Request.EscapedPath
	client, err := ring.NewClient(ring.WithEventWebSocketURL(wsURL))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	events := make([]ringapimodels.Event, 0, len(fixture.Frames))
	err = client.Listen(ctx, func(event *ringapimodels.Event) error {
		events = append(events, *event)

		return nil
	}, ring.ConnectEventsRequest{Auth: ring.AuthContext{AccessToken: "test_token", HardwareID: ""}})
	require.True(t, ringapimodels.IsConnectionError(err))
	require.Len(t, events, len(fixture.Frames))
	require.Equal(t, fixture.Frames[0].Payload.Kind, events[0].Kind)
	require.Equal(t, fixture.Frames[0].Payload.DeviceID, events[0].DeviceID)
	require.Equal(t, fixture.Frames[0].Payload.Source, events[0].Data["source"])

	request := <-requests
	require.Equal(t, fixture.Handshake.Request.Method, request.Method)
	require.Equal(t, fixture.Handshake.Request.EscapedPath, request.URL.EscapedPath())
	require.Equal(t, "Bearer test_token", request.Header.Get("Authorization"))
}

func TestListenPropagatesCallbackFailureFromPairedFrame(t *testing.T) {
	t.Parallel()

	fixture := loadAccountEventFixture(t)
	requests := make(chan *http.Request, 1)
	server := newAccountEventReplayServer(t, fixture, requests)
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + fixture.Handshake.Request.EscapedPath
	client, err := ring.NewClient(ring.WithEventWebSocketURL(wsURL))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = client.Listen(ctx, func(*ringapimodels.Event) error { return context.Canceled }, ring.ConnectEventsRequest{
		Auth: ring.AuthContext{AccessToken: "test_token", HardwareID: ""},
	})
	require.ErrorIs(t, err, context.Canceled)

	request := <-requests
	require.Equal(t, fixture.Handshake.Request.EscapedPath, request.URL.EscapedPath())
	require.Equal(t, "Bearer test_token", request.Header.Get("Authorization"))
}

func TestListenReturnsCancellationAfterPairedCallback(t *testing.T) {
	t.Parallel()

	fixture := loadAccountEventFixture(t)
	requests := make(chan *http.Request, 1)
	server := newAccountEventReplayServer(t, fixture, requests)
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + fixture.Handshake.Request.EscapedPath
	client, err := ring.NewClient(ring.WithEventWebSocketURL(wsURL))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCount := 0
	err = client.Listen(ctx, func(*ringapimodels.Event) error {
		eventCount++

		cancel()

		return nil
	}, ring.ConnectEventsRequest{Auth: ring.AuthContext{AccessToken: "test_token", HardwareID: ""}})
	require.True(t, ringapimodels.IsConnectionError(err))
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, eventCount)

	request := <-requests
	require.Equal(t, fixture.Handshake.Request.EscapedPath, request.URL.EscapedPath())
	require.Equal(t, "Bearer test_token", request.Header.Get("Authorization"))
}

func TestConnectEventsRejectsMissingTokenBeforeHandshake(t *testing.T) {
	t.Parallel()

	client, err := ring.NewClient()
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	_, err = client.ConnectEvents(context.Background(), ring.ConnectEventsRequest{
		Auth: ring.AuthContext{AccessToken: "", HardwareID: ""},
	})
	require.True(t, ringapimodels.IsTokenError(err))
}

func loadAccountEventFixture(t *testing.T) accountEventFixture {
	t.Helper()

	data, err := accountEventFixtureFS.ReadFile("fixtures/events/synthetic/paired/account-event.json")
	require.NoError(t, err)

	var fixture accountEventFixture

	err = json.Unmarshal(data, &fixture)
	require.NoError(t, err)

	return fixture
}

func newAccountEventReplayServer(
	t *testing.T,
	fixture accountEventFixture,
	requests chan<- *http.Request,
) *httptest.Server {
	t.Helper()

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests <- request.Clone(request.Context())

		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}

		defer func() { _ = connection.Close() }()

		_ = connection.WriteJSON(fixture.Frames[0].Payload)
		_ = connection.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		)
	}))
	t.Cleanup(server.Close)

	return server
}
