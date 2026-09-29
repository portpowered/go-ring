package replay_test

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"github.com/stretchr/testify/require"
)

func settingsExchange(t *testing.T, fixture, origin string) replay.Exchange {
	t.Helper()

	exchange, err := replay.LoadExchange(filepath.Join("fixtures", "http", "historical", fixture))
	require.NoError(t, err)

	exchange.Request.Origin = origin
	exchange.Request.Path = strings.ReplaceAll(exchange.Request.Path, "{device_id}", "12345")
	exchange.Request.HeadersMode = replay.HeadersRequired

	return exchange
}

// Pairs new typed settings/siren calls with C1 recordings. Each region receives
// an explicit API origin because the captures establish api.ring.com, not
// unverified regional API hosts.
func TestRecordedSettingsAndSirenAcrossRegions(t *testing.T) {
	t.Parallel()

	regions := []ring.Region{ring.RegionUS, ring.RegionEU, ring.RegionFE}
	for _, region := range regions {
		for _, tc := range []struct {
			name string
			file string
			call func(*ring.Client) error
		}{
			{name: "get motion setting", file: "device-settings-get.json", call: func(c *ring.Client) error {
				settings, err := c.GetDeviceSettings(
					context.Background(),
					ring.GetDeviceSettingsRequest{
						Auth:     ring.AuthContext{AccessToken: "recorded-test-token", HardwareID: ""},
						DeviceID: "12345",
					},
				)
				if err == nil && (settings.MotionDetectionEnabled == nil || *settings.MotionDetectionEnabled) {
					return ringapimodels.NewBadRequestError("fixture expected motion detection disabled", nil)
				}

				return wrapReplayTestError("get recorded device settings", err)
			}},
			{name: "patch motion setting", file: "device-settings-patch.json", call: func(c *ring.Client) error {
				enabled := true

				return c.PatchDeviceSettings(
					context.Background(),
					ring.PatchDeviceSettingsRequest{
						Auth:                   ring.AuthContext{AccessToken: "recorded-test-token", HardwareID: ""},
						DeviceID:               "12345",
						MotionDetectionEnabled: &enabled,
					},
				)
			}},
			{name: "siren on", file: "siren-on.json", call: func(c *ring.Client) error {
				return c.SetSiren(
					context.Background(),
					ring.SetSirenRequest{
						Auth:     ring.AuthContext{AccessToken: "recorded-test-token", HardwareID: ""},
						DeviceID: "12345",
						Enabled:  true,
					},
				)
			}},
			{name: "siren off", file: "siren-off.json", call: func(c *ring.Client) error {
				return c.SetSiren(
					context.Background(),
					ring.SetSirenRequest{
						Auth:     ring.AuthContext{AccessToken: "recorded-test-token", HardwareID: ""},
						DeviceID: "12345",
						Enabled:  false,
					},
				)
			}},
		} {
			t.Run(string(region)+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				origin := "https://" + string(region) + ".recorded.example.test"
				exchange := settingsExchange(t, tc.file, origin)
				transport := replay.NewTransport(exchange)
				apiOption := ring.WithEndpoints(ring.Endpoints{APIBaseURL: origin})
				regionOption := ring.WithRegion(region)

				options := []ring.Option{
					ring.WithHTTPClient(&http.Client{Transport: transport}),
					apiOption,
					regionOption,
				}

				if region == ring.RegionEU {
					options[1], options[2] = regionOption, apiOption // precedence is independent of option order
				}

				client, err := ring.NewClient(options...)
				require.NoError(t, err)
				require.NoError(t, tc.call(client))
				require.NoError(t, transport.AssertConsumed())
			})
		}
	}
}

func TestSettingsAndSirenRejectInvalidRequestsBeforeHTTP(t *testing.T) {
	t.Parallel()

	transport := replay.NewTransport()
	client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}))
	require.NoError(t, err)

	ctx := context.Background()
	_, err = client.GetDeviceSettings(
		ctx,
		ring.GetDeviceSettingsRequest{
			Auth:     ring.AuthContext{AccessToken: "recorded-test-token", HardwareID: ""},
			DeviceID: "0",
		},
	)
	require.Error(t, err)
	require.True(t, ringapimodels.IsBadRequestError(err))
	_, err = client.GetDeviceSettings(
		ctx,
		ring.GetDeviceSettingsRequest{
			Auth:     ring.AuthContext{AccessToken: "recorded-test-token", HardwareID: ""},
			DeviceID: "abc",
		},
	)
	require.Error(t, err)
	require.True(t, ringapimodels.IsBadRequestError(err))
	require.Error(
		t,
		client.PatchDeviceSettings(
			ctx,
			ring.PatchDeviceSettingsRequest{
				Auth:     ring.AuthContext{AccessToken: "recorded-test-token"},
				DeviceID: "12345",
			},
		),
	)
	require.Error(
		t,
		client.SetSiren(
			ctx,
			ring.SetSirenRequest{
				Auth:     ring.AuthContext{AccessToken: "recorded-test-token", HardwareID: ""},
				DeviceID: "-1",
				Enabled:  true,
			},
		),
	)
	require.NoError(t, transport.AssertConsumed())
}

func TestRecordedSettingsHTTPError(t *testing.T) {
	t.Parallel()

	const origin = "https://override.example.test"

	exchange := settingsExchange(t, "device-settings-get.json", origin)
	exchange.Response.Status = http.StatusForbidden
	transport := replay.NewTransport(exchange)
	client, err := ring.NewClient(
		ring.WithEndpoints(ring.Endpoints{APIBaseURL: origin}),
		ring.WithHTTPClient(&http.Client{Transport: transport}),
	)
	require.NoError(t, err)
	_, err = client.GetDeviceSettings(
		context.Background(),
		ring.GetDeviceSettingsRequest{
			Auth:     ring.AuthContext{AccessToken: "recorded-test-token", HardwareID: ""},
			DeviceID: "12345",
		},
	)
	require.Error(t, err)
	require.NoError(t, transport.AssertConsumed())
}
