// Package ring provides the main client for interacting with Ring services.
// Request types for the public client. The wire interface is generated from the schemas.
package ring

import (
	"context"

	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// ClientAPI is the complete public account-level surface implemented by Client.
// A caller that needs only part of it may define a smaller local interface.
// DeviceSession, PlaybackSession, and PushSubscription own their own lifecycles.
type ClientAPI interface {
	Apply(...Option) error
	Close() error

	Authenticate(context.Context, AuthenticateRequest) (*ringapimodels.AuthResponse, error)
	Request2FACode(context.Context, Request2FACodeRequest) error
	RefreshToken(context.Context, RefreshTokenRequest) (*ringapimodels.AuthResponse, error)

	ListDevices(context.Context) (*ringapimodels.DevicesResponse, error)
	GetDevice(context.Context, GetDeviceRequest) (ringapimodels.Device, error)
	UpdateDeviceHealth(context.Context, UpdateDeviceHealthRequest) (*ringapimodels.DeviceHealth, error)
	GetDeviceSettings(context.Context, GetDeviceSettingsRequest) (DeviceSettings, error)
	PatchDeviceSettings(context.Context, PatchDeviceSettingsRequest) error
	GetSnapshot(context.Context, GetSnapshotRequest) (*Snapshot, error)

	SetVolume(context.Context, SetVolumeRequest) error
	SetLights(context.Context, SetLightsRequest) error
	SetMotionDetection(context.Context, SetMotionDetectionRequest) error
	SetSiren(context.Context, SetSirenRequest) error
	TestSound(context.Context, TestSoundRequest) error
	SetInHomeChime(context.Context, SetInHomeChimeRequest) error

	GetDeviceHistory(context.Context, GetDeviceHistoryRequest) (*ringapimodels.RecordingHistoryResponse, error)
	GetActiveDings(context.Context) (*ringapimodels.RecordingHistoryResponse, error)
	GetRecording(context.Context, GetRecordingRequest) (*ringapimodels.VideoStream, error)
	GetRecordingShareURL(context.Context, GetRecordingShareURLRequest) (string, error)
	GetLastRecordingID(context.Context, GetLastRecordingIDRequest) (int64, error)

	OpenSignaling(context.Context, OpenSignalingRequest) (*SignalingConnection, error)
	ConnectEvents(context.Context) (*EventConnection, error)
	Listen(context.Context, ringapimodels.EventCallback) error
}

var _ ClientAPI = (*Client)(nil)

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
