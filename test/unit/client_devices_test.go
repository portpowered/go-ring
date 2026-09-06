package unit

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

func TestListDevices_Success(t *testing.T) {
	client, mockTransport := newTestClientWithToken("test_token")
	defer client.Close()

	ctx := newTestContext()
	devices, err := client.ListDevices(ctx)

	require.NoError(t, err)
	require.NotNil(t, devices)

	// Verify request was made correctly
	requests := mockTransport.GetRequests()
	require.Len(t, requests, 1)
	req := requests[0]
	assert.Equal(t, "GET", req.Method)
	assert.Contains(t, req.URL, "/device_info/v3/devices")
	assert.Equal(t, "Bearer test_token", req.Headers.Get("Authorization"))

	// Verify devices were parsed (check if we have any devices)
	// The fixture should have doorbells (including authorized doorbells merged in), chimes, stickup cams, and other devices
	assert.NotNil(t, devices.Doorbells)
	assert.NotNil(t, devices.Chimes)
	assert.NotNil(t, devices.StickUpCams)
	assert.NotNil(t, devices.Other)
}

func TestListDevices_Empty(t *testing.T) {
	client, mockTransport := newTestClientWithToken("test_token")
	defer client.Close()

	// Set up empty devices response
	mockTransport.SetResponseWithBody("GET", "/device_info/v3/devices", http.StatusOK, map[string]interface{}{
		"devices": []interface{}{},
	})

	ctx := newTestContext()
	devices, err := client.ListDevices(ctx)

	require.NoError(t, err)
	require.NotNil(t, devices)
	assert.Len(t, devices.Doorbells, 0)
	assert.Len(t, devices.Chimes, 0)
	assert.Len(t, devices.StickUpCams, 0)
	assert.Len(t, devices.Other, 0)
}

func TestGetDevice_Found(t *testing.T) {
	client, _ := newTestClientWithToken("test_token")
	defer client.Close()

	ctx := newTestContext()

	// First list devices to get a device ID
	devices, err := client.ListDevices(ctx)
	require.NoError(t, err)

	// Get a device (should use the first one from the list)
	if len(devices.Doorbells) > 0 {
		deviceID := devices.Doorbells[0].ID
		device, err := client.GetDevice(ctx, ring.GetDeviceRequest{
			DeviceID: deviceID,
		})
		require.NoError(t, err)
		require.NotNil(t, device)
		assert.Equal(t, deviceID, device.GetID())
	} else if len(devices.Chimes) > 0 {
		deviceID := devices.Chimes[0].ID
		device, err := client.GetDevice(ctx, ring.GetDeviceRequest{
			DeviceID: deviceID,
		})
		require.NoError(t, err)
		require.NotNil(t, device)
		assert.Equal(t, deviceID, device.GetID())
	} else if len(devices.StickUpCams) > 0 {
		deviceID := devices.StickUpCams[0].ID
		device, err := client.GetDevice(ctx, ring.GetDeviceRequest{
			DeviceID: deviceID,
		})
		require.NoError(t, err)
		require.NotNil(t, device)
		assert.Equal(t, deviceID, device.GetID())
	} else if len(devices.Other) > 0 {
		deviceID := devices.Other[0].ID
		device, err := client.GetDevice(ctx, ring.GetDeviceRequest{
			DeviceID: deviceID,
		})
		require.NoError(t, err)
		require.NotNil(t, device)
		assert.Equal(t, deviceID, device.GetID())
	} else {
		t.Skip("No devices in fixture to test GetDevice")
	}
}

func TestGetDevice_NotFound(t *testing.T) {
	client, _ := newTestClientWithToken("test_token")
	defer client.Close()

	ctx := newTestContext()

	device, err := client.GetDevice(ctx, ring.GetDeviceRequest{
		DeviceID: "999999999",
	})

	assert.Nil(t, device)
	assert.Error(t, err)
	assert.True(t, ringapimodels.IsNotFoundError(err))
}

func TestUpdateDeviceHealth_Success(t *testing.T) {
	client, mockTransport := newTestClientWithToken("test_token")
	defer client.Close()

	ctx := newTestContext()
	deviceID := "987653"

	health, err := client.UpdateDeviceHealth(ctx, ring.UpdateDeviceHealthRequest{
		DeviceID: deviceID,
	})
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
	client, mockTransport := newTestClientWithToken("test_token")
	defer client.Close()

	// Set up 404 response
	mockTransport.SetResponseWithBody("GET", "/clients_api/ring_devices/999999/health", http.StatusNotFound, map[string]string{
		"error": "device not found",
	})

	ctx := newTestContext()
	health, err := client.UpdateDeviceHealth(ctx, ring.UpdateDeviceHealthRequest{
		DeviceID: "999999",
	})

	assert.Nil(t, health)
	assert.Error(t, err)
	// The REST client returns HTTPError for 404 status codes
	assert.True(t, ringapimodels.IsHTTPError(err))
	if httpErr, ok := err.(*ringapimodels.HTTPError); ok {
		assert.Equal(t, 404, httpErr.StatusCode)
	}
}

func TestGetAllDevices(t *testing.T) {
	client, _ := newTestClientWithToken("test_token")
	defer client.Close()

	ctx := newTestContext()
	devices, err := client.ListDevices(ctx)
	require.NoError(t, err)

	allDevices := devices.GetAllDevices()
	assert.NotNil(t, allDevices)
	// Should have at least some devices from the fixture
	assert.Greater(t, len(allDevices), 0)
}
