package ring_test

import (
	"context"
	"testing"

	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"github.com/stretchr/testify/require"
)

// Wire-shape success cases use the shared Python replay fixtures in tests/replay.
// These focused cases ensure invalid calls never reach the HTTP transport.
func TestLegacyControlValidationBeforeHTTP(t *testing.T) {
	checks := []struct {
		name string
		call func(*ring.Client) error
	}{
		{"volume below range", func(c *ring.Client) error {
			return c.SetVolume(context.Background(), ring.SetVolumeRequest{DeviceID: "123", Kind: "chime", Description: "Bell", Volume: -1})
		}},
		{"volume above range", func(c *ring.Client) error {
			return c.SetVolume(context.Background(), ring.SetVolumeRequest{DeviceID: "123", Kind: "chime", Description: "Bell", Volume: 12})
		}},
		{"volume missing kind", func(c *ring.Client) error {
			return c.SetVolume(context.Background(), ring.SetVolumeRequest{DeviceID: "123", Description: "Bell", Volume: 2})
		}},
		{"volume missing description", func(c *ring.Client) error {
			return c.SetVolume(context.Background(), ring.SetVolumeRequest{DeviceID: "123", Kind: "doorbell", Volume: 2})
		}},
		{"volume invalid device", func(c *ring.Client) error {
			return c.SetVolume(context.Background(), ring.SetVolumeRequest{DeviceID: "0", Kind: "chime", Description: "Bell", Volume: 2})
		}},
		{"light invalid state", func(c *ring.Client) error {
			return c.SetLights(context.Background(), ring.SetLightsRequest{DeviceID: "123", State: "blink"})
		}},
		{"light unsupported duration", func(c *ring.Client) error {
			d := 30
			return c.SetLights(context.Background(), ring.SetLightsRequest{DeviceID: "123", State: "on", Duration: &d})
		}},
		{"sound invalid kind", func(c *ring.Client) error {
			return c.TestSound(context.Background(), ring.TestSoundRequest{DeviceID: "123", Kind: "alarm"})
		}},
		{"chime missing description", func(c *ring.Client) error {
			return c.SetInHomeChime(context.Background(), ring.SetInHomeChimeRequest{DeviceID: "123", Settings: map[string]interface{}{"type": 1}})
		}},
		{"chime multiple settings", func(c *ring.Client) error {
			return c.SetInHomeChime(context.Background(), ring.SetInHomeChimeRequest{DeviceID: "123", Description: "Bell", Settings: map[string]interface{}{"type": 1, "duration": 5}})
		}},
		{"chime unknown field", func(c *ring.Client) error {
			return c.SetInHomeChime(context.Background(), ring.SetInHomeChimeRequest{DeviceID: "123", Description: "Bell", Settings: map[string]interface{}{"foo": 1}})
		}},
		{"chime invalid value", func(c *ring.Client) error {
			return c.SetInHomeChime(context.Background(), ring.SetInHomeChimeRequest{DeviceID: "123", Description: "Bell", Settings: map[string]interface{}{"type": "mechanical"}})
		}},
		{"chime negative duration", func(c *ring.Client) error {
			return c.SetInHomeChime(context.Background(), ring.SetInHomeChimeRequest{DeviceID: "123", Description: "Bell", Settings: map[string]interface{}{"duration": -1}})
		}},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			client, transport := newTestClientWithToken("portable-token")
			defer client.Close()
			err := tc.call(client)
			require.Error(t, err)
			require.True(t, ringapimodels.IsBadRequestError(err))
			require.Empty(t, transport.GetRequests())
		})
	}
}
