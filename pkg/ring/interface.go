// Package ring provides the main client for interacting with Ring services.
// Public interfaces and types for clients, connections, and sessions.
package ring

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/signaling"
	"github.com/portpowered/go-ring/pkg/dependencies/rest"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// Client is the main client for interacting with Ring services
type Client struct {
	restClient                 *rest.Client
	accessToken                string
	refreshToken               string
	username                   string
	password                   string
	hardwareID                 string
	userAgent                  string
	region                     Region
	endpointOverrides          Endpoints
	endpoints                  Endpoints
	signalingWebSocketOverride string
	signalingDialer            WebSocketDialer

	tokenGetter           func(ctx context.Context) (string, error)
	signalingWebSocketURL string
	eventWebSocketURL     string
	mu                    sync.RWMutex
	sessionMu             sync.Mutex
	sessionRegistered     bool
	closed                bool
	signalingConnections  map[*SignalingConnection]struct{}
}

// ClientAPI is the complete public account-level surface implemented by Client.
// A caller that needs only part of it may define a smaller local interface.
// OpenSignaling creates a connection that can start live and playback sessions.
type ClientAPI interface {
	Apply(...Option) error
	Close() error

	// Authentication and authorization
	Authenticate(context.Context, AuthenticateRequest) (*ringapimodels.AuthResponse, error)
	Request2FACode(context.Context, Request2FACodeRequest) error
	RefreshToken(context.Context, RefreshTokenRequest) (*ringapimodels.AuthResponse, error)

	// APIs for enumerating and getting data
	ListDevices(context.Context) (*ringapimodels.DevicesResponse, error)
	GetDevice(context.Context, GetDeviceRequest) (ringapimodels.Device, error)
	GetDeviceSettings(context.Context, GetDeviceSettingsRequest) (DeviceSettings, error)

	// APIs for modifying or sending requests to a device
	UpdateDeviceHealth(context.Context, UpdateDeviceHealthRequest) (*ringapimodels.DeviceHealth, error)
	PatchDeviceSettings(context.Context, PatchDeviceSettingsRequest) error
	GetSnapshot(context.Context, GetSnapshotRequest) (*Snapshot, error)
	SetVolume(context.Context, SetVolumeRequest) error
	SetLights(context.Context, SetLightsRequest) error
	SetMotionDetection(context.Context, SetMotionDetectionRequest) error
	SetSiren(context.Context, SetSirenRequest) error
	TestSound(context.Context, TestSoundRequest) error
	SetInHomeChime(context.Context, SetInHomeChimeRequest) error

	// Various recording APIs
	GetDeviceHistory(context.Context, GetDeviceHistoryRequest) (*ringapimodels.RecordingHistoryResponse, error)
	GetActiveDings(context.Context) (*ringapimodels.RecordingHistoryResponse, error)
	GetRecording(context.Context, GetRecordingRequest) (*ringapimodels.VideoStream, error)
	GetRecordingShareURL(context.Context, GetRecordingShareURLRequest) (string, error)
	GetLastRecordingID(context.Context, GetLastRecordingIDRequest) (int64, error)

	// Event and signaling connections
	OpenSignaling(context.Context, OpenSignalingRequest) (*SignalingConnection, error)
	ConnectEvents(context.Context) (*EventConnection, error)
	Listen(context.Context, ringapimodels.EventCallback) error
}

// SignalingConnection owns one authenticated signaling socket and its child sessions.
type SignalingConnection struct {
	client     *Client
	conn       *websocket.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	closed     bool
	terminal   error
	done       chan struct{}
	readerDone chan struct{}
	writer     *signalingWriter
	pending    map[string]chan signaling.Message
	sessions   map[string]*DeviceSession
	channels   map[string]chan signaling.Message
	playbacks  map[string]*PlaybackSession
}

// SignalingConnectionAPI creates sessions and push subscriptions on one socket.
type SignalingConnectionAPI interface {
	StartDeviceSession(context.Context, StartDeviceSessionRequest) (*DeviceSession, error)
	StartPlayback(context.Context, StartPlaybackRequest) (*PlaybackSession, error)
	SubscribePush(context.Context, []PushFilter) (*PushSubscription, error)
	Err() error
	Close() error
}

// DeviceSession is a live camera session created by SignalingConnection.
type DeviceSession struct {
	connection *SignalingConnection
	core       *signaling.Session
	dialogID   string
	answer     SessionDescription
	offerSDP   string
	started    time.Time
	deviceID   int64
	signalID   string
	riid       string
	iceMode    ICECandidateMode
	mu         sync.Mutex
	closed     bool
	terminal   error
	movement   map[PTZAxis]string
	ready      chan struct{}
	done       chan struct{}
	doneOnce   sync.Once
}

