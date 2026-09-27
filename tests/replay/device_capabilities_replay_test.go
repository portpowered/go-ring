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
	trueValue, falseValue := true, false
	commands := []string{protocol.RPCPanStep, protocol.RPCTiltContinuous, "future.command"}
	for _, tc := range []struct {
		name   string
		device generatedhttp.Device
		want   []ringapimodels.DeviceCapability
	}{
		{name: "unknown hardware without evidence", device: generatedhttp.Device{Id: 1, Kind: "future", Description: "future"}, want: []ringapimodels.DeviceCapability{}},
		{name: "explicit disabled controls", device: generatedhttp.Device{Id: 2, Kind: "future", Description: "future", HasLight: &falseValue, MotionDetectionEnabled: &falseValue, Health: &generatedhttp.DeviceHealth{SirenOn: &falseValue, VodEnabled: &falseValue}}, want: []ringapimodels.DeviceCapability{ringapimodels.DeviceCapabilityMotionDetection, ringapimodels.DeviceCapabilitySiren}},
		{name: "selected RPC controls", device: generatedhttp.Device{Id: 3, Kind: "future", Description: "future", HasLight: &trueValue, Health: &generatedhttp.DeviceHealth{VodEnabled: &trueValue, SupportedRpcCommands: &commands}}, want: []ringapimodels.DeviceCapability{ringapimodels.DeviceCapabilityLight, ringapimodels.DeviceCapabilityLiveView, ringapimodels.DeviceCapabilityPtzPanStep, ringapimodels.DeviceCapabilityPtzTiltContinuous}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exchange := deviceListExchange(t, "https://api.ring.com")
			body, err := json.Marshal(generatedhttp.DeviceList{Devices: []generatedhttp.Device{tc.device}})
			require.NoError(t, err)
			exchange.Response.Body = body
			transport := replay.NewTransport(exchange)
			client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}))
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })
			response, err := client.ListDevices(context.Background(), ring.ListDevicesRequest{Auth: ring.AuthContext{AccessToken: "replay-token"}})
			require.NoError(t, err)
			require.Len(t, response.Devices, 1)
			require.Equal(t, tc.want, response.Devices[0].Capabilities)
			require.NoError(t, transport.AssertConsumed())
		})
	}
}
