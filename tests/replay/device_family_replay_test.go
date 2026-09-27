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
// envelope and preserves hardware identity without client-side classification.
func TestDeviceIdentityReplay(t *testing.T) {
	for _, tc := range []struct {
		kind   string
		family *string
	}{
		{kind: "doorbell_oyster"},
		{kind: "lpd_v4"},
		{kind: "df_doorbell_clownfish"},
		{kind: "chime_pro_v2"},
		{kind: "hp_cam_v1"},
		{kind: "hp_cam_v2"},
		{kind: "stickup_cam_mini_ptz_v1"},
		{kind: "cocoa_camera"},
		{kind: "intercom_handset_video"},
		{kind: "beams_ct200_transformer"},
		{kind: "future_model"},
		{kind: "future_model_with_family", family: familyPointer(generatedhttp.StickupCams)},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			x := deviceListExchange(t, "https://api.ring.com")
			body, err := json.Marshal(generatedhttp.DeviceList{Devices: []generatedhttp.Device{{Id: 42, Kind: tc.kind, Family: tc.family, Description: tc.kind}}})
			require.NoError(t, err)
			x.Response.Body = body
			transport := replay.NewTransport(x)
			client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}))
			require.NoError(t, err)
			defer func() { _ = client.Close() }()
			devices, err := client.ListDevices(context.Background(), ring.ListDevicesRequest{Auth: ring.AuthContext{AccessToken: "recorded-test-token"}})
			require.NoError(t, err)
			require.Len(t, devices.Devices, 1)
			require.Equal(t, tc.kind, devices.Devices[0].Kind)
			if tc.family == nil {
				require.Empty(t, devices.Devices[0].Family)
			} else {
				require.Equal(t, *tc.family, devices.Devices[0].Family)
			}
			require.NoError(t, transport.AssertConsumed())
		})
	}
}

func familyPointer(value generatedhttp.DeviceFamilyCode) *string {
	family := string(value)
	return &family
}
