package ringapimodels

import "github.com/portpowered/go-ring/internal/protocol"

const (
	// Ring API base URIs
	RingAPIBaseURI   = protocol.APIBaseURL
	RingOAuthBaseURI = protocol.OAuthBaseURL
	RingOAuthURI     = protocol.OAuthBaseURL + protocol.OAuthTokenPath

	// Ring API endpoints
	RingDevicesEndpoint      = protocol.DevicesPath
	RingDevicesV3Endpoint    = protocol.DevicesV3Path
	RingSessionEndpoint      = protocol.SessionPath
	RingDingsActiveEndpoint  = protocol.DingsActivePath
	RingDingsHistoryEndpoint = protocol.DingsHistoryPath
	RingRecordingEndpoint    = protocol.RecordingPath

	// RTC Streaming endpoints
	RingAppAPIURI                     = protocol.USSolutionsBaseURL
	RingRTCStreamingTicketEndpoint    = protocol.TicketPath
	RingRTCStreamingWebSocketEndpoint = protocol.SignalingURL

	// Default user agent
	DefaultUserAgent = "android:com.ringapp"

	// OAuth constants
	RingClientID = "ring_official_android"
	RingScope    = "client"
)

// DeviceFamily represents the family/type of a Ring device
type DeviceFamily string

const (
	DeviceFamilyDoorbell   DeviceFamily = "doorbots"
	DeviceFamilyChime      DeviceFamily = "chimes"
	DeviceFamilyStickUpCam DeviceFamily = "stickup_cams"
	DeviceFamilyOther      DeviceFamily = "other"
)

// EventKind represents the type of event
type EventKind string

const (
	EventKindMotion   EventKind = "motion"
	EventKindDing     EventKind = "ding"
	EventKindOnDemand EventKind = "on_demand"
)

// SoundKind represents the type of sound for chime testing
type SoundKind string

const (
	SoundKindDing   SoundKind = "ding"
	SoundKindMotion SoundKind = "motion"
)
