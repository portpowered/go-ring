package system

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"github.com/stretchr/testify/require"
)

func deviceListExchange(t *testing.T, origin string) replay.Exchange {
	t.Helper()
	x, err := replay.LoadExchange(filepath.Join("..", "..", "test", "recordings", "http", "device-list.json"))
	require.NoError(t, err)
	// C1 captures only Accept among request headers; preserve that observed
	// requirement while allowing the public client to add its normal headers.
	x.Request.Origin = origin
	x.Request.HeadersMode = replay.HeadersRequired
	return x
}

func recordedDeviceValues(t *testing.T, exchange replay.Exchange) (int64, string, string, string, string) {
	t.Helper()
	var body struct {
		Devices []struct {
			ID          int64  `json:"id"`
			Kind        string `json:"kind"`
			Description string `json:"description"`
			Address     string `json:"address"`
			TimeZone    string `json:"time_zone"`
		} `json:"devices"`
	}
	require.NoError(t, json.Unmarshal(exchange.Response.Body, &body))
	require.NotEmpty(t, body.Devices)
	d := body.Devices[0]
	return d.ID, d.Description, d.Kind, d.Address, d.TimeZone
}

// Mirrors Python test_ring.py::test_basic_attributes and
// test_ring.py::test_stickup_cam_attributes against the sanitized C1 device
// inventory response. Current capture contains one stickup camera.
func TestRecordedDeviceListAndGetDevice(t *testing.T) {
	const origin = "https://api.ring.com"
	for _, lookup := range []bool{false, true} {
		exchange := deviceListExchange(t, origin)
		id, name, kind, address, timezone := recordedDeviceValues(t, exchange)
		transport := replay.NewTransport(exchange)
		client, err := ring.NewClient(
			ring.WithAccessToken("recorded-test-token"),
			ring.WithHTTPClient(&http.Client{Transport: transport}),
		)
		require.NoError(t, err)
		ctx := context.Background()
		var cams []*ringapimodels.StickUpCam
		if lookup {
			device, getErr := client.GetDevice(ctx, ring.GetDeviceRequest{DeviceID: fmt.Sprint(id)})
			require.NoError(t, getErr)
			cam, ok := device.(*ringapimodels.StickUpCam)
			require.True(t, ok)
			cams = []*ringapimodels.StickUpCam{cam}
		} else {
			devices, listErr := client.ListDevices(ctx)
			require.NoError(t, listErr)
			require.Len(t, devices.StickUpCams, 1)
			cams = devices.StickUpCams
		}
		require.Len(t, cams, 1)
		require.Equal(t, fmt.Sprint(id), cams[0].GetID())
		require.Equal(t, name, cams[0].GetName())
		require.Equal(t, kind, cams[0].Description)
		require.Equal(t, ringapimodels.DeviceFamilyStickUpCam, cams[0].GetFamily())
		require.Equal(t, address, cams[0].GetAddress())
		require.Equal(t, timezone, cams[0].GetTimezone())
		require.NoError(t, transport.AssertConsumed())
	}
}

func TestRecordedDeviceListAcrossRegionsAndEndpointOverrides(t *testing.T) {
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
			options := []ring.Option{ring.WithAccessToken("recorded-test-token"), ring.WithHTTPClient(httpClient)}
			if reverse {
				options = append(options, endpointOption, regionOption)
			} else {
				options = append(options, regionOption, endpointOption)
			}
			client, err := ring.NewClient(options...)
			require.NoError(t, err)
			devices, err := client.ListDevices(context.Background())
			require.NoError(t, err)
			require.Len(t, devices.StickUpCams, 1)
			require.NoError(t, transport.AssertConsumed())
		}
	}
}
