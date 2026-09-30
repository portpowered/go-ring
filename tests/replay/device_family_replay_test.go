package replay_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/generatedhttp"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"github.com/stretchr/testify/require"
)

// Each synthetic response uses the captured device-list request/response
// envelope, preserves raw hardware identity, and normalizes known hardware types.
func TestDeviceIdentityReplay(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		kind   string
		family *string
		want   ringapimodels.DeviceType
	}{
		{kind: "doorbell_oyster", family: nil, want: ringapimodels.DeviceTypeDoorbell},
		{kind: "lpd_v4", family: nil, want: ringapimodels.DeviceTypeDoorbell},
		{kind: "df_doorbell_clownfish", family: nil, want: ringapimodels.DeviceTypeDoorbell},
		{kind: "chime_pro_v2", family: nil, want: ringapimodels.DeviceTypeChime},
		{kind: "hp_cam_v1", family: nil, want: ringapimodels.DeviceTypeCamera},
		{kind: "hp_cam_v2", family: nil, want: ringapimodels.DeviceTypeCamera},
		{kind: "stickup_cam_mini_ptz_v1", family: nil, want: ringapimodels.DeviceTypeCamera},
		{kind: "cocoa_camera", family: nil, want: ringapimodels.DeviceTypeCamera},
		{kind: "intercom_handset_video", family: nil, want: ringapimodels.DeviceTypeOther},
		{kind: "beams_ct200_transformer", family: nil, want: ringapimodels.DeviceTypeOther},
		{kind: "future_model", family: nil, want: ringapimodels.DeviceTypeOther},
		{kind: "explicit_chime", family: familyPointer(generatedhttp.Chimes), want: ringapimodels.DeviceTypeChime},
		{kind: "future_model_with_family", family: familyPointer(generatedhttp.StickupCams),
			want: ringapimodels.DeviceTypeCamera},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			t.Parallel()

			exchange := deviceListExchange(t, "https://api.ring.com")
			liveView := true

			var health generatedhttp.DeviceHealth

			health.VodEnabled = &liveView
			body, err := json.Marshal(
				generatedhttp.DeviceList{
					Devices: []generatedhttp.Device{{
						Id: 42, Kind: tc.kind, Family: tc.family, Description: tc.kind,
						Health: &health,
					}},
				},
			)
			require.NoError(t, err)

			exchange.Response.Body = body
			transport := replay.NewTransport(exchange)
			client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}))
			require.NoError(t, err)

			defer func() { _ = client.Close() }()

			devices, err := client.ListDevices(
				context.Background(),
				ring.ListDevicesRequest{Auth: ring.AuthContext{AccessToken: "recorded-test-token", HardwareID: ""}},
			)
			require.NoError(t, err)
			require.Len(t, devices.Devices, 1)
			require.Equal(t, tc.kind, devices.Devices[0].Kind)

			if tc.family == nil {
				require.Empty(t, devices.Devices[0].Family)
			} else {
				require.Equal(t, *tc.family, devices.Devices[0].Family)
			}

			require.Equal(t, tc.want, devices.Devices[0].Type)
			require.True(t, devices.Devices[0].Supports(ringapimodels.DeviceCapabilityLiveView))

			require.NoError(t, transport.AssertConsumed())
		})
	}
}

func familyPointer(value generatedhttp.DeviceFamilyCode) *string {
	family := string(value)

	return &family
}
