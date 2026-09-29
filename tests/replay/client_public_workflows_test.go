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

type errorRoundTripper struct{ err error }

func (transport errorRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, transport.err
}

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
	t.Parallel()

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
		devices, listErr := client.ListDevices(
			ctx,
			ring.ListDevicesRequest{Auth: ring.AuthContext{AccessToken: "dynamic-token", HardwareID: "hardware-abc"}},
		)
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
		require.Equal(t, "hardware-abc", req.Header.Get("Hardware_id"))
		require.Equal(t, "application/json", req.Header.Get("Accept"))
		require.Equal(t, "application/json", req.Header.Get("Content-Type"))
	}
}

func TestMissingRequestTokenStopsBeforeHTTP(t *testing.T) {
	t.Parallel()

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

func TestClientPreservesTypedNetworkErrorAndCause(t *testing.T) {
	t.Parallel()

	client, err := ring.NewClient(
		ring.WithHTTPClient(&http.Client{Transport: errorRoundTripper{err: context.Canceled}}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	_, err = client.ListDevices(context.Background(), ring.ListDevicesRequest{
		Auth: ring.AuthContext{AccessToken: "network-error-token", HardwareID: ""},
	})
	require.Error(t, err)
	require.IsType(t, (*ringapimodels.NetworkError)(nil), err, "client should return the typed API error directly")

	var matchedError *ringapimodels.NetworkError

	require.ErrorAs(t, err, &matchedError)
	require.Same(t, matchedError, err)
	require.ErrorIs(t, err, context.Canceled)
}

func TestAuthenticateUsesOnlyRequestCredentials(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		username string
		password string
	}{
		{name: "first account", username: "first-user", password: "first-password"},
		{name: "second account", username: "second-user", password: "second-password"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

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
			require.Equal(t, "hardware-auth", transport.requests[4].Header.Get("Hardware_id"))
			verifyForm, parseErr := url.ParseQuery(transport.bodies[2])
			require.NoError(t, parseErr)
			require.Equal(t, "123456", verifyForm.Get("2fa_code"))
		})
	}
}

func TestRefreshTokenUsesOnlyRequestCredentials(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		refreshToken string
	}{
		{name: "first account", refreshToken: "first-refresh"},
		{name: "second account", refreshToken: "second-refresh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			transport := &captureRoundTripper{
				responseBody: `{"access_token":"new-access","refresh_to` +
					`ken":"rotated-refresh","expires_in":3600` +
					`,"token_type":"Bearer"}`,
			}
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
			require.Equal(t, "hardware-refresh", transport.requests[0].Header.Get("Hardware_id"))
			require.Equal(t, "refresh-test/1", transport.requests[0].Header.Get("User-Agent"))
		})
	}
}

// Mirrors Python test_ring.py's basic/chime/doorbell/shared-doorbell and
// test_other.py::test_other_attributes using values from the existing Go
// fixture. The C1 v3 capture establishes only a camera row, so this fixture
// tests conversion and generic getters, not a vendor v3 family wire contract.
func TestGenericDeviceInventoryPreservesMetadataAcrossKinds(t *testing.T) {
	t.Parallel()

	const body = `{"devices":[
		{"id":101,"kind":"lpd_v1"` +
		`,"family":"doorbots","owned":true,"descr` +
		`iption":"Front Door","name":"","address"` +
		`:"123 Main St","timezone":"America/New_Y` +
		`ork","time_zone":"ignored","volume":1,"h` +
		`as_light":true,"light_brightness":2,"mot` +
		`ion_detection_enabled":true,"health":{"s` +
		`ignal_strength":-58}},
		{"id":102,"kind` +
		`":"lpd_v1","family":"doorbots","owned":` +
		`false,"description":"Shared Door","address"` +
		`:"2 Side St","time_zone":"America/New_` +
		`York"},
		{"id":201,"kind":"chime","fami` +
		`ly":"chimes","name":"Hall Chime","volume` +
		`":2},
		{"id":301,"kind":"hp_cam_v1","fa` +
		`mily":"stickup_cams","description":"Came` +
		`ra","address":"4 Back St","time_zone":"A` +
		`merica/Phoenix"},
		{"id":401,"kind":"in` +
		`tercom_handset_audio","family":"other","` +
		`description":"Lobby Intercom","time_zone` +
		`":"Europe/Rome"},
		{"id":501,"kind":"fu` +
		`ture_device_kind","family":"future_famil` +
		`y","description":"Unknown"}
	]}`

	transport := &captureRoundTripper{responseBody: body}
	client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	devices, err := client.ListDevices(
		context.Background(),
		ring.ListDevicesRequest{Auth: ring.AuthContext{AccessToken: "inventory-token", HardwareID: ""}},
	)
	require.NoError(t, err)

	all := devices.Devices
	require.Len(t, all, 6)

	for _, device := range all {
		require.NotEmpty(t, device.ID)
		require.NotEmpty(t, device.Kind)
		// Missing optional text values normalize to the empty string, not panic.
		_ = device.Name
		_ = device.Address
		_ = device.Timezone
	}

	require.Equal(t, "Front Door", all[0].Name, "description is used if name is empty")
	require.Equal(t, "America/New_York", all[0].Timezone, "primary timezone wins")
	require.Equal(t, "Shared Door", all[1].Name)
	require.Empty(t, all[2].Address)
	require.Empty(t, all[2].Timezone)
	require.Equal(t, string(ringapimodels.DeviceFamilyChime), all[2].Family)
	require.Equal(t, "Camera", all[3].Name)
	require.Equal(t, "America/Phoenix", all[3].Timezone)
	require.Equal(t, "Lobby Intercom", all[4].Name)
	require.Equal(t, string(ringapimodels.DeviceFamilyOther), all[4].Family)
	require.Equal(t, "future_device_kind", all[5].Kind)
	require.Equal(t, "future_family", all[5].Family)
	require.Equal(t, "Unknown", all[5].Name)
	require.Empty(t, all[5].Address)
	require.Empty(t, all[5].Timezone)
	require.True(t, all[0].Supports(ringapimodels.DeviceCapabilityLight))
	require.True(t, all[0].Supports(ringapimodels.DeviceCapabilityMotionDetection))
	require.Empty(t, all[5].Capabilities, "unrecognized hardware does not gain inferred capabilities")
	require.False(t, all[5].Supports(ringapimodels.DeviceCapabilityLight))

	if health := all[0].Health; health != nil {
		require.NotNil(t, health.SignalStrength)
		require.Equal(t, -58, *health.SignalStrength)
	} else {
		t.Fatal("captured-style health metadata was dropped")
	}
}

