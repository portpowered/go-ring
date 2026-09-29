package replay_test

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
	t.Parallel()

	checks := append(volumeValidationCases(), otherControlValidationCases()...)
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			client, transport := newTestClientWithMockTransport()

			t.Cleanup(func() { _ = client.Close() })

			err := tc.call(client)
			require.Error(t, err)
			require.True(t, ringapimodels.IsBadRequestError(err))
			require.Empty(t, transport.GetRequests())
		})
	}
}

type controlValidationCase struct {
	name string
	call func(*ring.Client) error
}

func volumeValidationCases() []controlValidationCase {
	return []controlValidationCase{
		{"volume below range", func(c *ring.Client) error {
			return c.SetVolume(
				context.Background(),
				ring.SetVolumeRequest{
					Auth:        ring.AuthContext{AccessToken: "test_token", HardwareID: ""},
					DeviceID:    "123",
					Kind:        ringapimodels.VolumeKindChime,
					Description: "Bell",
					Volume:      -1,
				},
			)
		}},
		{"volume above range", func(c *ring.Client) error {
			return c.SetVolume(
				context.Background(),
				ring.SetVolumeRequest{
					Auth:        ring.AuthContext{AccessToken: "test_token", HardwareID: ""},
					DeviceID:    "123",
					Kind:        ringapimodels.VolumeKindChime,
					Description: "Bell",
					Volume:      12,
				},
			)
		}},
		{"volume missing device", func(c *ring.Client) error {
			return c.SetVolume(
				context.Background(),
				ring.SetVolumeRequest{
					Auth:        ring.AuthContext{AccessToken: "test_token"},
					Kind:        ringapimodels.VolumeKindChime,
					Description: "Bell",
					Volume:      2,
				},
			)
		}},
		{"volume invalid device", func(c *ring.Client) error {
			return c.SetVolume(
				context.Background(),
				ring.SetVolumeRequest{
					Auth:        ring.AuthContext{AccessToken: "test_token", HardwareID: ""},
					DeviceID:    "0",
					Kind:        ringapimodels.VolumeKindChime,
					Description: "Bell",
					Volume:      2,
				},
			)
		}},
		{"volume missing kind", func(c *ring.Client) error {
			return c.SetVolume(
				context.Background(),
				ring.SetVolumeRequest{
					Auth:        ring.AuthContext{AccessToken: "test_token"},
					DeviceID:    "123",
					Description: "Bell",
					Volume:      2,
				},
			)
		}},
		{"volume missing description", func(c *ring.Client) error {
			return c.SetVolume(
				context.Background(),
				ring.SetVolumeRequest{
					Auth:     ring.AuthContext{AccessToken: "test_token"},
					DeviceID: "123",
					Kind:     ringapimodels.VolumeKindDoorbell,
					Volume:   2,
				},
			)
		}},
	}
}

func otherControlValidationCases() []controlValidationCase {
	return []controlValidationCase{
		{"light invalid device", func(c *ring.Client) error {
			return c.SetLights(
				context.Background(),
				ring.SetLightsRequest{
					Auth:     ring.AuthContext{AccessToken: "test_token", HardwareID: ""},
					DeviceID: "bad",
					Enabled:  true,
				},
			)
		}},
		{"sound invalid kind", func(c *ring.Client) error {
			return c.TestSound(
				context.Background(),
				ring.TestSoundRequest{
					Auth:     ring.AuthContext{AccessToken: "test_token", HardwareID: ""},
					DeviceID: "123",
					Sound:    "alarm",
				},
			)
		}},
		{"chime missing device", func(c *ring.Client) error {
			return c.SetInHomeChime(
				context.Background(),
				ring.SetInHomeChimeRequest{
					Auth:        ring.AuthContext{AccessToken: "test_token"},
					Description: "Bell",
					Settings:    ringapimodels.InHomeChimeSettings{Type: chimePointer(1)},
				},
			)
		}},
		{"chime invalid device", func(c *ring.Client) error {
			return c.SetInHomeChime(
				context.Background(),
				ring.SetInHomeChimeRequest{
					Auth:        ring.AuthContext{AccessToken: "test_token", HardwareID: ""},
					DeviceID:    "bad",
					Description: "Bell",
					Settings:    ringapimodels.InHomeChimeSettings{Type: chimePointer(1)},
				},
			)
		}},
		{"chime missing description", func(c *ring.Client) error {
			return c.SetInHomeChime(
				context.Background(),
				ring.SetInHomeChimeRequest{
					Auth:     ring.AuthContext{AccessToken: "test_token"},
					DeviceID: "123",
					Settings: ringapimodels.InHomeChimeSettings{Type: chimePointer(1)},
				},
			)
		}},
		{"chime multiple settings", func(c *ring.Client) error {
			return c.SetInHomeChime(
				context.Background(),
				ring.SetInHomeChimeRequest{
					Auth:        ring.AuthContext{AccessToken: "test_token", HardwareID: ""},
					DeviceID:    "123",
					Description: "Bell",
					Settings:    ringapimodels.InHomeChimeSettings{Type: chimePointer(1), Duration: chimePointer(5)},
				},
			)
		}},
		{"chime missing setting", func(c *ring.Client) error {
			return c.SetInHomeChime(
				context.Background(),
				ring.SetInHomeChimeRequest{
					Auth:        ring.AuthContext{AccessToken: "test_token"},
					DeviceID:    "123",
					Description: "Bell",
				},
			)
		}},
		{"chime negative type", func(c *ring.Client) error {
			return c.SetInHomeChime(
				context.Background(),
				ring.SetInHomeChimeRequest{
					Auth:        ring.AuthContext{AccessToken: "test_token", HardwareID: ""},
					DeviceID:    "123",
					Description: "Bell",
					Settings:    ringapimodels.InHomeChimeSettings{Type: chimePointer(-1)},
				},
			)
		}},
		{"chime negative duration", func(c *ring.Client) error {
			return c.SetInHomeChime(
				context.Background(),
				ring.SetInHomeChimeRequest{
					Auth:        ring.AuthContext{AccessToken: "test_token", HardwareID: ""},
					DeviceID:    "123",
					Description: "Bell",
					Settings:    ringapimodels.InHomeChimeSettings{Duration: chimePointer(-1)},
				},
			)
		}},
		{"health missing device", func(c *ring.Client) error {
			_, err := c.UpdateDeviceHealth(
				context.Background(),
				ring.UpdateDeviceHealthRequest{Auth: ring.AuthContext{AccessToken: "test_token"}},
			)

			return wrapReplayTestError("validate health request without device id", err)
		}},
	}
}

func chimePointer[T any](value T) *T { return &value }