// DeviceSessionAPI is the live camera session surface.
type DeviceSessionAPI interface {
	Answer() SessionDescription
	State() SessionState
	Wait(context.Context) error
	Receive(context.Context) (*SessionEvent, error)
	SendICE(context.Context, ICECandidateRequest) error
	PanStep(context.Context, PanStepRequest) (*PTZResult, error)
	TiltStep(context.Context, TiltStepRequest) (*PTZResult, error)
	PanContinuous(context.Context, PanContinuousRequest) (*PTZResult, error)
	TiltContinuous(context.Context, TiltContinuousRequest) (*PTZResult, error)
	StopPTZ(context.Context, StopPTZRequest) (*PTZResult, error)
	SetMicrophone(context.Context, SetMicrophoneRequest) error
	SetStreamOptions(context.Context, SetStreamOptionsRequest) error
	Close() error
}

// DeviceSessionInterfaceAPI is kept as an alias for existing callers.
type DeviceSessionInterfaceAPI = DeviceSessionAPI

// PlaybackSession is a cloud playback negotiation on a signaling connection.
// Live camera controls remain on DeviceSession.
type PlaybackSession struct {
	connection *SignalingConnection
	dialog     string
	riid       string
	id         string
	deviceID   int64
	answer     SessionDescription
	events     chan signaling.Message
	done       chan struct{}
	once       sync.Once
	lastPong   atomic.Int64
}

// PlaybackSessionAPI is the cloud recording session surface.
type PlaybackSessionAPI interface {
	Answer() SessionDescription
	SendICE(context.Context, ICECandidateRequest) error
	Receive(context.Context) (SessionEvent, error)
	Close() error
}

// PushSubscription receives push notifications on a signaling connection.
type PushSubscription struct {
	connection *SignalingConnection
	dialog     string
	id         string
	events     chan signaling.Message
	done       chan struct{}
	once       sync.Once
}

// PushSubscriptionAPI receives push notifications until closed.
type PushSubscriptionAPI interface {
	Receive(context.Context) (PushEvent, error)
	Close() error
}

// EventConnection represents a WebSocket connection for receiving events.
type EventConnection struct {
	conn        *websocket.Conn
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	mu          sync.RWMutex
	closed      bool
	messageChan chan *ringapimodels.Event
	errChan     chan error
}

// EventConnectionAPI receives account events until closed.
type EventConnectionAPI interface {
	Receive() (*ringapimodels.Event, error)
	Close() error
}

var _ ClientAPI = (*Client)(nil)
var _ SignalingConnectionAPI = (*SignalingConnection)(nil)
var _ DeviceSessionAPI = (*DeviceSession)(nil)
var _ PlaybackSessionAPI = (*PlaybackSession)(nil)
var _ PushSubscriptionAPI = (*PushSubscription)(nil)
var _ EventConnectionAPI = (*EventConnection)(nil)

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

// Client configuration
// Option is a function that configures a Client
type Option interface {
	Apply(c *Client) error
}

// Signaling dialer configuration
// WebSocketDialer is the small portion of Gorilla's dialer used by a signaling connection.
type WebSocketDialer interface {
	DialContext(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)
}
type WithSignalingDialerOption struct{ Dialer WebSocketDialer }

// Regional endpoints
// Region selects a Ring account region. Regional Solutions bootstrap origins
// are not currently verified for EU or FE and must be supplied explicitly.
type Region string

const (
	RegionUS Region = "US"
	RegionEU Region = "EU"
	RegionFE Region = "FE"
)

// Endpoints overrides service origins for a client. Empty fields inherit the
// selected region's verified defaults.
type Endpoints struct {
	OAuthBaseURL     string
	APIBaseURL       string
	SolutionsBaseURL string
	SignalingURL     string
}

// Device settings
// DeviceSettings contains only fields confirmed in captured settings exchanges.
// Fields not represented here remain opaque to callers of the typed API.
type DeviceSettings struct {
	// MotionDetectionEnabled is nil when the response omits or nulls the field.
	MotionDetectionEnabled *bool
}

// GetDeviceSettingsRequest identifies a device whose supported settings are read.
type GetDeviceSettingsRequest struct{ DeviceID string }

// PatchDeviceSettingsRequest changes only explicitly supplied supported fields.
type PatchDeviceSettingsRequest struct {
	DeviceID               string
	MotionDetectionEnabled *bool
}

