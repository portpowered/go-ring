package replay_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/generatedhttp"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/stretchr/testify/require"
)

// Each synthetic response uses the captured device-list request/response
// envelope, while varying the hardware identity through the generated schema.
func TestDeviceFamilyCatalogReplay(t *testing.T) {
	for _, tc := range []struct {
		kind   string
		family *string
		want   generatedhttp.DeviceFamilyCode
	}{
		{kind: "doorbell_oyster", want: generatedhttp.Doorbots},
		{kind: "lpd_v4", want: generatedhttp.Doorbots},
		{kind: "df_doorbell_clownfish", want: generatedhttp.Doorbots},
		{kind: "chime_pro_v2", want: generatedhttp.Chimes},
		{kind: "hp_cam_v1", want: generatedhttp.StickupCams},
		{kind: "hp_cam_v2", want: generatedhttp.StickupCams},
		{kind: "stickup_cam_mini_ptz_v1", want: generatedhttp.StickupCams},
		{kind: "cocoa_camera", want: generatedhttp.StickupCams},
		{kind: "intercom_handset_video", want: generatedhttp.Other},
		{kind: "beams_ct200_transformer", want: generatedhttp.Other},
		{kind: "future_model", want: generatedhttp.Other},
		{kind: "future_model_with_family", family: familyPointer(generatedhttp.StickupCams), want: generatedhttp.StickupCams},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			x := deviceListExchange(t, "https://api.ring.com")
			body, err := json.Marshal(generatedhttp.DeviceList{Devices: []generatedhttp.Device{{Id: 42, Kind: tc.kind, Family: tc.family, Description: tc.kind}}})
			require.NoError(t, err)
			x.Response.Body = body
			transport := replay.NewTransport(x)
			client, err := ring.NewClient(ring.WithAccessToken("recorded-test-token"), ring.WithHTTPClient(&http.Client{Transport: transport}))
			require.NoError(t, err)
			defer client.Close()
			devices, err := client.ListDevices(context.Background())
			require.NoError(t, err)
			switch tc.want {
			case generatedhttp.Doorbots:
				require.Len(t, devices.Doorbells, 1)
			case generatedhttp.Chimes:
				require.Len(t, devices.Chimes, 1)
			case generatedhttp.StickupCams:
				require.Len(t, devices.StickUpCams, 1)
			default:
				require.Len(t, devices.Other, 1)
				require.Equal(t, tc.kind, devices.Other[0].Kind)
			}
			require.NoError(t, transport.AssertConsumed())
		})
	}
}

func familyPointer(value generatedhttp.DeviceFamilyCode) *string {
	family := string(value)
	return &family
}
