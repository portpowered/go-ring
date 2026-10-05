package replay_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/generatedhttp"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"github.com/stretchr/testify/require"
)

func TestDeviceCapabilitiesComeFromInventoryEvidence(t *testing.T) {
	t.Parallel()

	trueValue, falseValue := true, false

	commands := []string{protocol.RPCPanStep, protocol.RPCTiltContinuous, "future.command"}

	for _, tc := range []struct {
		name   string
		device generatedhttp.Device
		want   []ringapimodels.DeviceCapability
	}{
		{
			name:   "unknown hardware without evidence",
			device: capabilityDeviceFixture(1, "future", "future", nil, nil, nil),
			want:   []ringapimodels.DeviceCapability{},
		},
		{
			name: "explicit disabled controls",
			device: capabilityDeviceFixture(
				2,
				"future",
				"future",
				&falseValue,
				&falseValue,
				capabilityHealthFixture(&falseValue, &falseValue, nil),
			),
			want: []ringapimodels.DeviceCapability{
				ringapimodels.DeviceCapabilityMotionDetection,
				ringapimodels.DeviceCapabilitySiren,
			},
		},
		{
			name: "selected RPC controls",
			device: capabilityDeviceFixture(
				3,
				"future",
				"future",
				&trueValue,
				nil,
				capabilityHealthFixture(nil, &trueValue, &commands),
			),
			want: []ringapimodels.DeviceCapability{
				ringapimodels.DeviceCapabilityLight,
				ringapimodels.DeviceCapabilityLiveView,
				ringapimodels.DeviceCapabilityPtzPanStep,
				ringapimodels.DeviceCapabilityPtzTiltContinuous,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			exchange := deviceListExchange(t, "https://api.ring.com")

			var list generatedhttp.DeviceList

			list.Devices = []generatedhttp.Device{tc.device}
			body, err := json.Marshal(list)
			require.NoError(t, err)

			exchange.Response.Body = body
			transport := replay.NewTransport(exchange)
			client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}))
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })

			response, err := client.ListDevices(
				context.Background(),
				ring.ListDevicesRequest{Auth: ring.AuthContext{AccessToken: "replay-token", HardwareID: ""}},
			)
			require.NoError(t, err)
			require.Len(t, response.Devices, 1)
			require.Equal(t, tc.want, response.Devices[0].Capabilities)
			require.NoError(t, transport.AssertConsumed())
		})
	}
}

func capabilityDeviceFixture(
	id int64,
	kind, description string,
	hasLight, motionEnabled *bool,
	health *generatedhttp.DeviceHealth,
) generatedhttp.Device {
	var device generatedhttp.Device

	device.Id = id
	device.Kind = kind
	device.Description = description
	device.HasLight = hasLight
	device.MotionDetectionEnabled = motionEnabled
	device.Health = health

	return device
}

func capabilityHealthFixture(
	sirenOn, vodEnabled *bool,
	commands *[]string,
) *generatedhttp.DeviceHealth {
	var health generatedhttp.DeviceHealth

	health.SirenOn = sirenOn
	health.VodEnabled = vodEnabled
	health.SupportedRpcCommands = commands

	return &health
}
