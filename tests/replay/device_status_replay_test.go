package replay_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/stretchr/testify/require"
)

func TestBaselineDeviceBatteryVariants(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("fixtures", "http", "baseline", "ring_devices.json"))
	require.NoError(t, err)

	var families map[string][]json.RawMessage

	require.NoError(t, json.Unmarshal(data, &families))

	for _, test := range []struct {
		family string
		want   *float64
	}{
		{"authorized_doorbots", batteryPointer(51)},
		{"doorbots", nil},             // 4081 is not a percentage.
		{"other", batteryPointer(52)}, // Numeric text on an intercom.
	} {
		t.Run(test.family, func(t *testing.T) {
			t.Parallel()

			require.NotEmpty(t, families[test.family])

			var device ring.DeviceDetailDevice

			require.NoError(t, json.Unmarshal(families[test.family][0], &device))
			require.Equal(t, test.want, device.Status().BatteryPercent)
		})
	}
}

func batteryPointer(value float64) *float64 { return &value }

func TestSyntheticDeviceStatusVariants(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("fixtures", "http", "synthetic", "device-status-variants.json"))
	require.NoError(t, err)

	var cases []struct {
		Name       string          `json:"name"`
		Device     json.RawMessage `json:"device"`
		Offline    bool            `json:"offline"`
		Battery    *float64        `json:"battery"`
		Connection *string         `json:"connection"`
	}

	require.NoError(t, json.Unmarshal(data, &cases))

	for _, test := range cases {
		t.Run(test.Name, func(t *testing.T) {
			t.Parallel()

			var device ring.DeviceDetailDevice

			require.NoError(t, json.Unmarshal(test.Device, &device))
			status := device.Status()
			require.Equal(t, test.Offline, status.IsOffline)
			require.Equal(t, test.Battery, status.BatteryPercent)

			if test.Connection == nil {
				require.Nil(t, status.Connection)
			} else {
				require.Equal(t, *test.Connection, string(*status.Connection))
			}
		})
	}
}
