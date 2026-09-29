package replay_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"github.com/stretchr/testify/require"
)

type replayFCMDialCall struct {
	network string
	address string
}

func TestClientFCMTransportOptionsUsePairedReplay(t *testing.T) {
	t.Parallel()

	fcmTransport := syntheticFCMStartupTransport(t)
	ringExchange, err := replay.LoadExchange(filepath.Join("fixtures", "http", "synthetic", "ring-push-register.json"))
	require.NoError(t, err)

	ringTransport := replay.NewTransport(ringExchange)
	dialCalls := make(chan replayFCMDialCall, 1)

	client, err := ring.NewClient(
		ring.WithHTTPClient(&http.Client{Transport: ringTransport}),
		ring.WithFCMHTTPTransport(fcmReplayNormalizer{next: fcmTransport}),
		ring.WithFCMDialContext(func(_ context.Context, network, address string) (net.Conn, error) {
			dialCalls <- replayFCMDialCall{network: network, address: address}

			return nil, net.ErrClosed
		}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	connection, err := client.ConnectPush(ctx, ring.ConnectPushRequest{
		Auth:        ring.AuthContext{AccessToken: "synthetic-access-token", HardwareID: ""},
		Credentials: nil,
		DeviceIDs:   nil,
		Ding:        false,
		Motion:      false,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })

	waitForPushRegistration(t, ctx, connection.Events())

	select {
	case call := <-dialCalls:
		require.Equal(t, protocol.MCSNetwork, call.network)
		require.Equal(t, net.JoinHostPort(protocol.MCSHost, protocol.MCSPort), call.address)
	case <-ctx.Done():
		t.Fatal("built-in FCM receiver did not call the injected dial function")
	}

	require.NoError(t, connection.Close())
	require.NoError(t, ringTransport.AssertConsumed())
	require.NoError(t, fcmTransport.AssertConsumed())
}

func TestClientFCMTransportOptionsRejectNil(t *testing.T) {
	t.Parallel()

	_, httpErr := ring.NewClient(ring.WithFCMHTTPTransport(nil))
	require.True(t, ringapimodels.IsBadRequestError(httpErr))

	_, dialErr := ring.NewClient(ring.WithFCMDialContext(nil))
	require.True(t, ringapimodels.IsBadRequestError(dialErr))
}

func TestConnectPushReportsPairedRingRegistrationFailure(t *testing.T) {
	t.Parallel()

	exchange, err := replay.LoadExchange(filepath.Join(
		"fixtures", "http", "synthetic", "ring-push-register-failure.json",
	))
	require.NoError(t, err)

	transport := replay.NewTransport(exchange)
	source := func(context.Context, json.RawMessage) (<-chan ring.FCMEvent, error) {
		events := make(chan ring.FCMEvent, 1)
		events <- ring.FCMEvent{
			Kind:        ring.PushCredentials,
			Token:       "synthetic-fcm-token",
			DeviceID:    "",
			Action:      "",
			Credentials: json.RawMessage(`{"token":"synthetic-fcm-token"}`),
			Data:        nil,
			Err:         nil,
		}

		close(events)

		return events, nil
	}
	client, err := ring.NewClient(
		ring.WithHTTPClient(&http.Client{Transport: transport}),
		ring.WithFCMSource(source),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	connection, err := client.ConnectPush(context.Background(), ring.ConnectPushRequest{
		Auth:        ring.AuthContext{AccessToken: "synthetic-access-token", HardwareID: ""},
		Credentials: nil,
		DeviceIDs:   nil,
		Ding:        false,
		Motion:      false,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })

	events := make([]ring.FCMEvent, 0, 2)
	for event := range connection.Events() {
		events = append(events, event)
	}

	require.Len(t, events, 2)
	require.Equal(t, ring.PushCredentials, events[0].Kind)
	require.Equal(t, ring.PushRetry, events[1].Kind)
	require.Error(t, events[1].Err)
	require.NoError(t, transport.AssertConsumed())
	require.NoError(t, connection.Close())
}

func syntheticFCMStartupTransport(t *testing.T) *replay.Transport {
	t.Helper()

	fixtureNames := []string{"checkin", "legacy-registration", "installation", "registration"}

	exchanges := make([]replay.Exchange, 0, len(fixtureNames))
	for _, name := range fixtureNames {
		exchanges = append(exchanges, loadFCMExchange(t, name))
	}

	return replay.NewTransport(exchanges...)
}

func waitForPushRegistration(t *testing.T, ctx context.Context, events <-chan ring.FCMEvent) {
	t.Helper()

	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("push connection ended before Ring registration completed")
			}

			if event.Kind == ring.PushRegistered {
				return
			}
		case <-ctx.Done():
			t.Fatal("push registration replay timed out")
		}
	}
}
