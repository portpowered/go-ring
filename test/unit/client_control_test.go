package unit

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

func TestSetVolume_Success(t *testing.T) {
	client, mockTransport := newTestClientWithToken("test_token")
	defer client.Close()

	ctx := newTestContext()
	deviceID := "987652"

	// Set up success response
	mockTransport.SetResponseWithBody("PUT", "/clients_api/ring_devices/987652/volume", http.StatusOK, map[string]string{
		"status": "ok",
	})

	err := client.SetVolume(ctx, ring.SetVolumeRequest{
		DeviceID: deviceID,
		Volume:   5,
	})
	require.NoError(t, err)

	// Verify request was made correctly
	requests := mockTransport.GetRequests()
	require.Len(t, requests, 1)
	req := requests[0]
	assert.Equal(t, "PUT", req.Method)
	assert.Contains(t, req.URL, "/ring_devices/987652/volume")
	assert.Contains(t, req.BodyString, `"volume":5`)
}

func TestSetVolume_InvalidValue(t *testing.T) {
	client, _ := newTestClientWithToken("test_token")
	defer client.Close()

	ctx := newTestContext()
	deviceID := "987652"

	// Test invalid volume values
	err := client.SetVolume(ctx, ring.SetVolumeRequest{
		DeviceID: deviceID,
		Volume:   -1,
	})
	assert.Error(t, err)
	assert.True(t, ringapimodels.IsBadRequestError(err))

	err = client.SetVolume(ctx, ring.SetVolumeRequest{
		DeviceID: deviceID,
		Volume:   12,
	})
	assert.Error(t, err)
	assert.True(t, ringapimodels.IsBadRequestError(err))
}

func TestSetLights_On(t *testing.T) {
	client, mockTransport := newTestClientWithToken("test_token")
	defer client.Close()

	ctx := newTestContext()
	deviceID := "987652"

	// Set up success response
	mockTransport.SetResponseWithBody("PUT", "/clients_api/ring_devices/987652/lights", http.StatusOK, map[string]string{
		"status": "ok",
	})

	err := client.SetLights(ctx, ring.SetLightsRequest{
		DeviceID: deviceID,
		State:    "on",
		Duration: nil,
	})
	require.NoError(t, err)

	// Verify request
	requests := mockTransport.GetRequests()
	require.Len(t, requests, 1)
	req := requests[0]
	assert.Equal(t, "PUT", req.Method)
	assert.Contains(t, req.URL, "/ring_devices/987652/lights")
	assert.Contains(t, req.BodyString, `"state":"on"`)
}

func TestSetLights_OffWithDuration(t *testing.T) {
	client, mockTransport := newTestClientWithToken("test_token")
	defer client.Close()

	ctx := newTestContext()
	deviceID := "987652"
	duration := 30

	// Set up success response
	mockTransport.SetResponseWithBody("PUT", "/clients_api/ring_devices/987652/lights", http.StatusOK, map[string]string{
		"status": "ok",
	})

	err := client.SetLights(ctx, ring.SetLightsRequest{
		DeviceID: deviceID,
		State:    "off",
		Duration: &duration,
	})
	require.NoError(t, err)

	// Verify request
	requests := mockTransport.GetRequests()
	require.Len(t, requests, 1)
	req := requests[0]
	assert.Contains(t, req.BodyString, `"state":"off"`)
	assert.Contains(t, req.BodyString, `"duration":30`)
}

func TestSetLights_InvalidState(t *testing.T) {
	client, _ := newTestClientWithToken("test_token")
	defer client.Close()

	ctx := newTestContext()
	deviceID := "987652"

	err := client.SetLights(ctx, ring.SetLightsRequest{
		DeviceID: deviceID,
		State:    "invalid",
		Duration: nil,
	})
	assert.Error(t, err)
	assert.True(t, ringapimodels.IsBadRequestError(err))
}

func TestSetMotionDetection_Enable(t *testing.T) {
	client, mockTransport := newTestClientWithToken("test_token")
	defer client.Close()

	ctx := newTestContext()
	deviceID := "987652"

	// Set up success response
	mockTransport.SetResponseWithBody("PUT", "/clients_api/ring_devices/987652/motion_detection", http.StatusOK, map[string]string{
		"status": "ok",
	})

	err := client.SetMotionDetection(ctx, ring.SetMotionDetectionRequest{
		DeviceID: deviceID,
		Enabled:  true,
	})
	require.NoError(t, err)

	// Verify request
	requests := mockTransport.GetRequests()
	require.Len(t, requests, 1)
	req := requests[0]
	assert.Equal(t, "PUT", req.Method)
	assert.Contains(t, req.URL, "/ring_devices/987652/motion_detection")
	assert.Contains(t, req.BodyString, `"enabled":true`)
}