func TestGetDeviceSettingsDistinguishesUnknownFromCapturedFalse(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "omitted", body: `{"motion_settings":{}}`},
		{name: "null", body: `{"motion_settings":{"motion_detection_enabled":null}}`},
		{name: "explicit false", body: `{"motion_settings":{"motion_detection_enabled":false}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			transport := &captureRoundTripper{responseBody: tc.body}
			client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}))
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })

			settings, err := client.GetDeviceSettings(
				context.Background(),
				ring.GetDeviceSettingsRequest{
					Auth:     ring.AuthContext{AccessToken: "settings-token", HardwareID: ""},
					DeviceID: "101",
				},
			)
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
	t.Parallel()

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
		{
			name:               "explicit option wins",
			token:              "header." + validPayload + ".signature",
			explicitHardwareID: "explicit-hardware",
			wantHardwareID:     "explicit-hardware",
		},
		{name: "missing claim", token: "header." + missingPayload + ".signature"},
		{name: "bad base64 claim", token: "header.%%%.signature"},
		{name: "bad JSON claim", token: "header." + malformedJSON + ".signature"},
		{name: "malformed token", token: "not-a-jwt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			transport := &captureRoundTripper{responseBody: `{"devices":[]}`}
			options := []ring.Option{
				ring.WithHTTPClient(&http.Client{Transport: transport}),
				ring.WithEndpoints(ring.Endpoints{APIBaseURL: "https://api.token-claims.example"}),
			}
			client, err := ring.NewClient(options...)
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })

			_, err = client.ListDevices(
				context.Background(),
				ring.ListDevicesRequest{
					Auth: ring.AuthContext{AccessToken: tc.token, HardwareID: tc.explicitHardwareID},
				},
			)
			require.NoError(t, err)

			if tc.wantHardwareID == "" {
				require.Len(t, transport.requests, 1, "without a claim there is no hardware session registration")
				require.Equal(t, http.MethodGet, transport.requests[0].Method)

				return
			}

			require.Len(t, transport.requests, 2)
			require.Equal(t, http.MethodPost, transport.requests[0].Method)
			require.Equal(t, "/clients_api/session", transport.requests[0].URL.Path)
			require.Equal(t, tc.wantHardwareID, transport.requests[0].Header.Get("Hardware_id"))
			require.Equal(t, "Bearer "+tc.token, transport.requests[0].Header.Get("Authorization"))
		})
	}
}

// Input and cancellation guards are synthetic robustness tests, not captured
// Ring errors. They ensure malformed public requests never reach HTTP transport.
func TestInvalidPublicDeviceRequestsDoNotReachHTTP(t *testing.T) {
	t.Parallel()

	transport := &captureRoundTripper{responseBody: `{"devices":[]}`}
	client, err := ring.NewClient(
		ring.WithHTTPClient(&http.Client{Transport: transport}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()

	volume := []ring.SetVolumeRequest{
		{DeviceID: "not-a-number", Kind: ringapimodels.VolumeKindDoorbell, Description: "Fixture Device", Volume: 4},
		{DeviceID: "100", Kind: ringapimodels.VolumeKindDoorbell, Description: "Fixture Device", Volume: -1},
		{DeviceID: "100", Kind: ringapimodels.VolumeKindDoorbell, Description: "Fixture Device", Volume: 12},
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
	t.Parallel()

	transport := &captureRoundTripper{responseBody: `{}`}
	client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = client.SetVolume(
		ctx,
		ring.SetVolumeRequest{
			DeviceID:    "100",
			Kind:        ringapimodels.VolumeKindDoorbell,
			Description: "Fixture Device",
			Volume:      5,
		},
	)
	require.Error(t, err)
	require.Empty(t, transport.requests)
}
