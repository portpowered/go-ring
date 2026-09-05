package ringapimodels

const (
	// Ring API base URIs
	RingAPIBaseURI = "https://api.ring.com"
	RingOAuthURI   = "https://oauth.ring.com/oauth/token"

	// Ring API endpoints
	RingDevicesEndpoint      = "/clients_api/ring_devices"
	RingDingsActiveEndpoint  = "/clients_api/dings/active"
	RingDingsHistoryEndpoint = "/clients_api/dings/history"
	RingRecordingEndpoint    = "/clients_api/dings/{id}/recording"

	// RTC Streaming endpoints
	RingAppAPIURI                     = "https://prd-api-us.prd.rings.solutions"
	RingRTCStreamingTicketEndpoint    = "/api/v1/clap/ticket/request/signalsocket"
	RingRTCStreamingWebSocketEndpoint = "wss://api.prod.signalling.ring.devices.a2z.com:443/ws?api_version=4.0&auth_type=ring_solutions&client_id=ring_site-{client_id}&token={token}"

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
