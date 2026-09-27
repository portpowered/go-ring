package replay_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"github.com/stretchr/testify/require"
)

func TestFCMConnectionReplaysRegistrationAndMotion(t *testing.T) {
	fixtures := []string{"push-register", "push-motion-subscribe"}
	exchanges := make([]replay.Exchange, 0, len(fixtures))
	for _, name := range fixtures {
		exchange, err := replay.LoadExchange(filepath.Join("fixtures", "http", "reference", name+".json"))
		require.NoError(t, err)
		exchanges = append(exchanges, exchange)
	}
	transport := replay.NewTransport(exchanges...)
	data, err := os.ReadFile(filepath.Join("fixtures", "push", "motion.json"))
	require.NoError(t, err)
	source := func(ctx context.Context, _ json.RawMessage) (<-chan ring.FCMEvent, error) {
		stream := make(chan ring.FCMEvent, 3)
		stream <- ring.FCMEvent{Kind: ring.PushCredentials, Token: "fcm-test-token", Credentials: json.RawMessage(`{"token":"fcm-test-token"}`)}
		stream <- ring.FCMEvent{Kind: ring.PushConnected}
		stream <- ring.FCMEvent{Kind: ring.PushMessage, Data: data}
		close(stream)
		return stream, nil
	}
	client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}), ring.WithFCMSource(source))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	connection, err := client.ConnectPush(ctx, ring.ConnectPushRequest{
		Auth: ring.AuthContext{AccessToken: "captured-token"}, DeviceIDs: []string{"1000"}, Motion: true,
	})
	require.NoError(t, err)
	kinds := make([]ring.FCMEventKind, 0, 4)
	for event := range connection.Events() {
		kinds = append(kinds, event.Kind)
		require.NoError(t, event.Err)
		if event.Kind == ring.PushMessage {
			require.Equal(t, "709739068", event.DeviceID)
			require.Equal(t, ring.PushActionMotion, event.Action)
		}
	}
	require.Equal(t, []ring.FCMEventKind{ring.PushCredentials, ring.PushRegistered, ring.PushConnected, ring.PushMessage}, kinds)
	require.NoError(t, connection.Close())
	require.NoError(t, transport.AssertConsumed())
}

func TestFCMConnectionCloseDuringSilentSource(t *testing.T) {
	source := func(context.Context, json.RawMessage) (<-chan ring.FCMEvent, error) {
		return make(chan ring.FCMEvent), nil
	}
	client, err := ring.NewClient(ring.WithFCMSource(source))
	require.NoError(t, err)
	connection, err := client.ConnectPush(context.Background(), ring.ConnectPushRequest{Auth: ring.AuthContext{AccessToken: "captured-token"}})
	require.NoError(t, err)
	require.NoError(t, connection.Close())
	require.NoError(t, client.Close())
}

func TestFCMConnectionRejectsInvalidSetup(t *testing.T) {
	_, err := ring.NewClient(ring.WithFCMSource(nil))
	require.True(t, ringapimodels.IsBadRequestError(err))
	client, err := ring.NewClient()
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	_, err = client.ConnectPush(context.Background(), ring.ConnectPushRequest{})
	require.True(t, ringapimodels.IsTokenError(err))
	_, err = client.ConnectPush(context.Background(), ring.ConnectPushRequest{Auth: ring.AuthContext{AccessToken: "captured-token"}, DeviceIDs: []string{"bad"}})
	require.True(t, ringapimodels.IsBadRequestError(err))
	_, err = client.ConnectPush(context.Background(), ring.ConnectPushRequest{Auth: ring.AuthContext{AccessToken: "captured-token"}, Credentials: json.RawMessage(`bad`)})
	require.True(t, ringapimodels.IsBadRequestError(err))
}
