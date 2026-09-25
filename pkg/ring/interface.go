// Package ring provides the main client for interacting with Ring services.
// Request types for the public client. The wire interface is generated from the schemas.
package ring

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
	// Kind is "chime" or "doorbell"; Description is the current device name.
	Kind        string
	Description string
	Volume      int
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
	DeviceID    string
	Description string
	Settings    map[string]interface{}
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

type GetRecordingShareURLRequest struct{ RecordingID int64 }

// GetLastRecordingIDRequest contains parameters for GetLastRecordingID
type GetLastRecordingIDRequest struct {
	DeviceID string
}