func TestSetMotionDetection_Disable(t *testing.T) {
	client, mockTransport := newTestClientWithToken("test_token")
	defer client.Close()

	ctx := newTestContext()
	deviceID := "987652"

	// Set up success response
	mockTransport.SetResponseWithBody("PUT", "/clients_api/ring_devices/987652/motion_detection", http.StatusOK, map[string]string{
		"status": "ok",
	})

	err := client.SetMotionDetection(ctx, ring.SetMotionDetectionRequest{
		DeviceID: deviceID,
		Enabled:  false,
	})
	require.NoError(t, err)

	// Verify request
	requests := mockTransport.GetRequests()
	require.Len(t, requests, 1)
	req := requests[0]
	assert.Contains(t, req.BodyString, `"enabled":false`)
}

func TestTestSound_Ding(t *testing.T) {
	client, mockTransport := newTestClientWithToken("test_token")
	defer client.Close()

	ctx := newTestContext()
	deviceID := "999999" // Chime device

	// Set up success response
	mockTransport.SetResponseWithBody("POST", "/clients_api/ring_devices/999999/test_sound", http.StatusOK, map[string]string{
		"status": "ok",
	})

	err := client.TestSound(ctx, ring.TestSoundRequest{
		DeviceID: deviceID,
		Kind:     "ding",
	})
	require.NoError(t, err)

	// Verify request
	requests := mockTransport.GetRequests()
	require.Len(t, requests, 1)
	req := requests[0]
	assert.Equal(t, "POST", req.Method)
	assert.Contains(t, req.URL, "/ring_devices/999999/test_sound")
	assert.Contains(t, req.BodyString, `"kind":"ding"`)
}

func TestTestSound_Motion(t *testing.T) {
	client, mockTransport := newTestClientWithToken("test_token")
	defer client.Close()

	ctx := newTestContext()
	deviceID := "999999"

	// Set up success response
	mockTransport.SetResponseWithBody("POST", "/clients_api/ring_devices/999999/test_sound", http.StatusOK, map[string]string{
		"status": "ok",
	})

	err := client.TestSound(ctx, ring.TestSoundRequest{
		DeviceID: deviceID,
		Kind:     "motion",
	})
	require.NoError(t, err)

	// Verify request
	requests := mockTransport.GetRequests()
	require.Len(t, requests, 1)
	req := requests[0]
	assert.Contains(t, req.BodyString, `"kind":"motion"`)
}

func TestTestSound_InvalidKind(t *testing.T) {
	client, _ := newTestClientWithToken("test_token")
	defer client.Close()

	ctx := newTestContext()
	deviceID := "999999"

	err := client.TestSound(ctx, ring.TestSoundRequest{
		DeviceID: deviceID,
		Kind:     "invalid",
	})
	assert.Error(t, err)
	assert.True(t, ringapimodels.IsBadRequestError(err))
}

func TestSetInHomeChime(t *testing.T) {
	client, mockTransport := newTestClientWithToken("test_token")
	defer client.Close()

	ctx := newTestContext()
	deviceID := "987652"

	settings := map[string]interface{}{
		"type":     "Mechanical",
		"enabled":  true,
		"duration": 3,
	}

	// Set up success response
	mockTransport.SetResponseWithBody("PUT", "/clients_api/ring_devices/987652/in_home_chime", http.StatusOK, map[string]string{
		"status": "ok",
	})

	err := client.SetInHomeChime(ctx, ring.SetInHomeChimeRequest{
		DeviceID: deviceID,
		Settings: settings,
	})
	require.NoError(t, err)

	// Verify request
	requests := mockTransport.GetRequests()
	require.Len(t, requests, 1)
	req := requests[0]
	assert.Equal(t, "PUT", req.Method)
	assert.Contains(t, req.URL, "/ring_devices/987652/in_home_chime")
	assert.Contains(t, req.BodyString, `"type":"Mechanical"`)
	assert.Contains(t, req.BodyString, `"enabled":true`)
	assert.Contains(t, req.BodyString, `"duration":3`)
}