// SetSirenRequest controls the captured legacy doorbot siren endpoint.
type SetSirenRequest struct {
	DeviceID string
	Enabled  bool
}

// Snapshots
// GetSnapshotRequest controls the bounded Python-legacy freshness poll.
// Zero attempts defaults to three; a zero interval polls without delay.
type GetSnapshotRequest struct {
	DeviceID     string
	MaxAttempts  int
	PollInterval time.Duration
}

// Snapshot contains buffered image bytes and the timestamp returned by the
// legacy polling endpoint. The caller may choose how to persist the bytes.
type Snapshot struct {
	Bytes       []byte
	Timestamp   time.Time
	ContentType string
}

// Signaling connection request
type OpenSignalingRequest struct{}

// Live and playback session values
type SessionState string

var (
	ErrSessionClosed       = signaling.ErrClosed
	ErrSessionExpired      = signaling.ErrExpired
	ErrSessionHeartbeat    = signaling.ErrHeartbeat
	ErrSessionBackpressure = signaling.ErrBackpressure
)

const (
	SessionActive  SessionState = "active"
	SessionClosed  SessionState = "closed"
	SessionExpired SessionState = "expired"
	SessionFailed  SessionState = "failed"
)

// SDPType identifies the role of a session description.
type SDPType uint8

const (
	SDPTypeUnknown SDPType = iota
	SDPTypeOffer
	SDPTypeAnswer
)

type SessionDescription struct {
	Type SDPType `json:"type"`
	SDP  string  `json:"sdp"`
}
type StartDeviceSessionRequest struct {
	DeviceID     string
	Offer        SessionDescription
	AudioEnabled bool
	VideoEnabled bool
	MaxAge       time.Duration
	ICEMode      ICECandidateMode
}
type ICECandidateMode string

const (
	ICETrickle    ICECandidateMode = "trickle"
	ICENonTrickle ICECandidateMode = "non_trickle"
)

type ICECandidateRequest struct {
	Candidate  string
	MID        string
	MLineIndex int
}
type PanDirection string
type TiltDirection string

const (
	PanLeft  PanDirection  = protocol.PanLeft
	PanRight PanDirection  = protocol.PanRight
	TiltUp   TiltDirection = protocol.TiltUp
	TiltDown TiltDirection = protocol.TiltDown
)

type PanStepRequest struct{ Direction PanDirection }
type TiltStepRequest struct{ Direction TiltDirection }
type PanContinuousRequest struct {
	Direction PanDirection
	// Speed is normalized from 0 (stop) to 1 (full requested speed).
	Speed float64
}
type TiltContinuousRequest struct {
	Direction TiltDirection
	// Speed is normalized from 0 (stop) to 1 (full requested speed).
	Speed float64
}
type PTZAxis string

const (
	PanAxis  PTZAxis = "pan"
	TiltAxis PTZAxis = "tilt"
)

type StopPTZRequest struct{ Axis PTZAxis }
type SetMicrophoneRequest struct{ Enabled bool }
type SetStreamOptionsRequest struct {
	AudioEnabled *bool
	VideoEnabled *bool
}
type SessionEvent struct {
	Method string
	Body   json.RawMessage
}

// PTZResult exposes captured acknowledgement fields while Raw retains unknown extensions.
type PTZResult struct {
	SessionID string          `json:"sessionId"`
	Timestamp float64         `json:"timestamp"`
	Version   float64         `json:"version"`
	Raw       json.RawMessage `json:"-"`
}

// RPCError describes a command rejection returned by the signaling service.
type RPCError = signaling.RPCError

// Push notifications
// PushFilter is the captured subscription filter. Identifiers are caller
// supplied so one subscriber can distinguish multiple requested filters.
type PushFilter struct {
	FilterIdentifier  string      `json:"filter_identifier"`
	Filters           PushFilters `json:"filters"`
	NotificationScope string      `json:"notification_scope"`
	NotificationType  string      `json:"notification_type"`
}

type PushFilters struct {
	DoorbotIDs []int64 `json:"doorbot_ids"`
}

type PushEvent struct {
	FilterIdentifiers []string        `json:"filter_identifiers"`
	IngestionTimeMS   int64           `json:"ingestion_time_ms"`
	NotificationScope string          `json:"notification_scope"`
	NotificationType  string          `json:"notification_type"`
	Payload           json.RawMessage `json:"payload"`
	SubscriptionID    string          `json:"subscription_id"`
}

// Playback request
type StartPlaybackRequest struct {
	DeviceID   string
	Offer      SessionDescription
	EntryPoint string
}
