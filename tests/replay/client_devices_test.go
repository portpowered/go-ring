package replay_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

func TestListDevices_Success(t *testing.T) {
	t.Parallel()

	client, mockTransport := newTestClientWithMockTransport()

	defer func() { _ = client.Close() }()

	ctx := newTestContext()
	devices, err := client.ListDevices(
		ctx,
		ring.ListDevicesRequest{Auth: ring.AuthContext{AccessToken: "test_token", HardwareID: ""}},
	)

	require.NoError(t, err)
	require.NotNil(t, devices)

	// Verify request was made correctly
	requests := mockTransport.GetRequests()
	require.Len(t, requests, 1)
	req := requests[0]
	assert.Equal(t, "GET", req.Method)
	assert.Contains(t, req.URL, "/device_info/v3/devices")
	assert.Equal(t, "Bearer test_token", req.Headers.Get("Authorization"))

	assert.NotEmpty(t, devices.Devices)
}

func TestListDevices_Empty(t *testing.T) {
	t.Parallel()

	client, mockTransport := newTestClientWithMockTransport()

	defer func() { _ = client.Close() }()

	// Set up empty devices response
	mockTransport.SetResponseWithBody("GET", "/device_info/v3/devices", http.StatusOK, map[string]interface{}{
		"devices": []interface{}{},
	})

	ctx := newTestContext()
	devices, err := client.ListDevices(
		ctx,
		ring.ListDevicesRequest{Auth: ring.AuthContext{AccessToken: "test_token", HardwareID: ""}},
	)

	require.NoError(t, err)
	require.NotNil(t, devices)
	assert.Empty(t, devices.Devices)
}

func TestGetDevice_Found(t *testing.T) {
	t.Parallel()

	client, _ := newTestClientWithMockTransport()

	defer func() { _ = client.Close() }()

	ctx := newTestContext()

	// First list devices to get a device ID
	devices, err := client.ListDevices(
		ctx,
		ring.ListDevicesRequest{Auth: ring.AuthContext{AccessToken: "test_token", HardwareID: ""}},
	)
	require.NoError(t, err)

	// Get a device (should use the first one from the list)
	if len(devices.Devices) == 0 {
		t.Skip("No devices in fixture to test GetDevice")
	}

	deviceID := devices.Devices[0].ID
	device, err := client.GetDevice(
		ctx,
		ring.GetDeviceRequest{Auth: ring.AuthContext{AccessToken: "test_token", HardwareID: ""}, DeviceID: deviceID},
	)
	require.NoError(t, err)
	require.NotNil(t, device)
	assert.Equal(t, deviceID, device.ID)
}

func TestGetDevice_NotFound(t *testing.T) {
	t.Parallel()

	client, _ := newTestClientWithMockTransport()

	defer func() { _ = client.Close() }()

	ctx := newTestContext()

	device, err := client.GetDevice(
		ctx,
		ring.GetDeviceRequest{Auth: ring.AuthContext{AccessToken: "test_token", HardwareID: ""},
			DeviceID: "999999999",
		},
	)

	assert.Nil(t, device)
	require.Error(t, err)
	assert.True(t, ringapimodels.IsNotFoundError(err))
}

func TestUpdateDeviceHealth_Success(t *testing.T) {
	t.Parallel()

	client, mockTransport := newTestClientWithMockTransport()

	defer func() { _ = client.Close() }()

	ctx := newTestContext()
	deviceID := "987653"

	health, err := client.UpdateDeviceHealth(
		ctx,
		ring.UpdateDeviceHealthRequest{Auth: ring.AuthContext{AccessToken: "test_token", HardwareID: ""},
			DeviceID: deviceID,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, health)

	// Verify request was made correctly
	requests := mockTransport.GetRequests()
	require.Len(t, requests, 1)
	req := requests[0]
	assert.Equal(t, "GET", req.Method)
	assert.Contains(t, req.URL, "/ring_devices/987653/health")
}

func TestUpdateDeviceHealth_NotFound(t *testing.T) {
	t.Parallel()

	client, mockTransport := newTestClientWithMockTransport()

	defer func() { _ = client.Close() }()

	// Set up 404 response
	mockTransport.SetResponseWithBody(
		"GET",
		"/clients_api/ring_devices/999999/health",
		http.StatusNotFound,
		map[string]string{
			"error": "device not found",
		},
	)

	ctx := newTestContext()
	health, err := client.UpdateDeviceHealth(
		ctx,
		ring.UpdateDeviceHealthRequest{Auth: ring.AuthContext{AccessToken: "test_token", HardwareID: ""},
			DeviceID: "999999",
		},
	)

	assert.Nil(t, health)
	require.Error(t, err)
	// The REST client returns HTTPError for 404 status codes
	assert.True(t, ringapimodels.IsHTTPError(err))
	assert.IsType(t, (*ringapimodels.NotFoundError)(nil), err)

	var httpErr *ringapimodels.HTTPError
	if assert.ErrorAs(t, err, &httpErr) {
		assert.Equal(t, 404, httpErr.StatusCode)
	}
}

func TestListDevicesContainsGenericDevices(t *testing.T) {
	t.Parallel()

	client, _ := newTestClientWithMockTransport()

	defer func() { _ = client.Close() }()

	ctx := newTestContext()
	devices, err := client.ListDevices(
		ctx,
		ring.ListDevicesRequest{Auth: ring.AuthContext{AccessToken: "test_token", HardwareID: ""}},
	)
	require.NoError(t, err)

	allDevices := devices.Devices
	// Should have at least some devices from the fixture
	assert.NotEmpty(t, allDevices)
}
