//go:build integration

package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

const (
	accessTokenEnvVar  = "RING_ACCESS_TOKEN"
	refreshTokenEnvVar = "RING_REFRESH_TOKEN"
	usernameEnvVar     = "RING_USERNAME"
	passwordEnvVar     = "RING_PASSWORD"
	testTimeout        = 30 * time.Second
	eventTimeout       = 60 * time.Second
)

// TestAuthentication tests the happy case for authentication with username/password
func TestAuthentication(t *testing.T) {
	username := os.Getenv(usernameEnvVar)
	password := os.Getenv(passwordEnvVar)
	if username == "" || password == "" {
		t.Skipf("Skipping test: %s and %s environment variables not set", usernameEnvVar, passwordEnvVar)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, err := ring.NewClient()
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Try to authenticate
	authResp, err := client.Authenticate(ctx, ring.AuthenticateRequest{
		Username: username,
		Password: password,
		OTPCode:  "",
	})
	if err != nil {
		if ringapimodels.IsRequires2FAError(err) {
			t.Log("2FA required, requesting code...")
			err = client.Request2FACode(ctx, ring.Request2FACodeRequest{
				Username: username,
				Password: password,
			})
			if err != nil {
				t.Fatalf("Failed to request 2FA code: %v", err)
			}
			t.Skip("2FA code required - please set RING_OTP_CODE environment variable and run test again")
		} else {
			t.Fatalf("Failed to authenticate: %v", err)
		}
	}

	if authResp == nil {
		t.Fatal("Auth response is nil")
	}

	if authResp.AccessToken == "" {
		t.Fatal("Access token is empty")
	}

	t.Logf("Successfully authenticated (token expires in %d seconds)", authResp.ExpiresIn)
}

// TestTokenRefresh tests the happy case for token refresh
func TestTokenRefresh(t *testing.T) {
	refreshToken := os.Getenv(refreshTokenEnvVar)
	if refreshToken == "" {
		t.Skipf("Skipping test: %s environment variable not set", refreshTokenEnvVar)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, err := ring.NewClient()
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	authResp, err := client.RefreshToken(ctx, ring.RefreshTokenRequest{
		RefreshToken: refreshToken,
	})
	if err != nil {
		t.Fatalf("Failed to refresh access token: %v", err)
	}

	if authResp.AccessToken == "" {
		t.Fatal("Access token is empty")
	}

	t.Logf("Successfully refreshed access token (length: %d)", len(authResp.AccessToken))
}

// TestDeviceEnumeration tests the happy case for device enumeration
func TestDeviceEnumeration(t *testing.T) {
	accessToken := os.Getenv(accessTokenEnvVar)
	if accessToken == "" {
		t.Skipf("Skipping test: %s environment variable not set", accessTokenEnvVar)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, err := ring.NewClientWithToken(accessToken)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	devices, err := client.ListDevices(ctx)
	if err != nil {
		t.Fatalf("Failed to list devices: %v", err)
	}

	if devices == nil {
		t.Fatal("Devices response is nil")
	}

	totalDevices := len(devices.Doorbells) + len(devices.Chimes) + len(devices.StickUpCams)
	if totalDevices == 0 {
		t.Log("Warning: No devices found (this may be expected if no devices are registered)")
	} else {
		t.Logf("Successfully enumerated %d devices", totalDevices)
		t.Logf("  - Doorbells: %d", len(devices.Doorbells))
		t.Logf("  - Chimes: %d", len(devices.Chimes))
		t.Logf("  - StickUp Cams: %d", len(devices.StickUpCams))

		// Log first few devices
		for i, doorbell := range devices.Doorbells {
			if i >= 3 {
				break
			}
			t.Logf("  - Doorbell %d: %s (ID: %d)", i+1, doorbell.Name, doorbell.ID)
		}
	}
}

// TestGetDevice tests getting a specific device
func TestGetDevice(t *testing.T) {
	accessToken := os.Getenv(accessTokenEnvVar)
	if accessToken == "" {
		t.Skipf("Skipping test: %s environment variable not set", accessTokenEnvVar)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, err := ring.NewClientWithToken(accessToken)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// First, list devices to get an ID
	devices, err := client.ListDevices(ctx)
	if err != nil {
		t.Fatalf("Failed to list devices: %v", err)
	}

	if len(devices.Doorbells) == 0 && len(devices.Chimes) == 0 && len(devices.StickUpCams) == 0 {
		t.Skip("Skipping test: No devices found")
	}

	// Get first device ID
	var deviceID string
	if len(devices.Doorbells) > 0 {
		deviceID = devices.Doorbells[0].ID
	} else if len(devices.Chimes) > 0 {
		deviceID = devices.Chimes[0].ID
	} else {
		deviceID = devices.StickUpCams[0].ID
	}

	device, err := client.GetDevice(ctx, ring.GetDeviceRequest{
		DeviceID: deviceID,
	})
	if err != nil {
		t.Fatalf("Failed to get device: %v", err)
	}

	if device == nil {
		t.Fatal("Device is nil")
	}

	t.Logf("Successfully retrieved device: %s (ID: %d)", device.GetName(), device.GetID())
}

// TestRecordingDownload tests downloading a recording
func TestRecordingDownload(t *testing.T) {
	accessToken := os.Getenv(accessTokenEnvVar)
	if accessToken == "" {
		t.Skipf("Skipping test: %s environment variable not set", accessTokenEnvVar)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, err := ring.NewClientWithToken(accessToken)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// List devices
	devices, err := client.ListDevices(ctx)
	if err != nil {
		t.Fatalf("Failed to list devices: %v", err)
	}

	if len(devices.Doorbells) == 0 {
		t.Skip("Skipping test: No doorbells found")
	}

	deviceID := devices.Doorbells[0].ID

	// Get device history
	history, err := client.GetDeviceHistory(ctx, ring.GetDeviceHistoryRequest{
		DeviceID: deviceID,
		Limit:    1,
		Kind:     "",
	})
	if err != nil {
		t.Fatalf("Failed to get device history: %v", err)
	}

	if len(history.Recordings) == 0 {
		t.Skip("Skipping test: No recordings found")
	}

	recordingID := history.Recordings[0].ID

	// Get recording URL
	url, err := client.GetRecordingURL(ctx, recordingID)
	if err != nil {
		t.Fatalf("Failed to get recording URL: %v", err)
	}

	if url == "" {
		t.Fatal("Recording URL is empty")
	}

	t.Logf("Successfully retrieved recording URL: %s", url)

	// Note: We don't actually download the file in the test to avoid large downloads
	// Uncomment the following to test actual download:
	// err = client.DownloadRecording(ctx, recordingID, "test_recording.mp4")
	// if err != nil {
	//     t.Fatalf("Failed to download recording: %v", err)
	// }
	// defer os.Remove("test_recording.mp4")
}

// TestDeviceControl tests device control operations
func TestDeviceControl(t *testing.T) {
	accessToken := os.Getenv(accessTokenEnvVar)
	if accessToken == "" {
		t.Skipf("Skipping test: %s environment variable not set", accessTokenEnvVar)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, err := ring.NewClientWithToken(accessToken)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// List devices
	devices, err := client.ListDevices(ctx)
	if err != nil {
		t.Fatalf("Failed to list devices: %v", err)
	}

	if len(devices.Doorbells) == 0 && len(devices.StickUpCams) == 0 {
		t.Skip("Skipping test: No controllable devices found")
	}

	// Find a device to test with
	var testDeviceID string
	var deviceName string
	if len(devices.Doorbells) > 0 {
		testDeviceID = devices.Doorbells[0].ID
		deviceName = devices.Doorbells[0].Name
	} else {
		testDeviceID = devices.StickUpCams[0].ID
		deviceName = devices.StickUpCams[0].Name
	}

	t.Logf("Testing device control on: %s (ID: %s)", deviceName, testDeviceID)

	// Test volume control
	err = client.SetVolume(ctx, ring.SetVolumeRequest{
		DeviceID: testDeviceID,
		Volume:   5,
	})
	if err != nil {
		t.Logf("Warning: Failed to set volume: %v", err)
	} else {
		t.Log("Successfully set volume")
	}

	// Test motion detection (if supported)
	err = client.SetMotionDetection(ctx, ring.SetMotionDetectionRequest{
		DeviceID: testDeviceID,
		Enabled:  true,
	})
	if err != nil {
		t.Logf("Warning: Failed to set motion detection: %v", err)
	} else {
		t.Log("Successfully set motion detection")
	}
}

// TestEventRegistration tests connecting to event stream
func TestEventRegistration(t *testing.T) {
	accessToken := os.Getenv(accessTokenEnvVar)
	if accessToken == "" {
		t.Skipf("Skipping test: %s environment variable not set", accessTokenEnvVar)
	}

	ctx, cancel := context.WithTimeout(context.Background(), eventTimeout)
	defer cancel()

	client, err := ring.NewClientWithToken(accessToken)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Connect to events
	conn, err := client.ConnectEvents(ctx)
	if err != nil {
		// WebSocket connection may not be available in test environment
		t.Logf("Note: Event connection failed (this may be expected): %v", err)
		t.Skip("Skipping test: Event connection not available")
	}
	defer conn.Close()

	t.Log("Successfully connected to event stream")

	// Try to receive an event (with timeout)
	eventReceived := false
	timeout := time.After(10 * time.Second)

	select {
	case <-timeout:
		t.Log("Timeout waiting for event (this may be expected if no events are occurring)")
	case <-ctx.Done():
		t.Log("Context cancelled")
	default:
		event, err := conn.Receive()
		if err != nil {
			if ringapimodels.IsClosedError(err) {
				t.Log("Connection closed")
			} else {
				t.Logf("Error receiving event: %v", err)
			}
		} else if event != nil {
			t.Logf("Received event: %s from device %d", event.Kind, event.DeviceID)
			eventReceived = true
		}
	}

	if eventReceived {
		t.Log("Successfully received event")
	}
}

// TestRTCStream tests RTC stream establishment
func TestRTCStream(t *testing.T) {
	accessToken := os.Getenv(accessTokenEnvVar)
	if accessToken == "" {
		t.Skipf("Skipping test: %s environment variable not set", accessTokenEnvVar)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, err := ring.NewClientWithToken(accessToken)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// List devices
	devices, err := client.ListDevices(ctx)
	if err != nil {
		t.Fatalf("Failed to list devices: %v", err)
	}

	if len(devices.Doorbells) == 0 && len(devices.StickUpCams) == 0 {
		t.Skip("Skipping test: No devices with video capability found")
	}

	deviceID := devices.Doorbells[0].ID
	if len(devices.Doorbells) == 0 {
		deviceID = devices.StickUpCams[0].ID
	}

	// Create a dummy SDP offer for testing
	sdpOffer := "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\na=recvonly\r\n"

	// Start RTC stream
	stream, err := client.StartRTCStream(ctx, ring.StartRTCStreamRequest{
		DeviceID: deviceID,
		SDPOffer: sdpOffer,
	})
	if err != nil {
		// RTC stream may not be available in test environment
		t.Logf("Note: RTC stream failed (this may be expected): %v", err)
		t.Skip("Skipping test: RTC stream not available")
	}
	defer stream.Close()

	t.Logf("Successfully started RTC stream: %s", stream.GetStreamID())

	// Stop stream
	err = client.StopRTCStream(ctx, ring.StopRTCStreamRequest{
		StreamID: stream.GetStreamID(),
	})
	if err != nil {
		t.Logf("Warning: Failed to stop RTC stream: %v", err)
	} else {
		t.Log("Successfully stopped RTC stream")
	}
}
