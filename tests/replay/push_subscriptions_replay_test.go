package replay_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/stretchr/testify/require"
)

func TestConnectPushReplaysDingAndMotionSubscriptions(t *testing.T) {
	t.Parallel()

	exchanges := loadSyntheticPushExchanges(
		t,
		"ring-push-register",
		"ring-push-ding-subscribe",
		"ring-push-motion-subscribe",
	)
	transport := replay.NewTransport(exchanges...)
	client, err := ring.NewClient(
		ring.WithHTTPClient(&http.Client{Transport: transport}),
		ring.WithFCMSource(syntheticPushCredentialsSource()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	connection, err := client.ConnectPush(context.Background(), ring.ConnectPushRequest{
		Auth:        ring.AuthContext{AccessToken: "synthetic-access-token", HardwareID: ""},
		Credentials: nil,
		DeviceIDs:   []string{"1000"},
		Ding:        true,
		Motion:      true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })

	kinds := make([]ring.FCMEventKind, 0, 2)
	for event := range connection.Events() {
		kinds = append(kinds, event.Kind)
		require.NoError(t, event.Err)
	}

	require.Equal(t, []ring.FCMEventKind{ring.PushCredentials, ring.PushRegistered}, kinds)
	require.NoError(t, transport.AssertConsumed())
	require.NoError(t, connection.Close())
}

func TestConnectPushRegistersHardwareBeforeDevice(t *testing.T) {
	t.Parallel()

	exchanges := loadSyntheticPushExchanges(t, "ring-session-register", "ring-push-register")
	transport := replay.NewTransport(exchanges...)
	client, err := ring.NewClient(
		ring.WithHTTPClient(&http.Client{Transport: transport}),
		ring.WithFCMSource(syntheticPushCredentialsSource()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	connection, err := client.ConnectPush(context.Background(), ring.ConnectPushRequest{
		Auth:        ring.AuthContext{AccessToken: "synthetic-access-token", HardwareID: "synthetic-hardware-id"},
		Credentials: nil,
		DeviceIDs:   nil,
		Ding:        false,
		Motion:      false,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })

	kinds := make([]ring.FCMEventKind, 0, 2)
	for event := range connection.Events() {
		kinds = append(kinds, event.Kind)
		require.NoError(t, event.Err)
	}

	require.Equal(t, []ring.FCMEventKind{ring.PushCredentials, ring.PushRegistered}, kinds)
	require.NoError(t, transport.AssertConsumed())
	require.NoError(t, connection.Close())
}

func TestConnectPushReportsPairedSessionRegistrationFailure(t *testing.T) {
	t.Parallel()

	exchanges := loadSyntheticPushExchanges(t, "ring-session-register-failure")
	transport := replay.NewTransport(exchanges...)
	client, err := ring.NewClient(
		ring.WithHTTPClient(&http.Client{Transport: transport}),
		ring.WithFCMSource(syntheticPushCredentialsSource()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	connection, err := client.ConnectPush(context.Background(), ring.ConnectPushRequest{
		Auth:        ring.AuthContext{AccessToken: "synthetic-access-token", HardwareID: "synthetic-hardware-id"},
		Credentials: nil,
		DeviceIDs:   nil,
		Ding:        false,
		Motion:      false,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })

	kinds := make([]ring.FCMEventKind, 0, 2)
	for event := range connection.Events() {
		kinds = append(kinds, event.Kind)

		if event.Kind == ring.PushRetry {
			require.Error(t, event.Err)
		}
	}

	require.Equal(t, []ring.FCMEventKind{ring.PushCredentials, ring.PushRetry}, kinds)
	require.NoError(t, transport.AssertConsumed())
	require.NoError(t, connection.Close())
}

func syntheticPushCredentialsSource() ring.FCMSource {
	return func(context.Context, json.RawMessage) (<-chan ring.FCMEvent, error) {
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
}

func loadSyntheticPushExchanges(t *testing.T, fixtureNames ...string) []replay.Exchange {
	t.Helper()

	exchanges := make([]replay.Exchange, 0, len(fixtureNames))

	for _, name := range fixtureNames {
		exchange, err := replay.LoadExchange(filepath.Join("fixtures", "http", "synthetic", name+".json"))
		require.NoError(t, err)

		exchanges = append(exchanges, exchange)
	}

	return exchanges
}
