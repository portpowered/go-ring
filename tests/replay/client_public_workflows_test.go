package replay_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"github.com/stretchr/testify/require"
)

type contextValueKey struct{}

type captureRoundTripper struct {
	responseBody string
	requests     []*http.Request
	bodies       [][]byte
}

func (c *captureRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	c.requests = append(c.requests, req.Clone(req.Context()))
	c.bodies = append(c.bodies, body)
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(c.responseBody)),
		Request:    req,
	}, nil
}

func TestListDevicesUsesTokenGetterAndPropagatesConfiguredRequestHeaders(t *testing.T) {
	ctx := context.WithValue(context.Background(), contextValueKey{}, "trace-context")
	transport := &captureRoundTripper{responseBody: `{"devices":[]}`}
	client, err := ring.NewClient(
		ring.WithHTTPClient(&http.Client{Transport: transport}),
		ring.WithUserAgent("test-agent/1"),
		ring.WithEndpoints(ring.Endpoints{APIBaseURL: "https://api.override.example"}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	for range 2 {
		devices, listErr := client.ListDevices(ctx, ring.ListDevicesRequest{Auth: ring.AuthContext{AccessToken: "dynamic-token", HardwareID: "hardware-abc"}})
		require.NoError(t, listErr)
		require.NotNil(t, devices)
	}
	require.Len(t, transport.requests, 4)
	require.Equal(t, http.MethodPost, transport.requests[0].Method, "hardware ID triggers session registration")
	for i, req := range transport.requests {
		if i%2 == 0 {
			require.Equal(t, "https://api.override.example/clients_api/session", req.URL.String())
		} else {
			require.Equal(t, "https://api.override.example/device_info/v3/devices", req.URL.String())
			require.Equal(t, http.MethodGet, req.Method)
		}
		require.Equal(t, "Bearer dynamic-token", req.Header.Get("Authorization"))
		require.Equal(t, "test-agent/1", req.Header.Get("User-Agent"))
		require.Equal(t, "hardware-abc", req.Header.Get("hardware_id"))
		require.Equal(t, "application/json", req.Header.Get("Accept"))
		require.Equal(t, "application/json", req.Header.Get("Content-Type"))
	}
}

func TestMissingRequestTokenStopsBeforeHTTP(t *testing.T) {
	transport := &captureRoundTripper{responseBody: `{"devices":[]}`}
	client, err := ring.NewClient(
		ring.WithHTTPClient(&http.Client{Transport: transport}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	_, err = client.ListDevices(context.Background(), ring.ListDevicesRequest{})
	require.Error(t, err)
	require.True(t, ringapimodels.IsTokenError(err))
	require.Empty(t, transport.requests)
}

func TestAuthenticateUsesOnlyRequestCredentials(t *testing.T) {
	for _, tc := range []struct {
		name     string
		username string
		password string
	}{
		{name: "first account", username: "first-user", password: "first-password"},
		{name: "second account", username: "second-user", password: "second-password"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := &pkceMockTransport{}
			client, err := ring.NewClient(
				ring.WithUserAgent("auth-test/1"),
				ring.WithHTTPClient(&http.Client{Transport: transport}),
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })

			_, err = client.Authenticate(context.Background(), ring.AuthenticateRequest{
				Username:   tc.username,
				Password:   tc.password,
				OTPCode:    "123456",
				HardwareID: "hardware-auth",
			})
			require.NoError(t, err)
			require.GreaterOrEqual(t, len(transport.bodies), 3)
			form, parseErr := url.ParseQuery(transport.bodies[1])
			require.NoError(t, parseErr)
			require.Equal(t, tc.username, form.Get("username"))
			require.Equal(t, tc.password, form.Get("password"))
			require.Equal(t, "auth-test/1", transport.requests[1].Header.Get("User-Agent"))
			require.Equal(t, "hardware-auth", transport.requests[4].Header.Get("hardware_id"))
			verifyForm, parseErr := url.ParseQuery(transport.bodies[2])
			require.NoError(t, parseErr)
			require.Equal(t, "123456", verifyForm.Get("2fa_code"))
		})
	}
}

func TestRefreshTokenUsesOnlyRequestCredentials(t *testing.T) {
	for _, tc := range []struct {
		name         string
		refreshToken string
	}{
		{name: "first account", refreshToken: "first-refresh"},
		{name: "second account", refreshToken: "second-refresh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := &captureRoundTripper{responseBody: `{"access_token":"new-access","refresh_token":"rotated-refresh","expires_in":3600,"token_type":"Bearer"}`}
			client, err := ring.NewClient(
				ring.WithUserAgent("refresh-test/1"),
				ring.WithHTTPClient(&http.Client{Transport: transport}),
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })

			request := ring.RefreshTokenRequest{RefreshToken: tc.refreshToken, HardwareID: "hardware-refresh"}
			_, err = client.RefreshToken(context.Background(), request)
			require.NoError(t, err)
			require.Len(t, transport.requests, 1)
			form, parseErr := url.ParseQuery(string(transport.bodies[0]))
			require.NoError(t, parseErr)
			require.Equal(t, tc.refreshToken, form.Get("refresh_token"))
			require.Equal(t, "hardware-refresh", transport.requests[0].Header.Get("hardware_id"))
			require.Equal(t, "refresh-test/1", transport.requests[0].Header.Get("User-Agent"))
		})
	}
}

