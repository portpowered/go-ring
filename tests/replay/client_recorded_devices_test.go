package replay_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"github.com/stretchr/testify/require"
)

type recordedDeviceListBody struct {
	Devices []recordedDevice `json:"devices"`
}

type recordedDevice struct {
	ID          int64  `json:"id"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
	Address     string `json:"address"`
	TimeZone    string `json:"time_zone"`
}

func deviceListExchange(t *testing.T, origin string) replay.Exchange {
	t.Helper()

	exchange, err := replay.LoadExchange(filepath.Join("fixtures", "http", "historical", "device-list.json"))
	require.NoError(t, err)
	// C1 captures only Accept among request headers; preserve that observed
	// requirement while allowing the public client to add its normal headers.
	exchange.Request.Origin = origin
	exchange.Request.HeadersMode = replay.HeadersRequired

	return exchange
}

func recordedDeviceValues(t *testing.T, exchange replay.Exchange) (int64, string, string, string, string) {
	t.Helper()

	var body recordedDeviceListBody

	require.NoError(t, json.Unmarshal(exchange.Response.Body, &body))
	require.NotEmpty(t, body.Devices)
	d := body.Devices[0]

	return d.ID, d.Description, d.Kind, d.Address, d.TimeZone
}

// Mirrors Python test_ring.py::test_basic_attributes and
// test_ring.py::test_stickup_cam_attributes against the sanitized C1 device
// inventory response. Current capture contains one stickup camera.
func TestRecordedDeviceListAndGetDevice(t *testing.T) {
	t.Parallel()

	const origin = "https://api.ring.com"
	for _, lookup := range []bool{false, true} {
		exchange := deviceListExchange(t, origin)
		id, name, kind, address, timezone := recordedDeviceValues(t, exchange)
		transport := replay.NewTransport(exchange)
		client, err := ring.NewClient(
			ring.WithHTTPClient(&http.Client{Transport: transport}),
		)
		require.NoError(t, err)

		ctx := context.Background()

		var cams []ringapimodels.Device

		if lookup {
			device, getErr := client.GetDevice(
				ctx,
				ring.GetDeviceRequest{
					Auth:     ring.AuthContext{AccessToken: "recorded-test-token", HardwareID: ""},
					DeviceID: strconv.FormatInt(id, 10),
				},
			)
			require.NoError(t, getErr)

			cams = []ringapimodels.Device{*device}
		} else {
			devices, listErr := client.ListDevices(
				ctx,
				ring.ListDevicesRequest{
					Auth: ring.AuthContext{AccessToken: "recorded-test-token", HardwareID: ""},
				},
			)
			require.NoError(t, listErr)
			require.Len(t, devices.Devices, 1)
			cams = devices.Devices
		}

		require.Len(t, cams, 1)
		require.Equal(t, strconv.FormatInt(id, 10), cams[0].ID)
		require.Equal(t, name, cams[0].Name)
		require.Equal(t, kind, cams[0].Kind)
		require.Empty(t, cams[0].Family, "v3 capture does not declare a family")
		require.Equal(t, address, cams[0].Address)
		require.Equal(t, timezone, cams[0].Timezone)
		require.True(t, cams[0].Supports(ringapimodels.DeviceCapabilityPtzPanStep))
		require.True(t, cams[0].Supports(ringapimodels.DeviceCapabilityLiveView))
		require.NoError(t, transport.AssertConsumed())
	}
}

func TestRecordedDeviceListAcrossRegionsAndEndpointOverrides(t *testing.T) {
	t.Parallel()

	for _, region := range []ring.Region{ring.RegionUS, ring.RegionEU, ring.RegionFE} {
		origin := "https://" + string(region) + ".api.example.test"
		for _, reverse := range []bool{false, true} {
			transport := replay.NewTransport(deviceListExchange(t, origin))
			httpClient := &http.Client{Transport: transport}
			regionOption := ring.WithRegion(region)
			endpointOption := ring.WithEndpoints(ring.Endpoints{
				APIBaseURL:       origin,
				SolutionsBaseURL: "https://" + string(region) + ".solutions.example.test",
			})

			options := []ring.Option{ring.WithHTTPClient(httpClient)}

			if reverse {
				options = append(options, endpointOption, regionOption)
			} else {
				options = append(options, regionOption, endpointOption)
			}

			client, err := ring.NewClient(options...)
			require.NoError(t, err)
			devices, err := client.ListDevices(
				context.Background(),
				ring.ListDevicesRequest{Auth: ring.AuthContext{AccessToken: "recorded-test-token", HardwareID: ""}},
			)
			require.NoError(t, err)
			require.Len(t, devices.Devices, 1)
			require.NoError(t, transport.AssertConsumed())
		}
	}
}
