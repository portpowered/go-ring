// Package ring provides the main client for interacting with Ring services.
// This file defines the Client interface for dependency injection and testing.
package ring

import (
	"context"

	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// AuthenticateRequest contains parameters for Authenticate
type AuthenticateRequest struct {
	Username string
	Password string
	OTPCode  string
}

// Request2FACodeRequest contains parameters for Request2FACode
type Request2FACodeRequest struct {
	Username string
	Password string
}

// RefreshTokenRequest contains parameters for RefreshToken
type RefreshTokenRequest struct {
	RefreshToken string
}

// GetDeviceRequest contains parameters for GetDevice
type GetDeviceRequest struct {
	DeviceID string
}

// UpdateDeviceHealthRequest contains parameters for UpdateDeviceHealth
type UpdateDeviceHealthRequest struct {
	DeviceID string
}

// SetVolumeRequest contains parameters for SetVolume
type SetVolumeRequest struct {
	DeviceID string
	Volume   int
}

// SetLightsRequest contains parameters for SetLights
type SetLightsRequest struct {
	DeviceID string
	State    string
	Duration *int
}

// SetMotionDetectionRequest contains parameters for SetMotionDetection
type SetMotionDetectionRequest struct {
	DeviceID string
	Enabled  bool
}

// TestSoundRequest contains parameters for TestSound
type TestSoundRequest struct {
	DeviceID string
	Kind     string
}

// SetInHomeChimeRequest contains parameters for SetInHomeChime
type SetInHomeChimeRequest struct {
	DeviceID string
	Settings map[string]interface{}
}

// GetDeviceHistoryRequest contains parameters for GetDeviceHistory
type GetDeviceHistoryRequest struct {
	DeviceID string
	Limit    int
	Kind     string
}

// GetRecordingRequest contains parameters for GetRecording
type GetRecordingRequest struct {
	RecordingID int64
}

// GetLastRecordingIDRequest contains parameters for GetLastRecordingID
type GetLastRecordingIDRequest struct {
	DeviceID string
}

// StartRTCStreamRequest contains parameters for StartRTCStream
type StartRTCStreamRequest struct {
	DeviceID string
	SDPOffer string
}

// StopRTCStreamRequest contains parameters for StopRTCStream
type StopRTCStreamRequest struct {
	StreamID string
}

// ClientInterface defines the interface for the Ring client.
type ClientInterface interface {
	// Authenticate performs full authentication flow with username/password
	Authenticate(ctx context.Context, req AuthenticateRequest) (*ringapimodels.AuthResponse, error)

	// Request2FACode requests a 2FA code by attempting authentication
	Request2FACode(ctx context.Context, req Request2FACodeRequest) error

	// RefreshToken refreshes an access token using a refresh token
	RefreshToken(ctx context.Context, req RefreshTokenRequest) (*ringapimodels.AuthResponse, error)

	// ListDevices retrieves all devices associated with the account
	ListDevices(ctx context.Context) (*ringapimodels.DevicesResponse, error)

	// GetDevice retrieves a specific device by ID
	GetDevice(ctx context.Context, req GetDeviceRequest) (ringapimodels.Device, error)

	// UpdateDeviceHealth refreshes health data for a device
	UpdateDeviceHealth(ctx context.Context, req UpdateDeviceHealthRequest) (*ringapimodels.DeviceHealth, error)

	// SetVolume sets the volume for a device
	SetVolume(ctx context.Context, req SetVolumeRequest) error

	// SetLights sets the lights for a device (floodlight cams)
	SetLights(ctx context.Context, req SetLightsRequest) error

	// SetMotionDetection sets motion detection for a device
	SetMotionDetection(ctx context.Context, req SetMotionDetectionRequest) error

	// TestSound tests a sound on a chime device
	TestSound(ctx context.Context, req TestSoundRequest) error

	// SetInHomeChime sets in-home chime settings for a doorbell
	SetInHomeChime(ctx context.Context, req SetInHomeChimeRequest) error

	// ConnectEvents establishes a WebSocket connection for receiving events
	ConnectEvents(ctx context.Context) (*EventConnection, error)

	// Listen listens for events and calls the callback for each event
	Listen(ctx context.Context, callback ringapimodels.EventCallback) error

	// GetDeviceHistory retrieves the history of recordings for a device
	GetDeviceHistory(ctx context.Context, req GetDeviceHistoryRequest) (*ringapimodels.RecordingHistoryResponse, error)

	// GetActiveDings retrieves currently active dings
	GetActiveDings(ctx context.Context) (*ringapimodels.RecordingHistoryResponse, error)

	// GetRecording retrieves a video stream for a recording
	GetRecording(ctx context.Context, req GetRecordingRequest) (*ringapimodels.VideoStream, error)

	// GetLastRecordingID retrieves the ID of the most recent recording for a device
	GetLastRecordingID(ctx context.Context, req GetLastRecordingIDRequest) (int64, error)

	// StartRTCStream starts a WebRTC stream for live video from a device
	// req.SDPOffer is the SDP offer from the caller
	StartRTCStream(ctx context.Context, req StartRTCStreamRequest) (*RTCStream, error)

	// StopRTCStream stops an active RTC stream
	StopRTCStream(ctx context.Context, req StopRTCStreamRequest) error

	// Close closes the client and all connections
	Close() error
}

// Ensure Client implements ClientInterface at compile time
var _ ClientInterface = (*Client)(nil)