// Mirrors Python test_ring.py's basic/chime/doorbell/shared-doorbell and
// test_other.py::test_other_attributes using values from the existing Go
// fixture. The C1 v3 capture establishes only a camera row, so this fixture
// tests conversion and generic getters, not a vendor v3 family wire contract.
func TestGetAllDevicesUniformMetadataAndDefaultsAcrossFamilies(t *testing.T) {
	const body = `{"devices":[
		{"id":101,"kind":"lpd_v1","family":"doorbots","owned":true,"description":"Front Door","name":"","address":"123 Main St","timezone":"America/New_York","time_zone":"ignored","volume":1,"has_light":true,"light_brightness":2,"motion_detection_enabled":true,"health":{"signal_strength":-58}},
		{"id":102,"kind":"lpd_v1","family":"doorbots","owned":false,"description":"Shared Door","address":"2 Side St","time_zone":"America/New_York"},
		{"id":201,"kind":"chime","family":"chimes","name":"Hall Chime","volume":2},
		{"id":301,"kind":"hp_cam_v1","family":"stickup_cams","description":"Camera","address":"4 Back St","time_zone":"America/Phoenix"},
		{"id":401,"kind":"intercom_handset_audio","family":"other","description":"Lobby Intercom","time_zone":"Europe/Rome"},
		{"id":501,"kind":"future_device_kind","family":"future_family","description":"Unknown"}
	]}`
	transport := &captureRoundTripper{responseBody: body}
	client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	devices, err := client.ListDevices(context.Background(), ring.ListDevicesRequest{Auth: ring.AuthContext{AccessToken: "inventory-token"}})
	require.NoError(t, err)
	require.Len(t, devices.Doorbells, 2, "owned and shared doorbells are both exposed")
	require.Len(t, devices.Chimes, 1)
	require.Len(t, devices.StickUpCams, 1)
	require.Len(t, devices.Other, 2, "intercom and unfamiliar kinds remain available as generic devices")

	all := devices.GetAllDevices()
	require.Len(t, all, 6)
	for _, device := range all {
		require.NotEmpty(t, device.GetID())
		require.NotEmpty(t, device.GetFamily())
		// Missing optional text values normalize to the empty string, not panic.
		_ = device.GetName()
		_ = device.GetAddress()
		_ = device.GetTimezone()
	}
	require.Equal(t, "Front Door", devices.Doorbells[0].GetName(), "description is used if name is empty")
	require.Equal(t, "America/New_York", devices.Doorbells[0].GetTimezone(), "primary timezone wins")
	require.Equal(t, "Shared Door", devices.Doorbells[1].GetName())
	require.Equal(t, "", devices.Chimes[0].GetAddress())
	require.Equal(t, "", devices.Chimes[0].GetTimezone())
	require.Equal(t, ringapimodels.DeviceFamilyChime, devices.Chimes[0].GetFamily())
	require.Equal(t, "Camera", devices.StickUpCams[0].GetName())
	require.Equal(t, "America/Phoenix", devices.StickUpCams[0].GetTimezone())
	require.Equal(t, "Lobby Intercom", devices.Other[0].GetName())
	require.Equal(t, ringapimodels.DeviceFamilyOther, devices.Other[0].GetFamily())
	require.Equal(t, "future_device_kind", devices.Other[1].Kind)
	require.Equal(t, "future_family", devices.Other[1].Family)
	require.Equal(t, "Unknown", devices.Other[1].GetName())
	require.Equal(t, "", devices.Other[1].GetAddress())
	require.Equal(t, "", devices.Other[1].GetTimezone())
	if health := devices.Doorbells[0].Health; health != nil {
		require.NotNil(t, health.SignalStrength)
		require.Equal(t, -58, *health.SignalStrength)
	} else {
		t.Fatal("captured-style health metadata was dropped")
	}
}

