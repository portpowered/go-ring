package replay_test

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"github.com/stretchr/testify/require"
)

func TestReferencePushHTTPSetup(t *testing.T) {
	tests := []struct {
		fixture string
		call    func(context.Context, *ring.Client) error
	}{
		{"push-register", func(ctx context.Context, c *ring.Client) error {
			return c.RegisterPushDevice(ctx, ring.RegisterPushDeviceRequest{Auth: ring.AuthContext{AccessToken: "captured-token"}, Token: "fcm-test-token"})
		}},
		{"push-ding-subscribe", func(ctx context.Context, c *ring.Client) error {
			return c.SubscribeDeviceDing(ctx, ring.DeviceIDRequest{Auth: ring.AuthContext{AccessToken: "captured-token"}, DeviceID: "1000"})
		}},
		{"push-motion-subscribe", func(ctx context.Context, c *ring.Client) error {
			return c.SubscribeDeviceMotion(ctx, ring.DeviceIDRequest{Auth: ring.AuthContext{AccessToken: "captured-token"}, DeviceID: "1000"})
		}},
	}
	for _, test := range tests {
		t.Run(test.fixture, func(t *testing.T) {
			exchange, err := replay.LoadExchange(filepath.Join("fixtures", "http", "reference", test.fixture+".json"))
			require.NoError(t, err)
			client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: replay.NewTransport(exchange)}))
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })
			require.NoError(t, test.call(context.Background(), client))
		})
	}
}

func TestPushSetupValidatesInput(t *testing.T) {
	client, err := ring.NewClient()
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	err = client.RegisterPushDevice(context.Background(), ring.RegisterPushDeviceRequest{})
	require.True(t, ringapimodels.IsBadRequestError(err))
	err = client.SubscribeDeviceMotion(context.Background(), ring.DeviceIDRequest{DeviceID: "bad"})
	require.True(t, ringapimodels.IsBadRequestError(err))
}