func TestGetDeviceSettingsDistinguishesUnknownFromCapturedFalse(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "omitted", body: `{"motion_settings":{}}`},
		{name: "null", body: `{"motion_settings":{"motion_detection_enabled":null}}`},
		{name: "explicit false", body: `{"motion_settings":{"motion_detection_enabled":false}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := &captureRoundTripper{responseBody: tc.body}
			client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}))
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })
			settings, err := client.GetDeviceSettings(context.Background(), ring.GetDeviceSettingsRequest{Auth: ring.AuthContext{AccessToken: "settings-token"}, DeviceID: "101"})
			require.NoError(t, err)
			if tc.name == "explicit false" {
				require.NotNil(t, settings.MotionDetectionEnabled)
				require.False(t, *settings.MotionDetectionEnabled)
			} else {
				require.Nil(t, settings.MotionDetectionEnabled, "absent settings are unknown, not disabled")
			}
		})
	}
}

// AuthContext recovers a hardware ID from the JWT unless explicitly supplied.
func TestAuthContextClaimHandlingAndHardwareIDPrecedence(t *testing.T) {
	validPayload := base64.RawURLEncoding.EncodeToString([]byte(`{"hardware_id":"jwt-hardware"}`))
	missingPayload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"account"}`))
	malformedJSON := base64.RawURLEncoding.EncodeToString([]byte(`{"hardware_id":`))
	for _, tc := range []struct {
		name               string
		token              string
		explicitHardwareID string
		wantHardwareID     string
	}{
		{name: "valid claim", token: "header." + validPayload + ".signature", wantHardwareID: "jwt-hardware"},
		{name: "explicit option wins", token: "header." + validPayload + ".signature", explicitHardwareID: "explicit-hardware", wantHardwareID: "explicit-hardware"},
		{name: "missing claim", token: "header." + missingPayload + ".signature"},
		{name: "bad base64 claim", token: "header.%%%.signature"},
		{name: "bad JSON claim", token: "header." + malformedJSON + ".signature"},
		{name: "malformed token", token: "not-a-jwt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := &captureRoundTripper{responseBody: `{"devices":[]}`}
			options := []ring.Option{
				ring.WithHTTPClient(&http.Client{Transport: transport}),
				ring.WithEndpoints(ring.Endpoints{APIBaseURL: "https://api.token-claims.example"}),
			}
			client, err := ring.NewClient(options...)
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })
			_, err = client.ListDevices(context.Background(), ring.ListDevicesRequest{Auth: ring.AuthContext{AccessToken: tc.token, HardwareID: tc.explicitHardwareID}})
			require.NoError(t, err)

			if tc.wantHardwareID == "" {
				require.Len(t, transport.requests, 1, "without a claim there is no hardware session registration")
				require.Equal(t, http.MethodGet, transport.requests[0].Method)
				return
			}
			require.Len(t, transport.requests, 2)
			require.Equal(t, http.MethodPost, transport.requests[0].Method)
			require.Equal(t, "/clients_api/session", transport.requests[0].URL.Path)
			require.Equal(t, tc.wantHardwareID, transport.requests[0].Header.Get("hardware_id"))
			require.Equal(t, "Bearer "+tc.token, transport.requests[0].Header.Get("Authorization"))
		})
	}
}

// Input and cancellation guards are synthetic robustness tests, not captured
// Ring errors. They ensure malformed public requests never reach HTTP transport.
func TestInvalidPublicDeviceRequestsDoNotReachHTTP(t *testing.T) {
	transport := &captureRoundTripper{responseBody: `{"devices":[]}`}
	client, err := ring.NewClient(
		ring.WithHTTPClient(&http.Client{Transport: transport}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	volume := []ring.SetVolumeRequest{
		{DeviceID: "not-a-number", Volume: 4},
		{DeviceID: "100", Volume: -1},
		{DeviceID: "100", Volume: 12},
	}
	for _, req := range volume {
		require.Error(t, client.SetVolume(ctx, req))
	}
	require.Error(t, client.SetLights(ctx, ring.SetLightsRequest{DeviceID: "bad", Enabled: true}))
	require.Error(t, client.SetMotionDetection(ctx, ring.SetMotionDetectionRequest{DeviceID: "bad"}))
	require.Error(t, client.TestSound(ctx, ring.TestSoundRequest{DeviceID: "100", Sound: "alarm"}))
	require.Error(t, client.SetInHomeChime(ctx, ring.SetInHomeChimeRequest{DeviceID: "bad"}))
	_, err = client.UpdateDeviceHealth(ctx, ring.UpdateDeviceHealthRequest{DeviceID: "bad"})
	require.Error(t, err)
	_, err = client.GetDeviceHistory(ctx, ring.GetDeviceHistoryRequest{DeviceID: "bad"})
	require.Error(t, err)
	_, err = client.GetLastRecordingID(ctx, ring.GetLastRecordingIDRequest{DeviceID: "bad"})
	require.Error(t, err)
	_, err = client.GetDeviceSettings(ctx, ring.GetDeviceSettingsRequest{DeviceID: "0"})
	require.Error(t, err)
	require.Error(t, client.PatchDeviceSettings(ctx, ring.PatchDeviceSettingsRequest{DeviceID: "100"}))
	require.Error(t, client.SetSiren(ctx, ring.SetSirenRequest{DeviceID: "-3"}))
	require.Empty(t, transport.requests)
}

func TestCanceledPublicControlDoesNotReachHTTP(t *testing.T) {
	transport := &captureRoundTripper{responseBody: `{}`}
	client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = client.SetVolume(ctx, ring.SetVolumeRequest{DeviceID: "100", Volume: 5})
	require.Error(t, err)
	require.Empty(t, transport.requests)
}
