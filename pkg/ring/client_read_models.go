package ring

import (
	"encoding/json"
	"math"
	"strconv"
	"time"

	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// DeviceDetail is the stable, typed subset of the captured device detail.
// Unobserved vendor fields remain internal to the wire transport.
type DeviceDetail struct {
	Device        DeviceDetailDevice  `json:"device"`
	OperationSets map[string][]string `json:"-"`
}

// DeviceKind is an open hardware kind; this PTZ kind was observed in capture.
type DeviceKind string

const DeviceKindStickUpMiniPTZ DeviceKind = "stickup_cam_mini_ptz_v1"

type DeviceDetailDevice struct {
	ID                     int64                 `json:"id"`
	DeviceID               *string               `json:"device_id,omitempty"`
	Description            string                `json:"description"`
	Kind                   DeviceKind            `json:"kind"`
	Family                 *DeviceFamily         `json:"family,omitempty"`
	CreatedAt              *time.Time            `json:"created_at,omitempty"`
	DeactivatedAt          *time.Time            `json:"deactivated_at,omitempty"`
	Name                   *string               `json:"name,omitempty"`
	Address                *string               `json:"address,omitempty"`
	LocationID             *string               `json:"location_id,omitempty"`
	OperationSet           *string               `json:"operation_set,omitempty"`
	Owner                  *DeviceOwner          `json:"owner,omitempty"`
	Settings               *DeviceLegacySettings `json:"settings,omitempty"`
	Features               *DeviceFeatures       `json:"features,omitempty"`
	TimeZone               *string               `json:"time_zone,omitempty"`
	Timezone               *string               `json:"timezone,omitempty"`
	WifiName               *string               `json:"wifi_name,omitempty"`
	WifiSignalStrength     *int                  `json:"wifi_signal_strength,omitempty"`
	Owned                  *bool                 `json:"owned,omitempty"`
	MotionDetectionEnabled *bool                 `json:"motion_detection_enabled,omitempty"`
	HasLight               *bool                 `json:"has_light,omitempty"`
	LightBrightness        *int                  `json:"light_brightness,omitempty"`
	Volume                 *int                  `json:"volume,omitempty"`
	Health                 *DeviceDetailHealth   `json:"health,omitempty"`
	Alerts                 *DeviceAlerts         `json:"alerts,omitempty"`
	BatteryLife            *BatteryReading       `json:"battery_life,omitempty"`
	BatteryLife2           *BatteryReading       `json:"battery_life_2,omitempty"`
	ExternalConnection     *bool                 `json:"external_connection,omitempty"`
}

// BatteryReading accepts the numeric and numeric-text percentages Ring returns.
type BatteryReading float64

func (b *BatteryReading) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}

	var value json.Number
	{
		err := json.Unmarshal(data, &value)
		if err != nil {
			var text string

			err = json.Unmarshal(data, &text)
			if err != nil {
				return err
			}

			value = json.Number(text)
		}
	}

	percentage, err := strconv.ParseFloat(value.String(), 64)
	if err != nil {
		return err
	}

	*b = BatteryReading(percentage)

	return nil
}

type DeviceAlerts struct {
	Connection *ConnectionState `json:"connection,omitempty"`
}

// ConnectionState is open to new server-provided states.
type ConnectionState string

const (
	ConnectionOnline  ConnectionState = "online"
	ConnectionOffline ConnectionState = "offline"
)

// PowerMode is open to new device power configurations.
type PowerMode string

const PowerModeWired PowerMode = "wired"

// DeviceStatus derives a concise health view without implying a battery on wired devices.
type DeviceStatus struct {
	BatteryPercent *float64         `json:"battery_percent,omitempty"`
	Connection     *ConnectionState `json:"connection,omitempty"`
	IsOffline      bool             `json:"is_offline"`
	PowerMode      *PowerMode       `json:"power_mode,omitempty"`
}

func (d DeviceDetailDevice) Status() DeviceStatus {
	status := DeviceStatus{
		BatteryPercent: nil,
		Connection:     nil,
		IsOffline:      false,
		PowerMode:      nil,
	}
	if d.Settings != nil {
		status.PowerMode = d.Settings.PowerMode
	}

	if d.Alerts != nil {
		status.Connection = d.Alerts.Connection
		status.IsOffline = d.Alerts.Connection != nil && *d.Alerts.Connection == ConnectionOffline
	}

	if status.Connection == nil && d.Health != nil && d.Health.Connected != nil {
		state := ConnectionOffline
		if *d.Health.Connected {
			state = ConnectionOnline
		}

		status.Connection = &state
		status.IsOffline = state == ConnectionOffline
	}

	if d.Health != nil && d.Health.BatteryPresent != nil && !*d.Health.BatteryPresent {
		return status
	}

	if status.PowerMode != nil && *status.PowerMode == PowerModeWired &&
		(d.Health == nil || d.Health.BatteryPresent == nil || !*d.Health.BatteryPresent) {
		return status
	}

	if d.Health != nil {
		switch {
		case d.Health.BatteryPercentage != nil:
			status.BatteryPercent = validBattery(*d.Health.BatteryPercentage)
		case d.Health.BatteryLevel != nil:
			status.BatteryPercent = validBattery(float64(*d.Health.BatteryLevel))
		}
	}

	if status.BatteryPercent == nil && (status.PowerMode == nil || *status.PowerMode != PowerModeWired) {
		if d.BatteryLife != nil {
			status.BatteryPercent = validBattery(float64(*d.BatteryLife))
		}
	}

	return status
}

func validBattery(value float64) *float64 {
	if math.IsNaN(value) || value < 0 || value > 100 {
		return nil
	}

	return &value
}

// OwnerID normalizes numeric and text identifiers to a string for callers.
type OwnerID string

func (id *OwnerID) UnmarshalJSON(data []byte) error {
	var text string
	if len(data) > 0 && data[0] == '"' {
		err := json.Unmarshal(data, &text)
		if err != nil {
			return err
		}

		*id = OwnerID(text)

		return nil
	}

	var number int64

	err := json.Unmarshal(data, &number)
	if err != nil {
		return err
	}

	*id = OwnerID(strconv.FormatInt(number, 10))

	return nil
}

type DeviceOwner struct {
	ID        *OwnerID `json:"id,omitempty"`
	FirstName *string  `json:"first_name,omitempty"`
	LastName  *string  `json:"last_name,omitempty"`
	Email     *string  `json:"email,omitempty"`
}

type DeviceLegacySettings struct {
	DoorbellVolume         *int       `json:"doorbell_volume,omitempty"`
	LiveViewDisabled       *bool      `json:"live_view_disabled,omitempty"`
	MotionDetectionEnabled *bool      `json:"motion_detection_enabled,omitempty"`
	PowerMode              *PowerMode `json:"power_mode,omitempty"`
}

type DeviceDetailHealth struct {
	BatteryLevel              *int     `json:"battery_level,omitempty"`
	BatteryPercentage         *float64 `json:"battery_percentage,omitempty"`
	BatteryPresent            *bool    `json:"battery_present,omitempty"`
	BatteryPercentageCategory *string  `json:"battery_percentage_category,omitempty"`
	BatteryStatus             *string  `json:"battery_status,omitempty"`
	Connected                 *bool    `json:"connected,omitempty"`
	FirmwareVersion           *string  `json:"firmware_version,omitempty"`
	LastUpdate                *string  `json:"last_update,omitempty"`
	PTZConnected              *string  `json:"ptz_connected,omitempty"`
	RSSI                      *float32 `json:"rssi,omitempty"`
	SignalStrength            *int     `json:"signal_strength,omitempty"`
	SupportedRPCCommands      []string `json:"supported_rpc_commands,omitempty"`
}

type DeviceFeatures struct {
	AutoTrack      *FeatureAvailability   `json:"auto_track,omitempty"`
	AutoZoomTrack  *FeatureAvailability   `json:"auto_zoom_track,omitempty"`
	MotionsEnabled *bool                  `json:"motions_enabled,omitempty"`
	VideoRendering *VideoRenderingFeature `json:"video_rendering,omitempty"`
}

type VideoRenderingFeature struct {
	MaxDigitalZoomLevel *float32 `json:"max_digital_zoom_level,omitempty"`
}

type FeatureAvailability struct {
	Eligibility *FeatureEligibility `json:"eligibility,omitempty"`
	Enablement  *FeatureEnablement  `json:"enablement,omitempty"`
}

type FeatureEligibility struct {
	Eligible             *bool    `json:"eligible,omitempty"`
	IneligibilityReasons []string `json:"ineligibility_reasons,omitempty"`
}

type FeatureEnablement struct {
	Allowed         *bool    `json:"allowed,omitempty"`
	Enabled         *bool    `json:"enabled,omitempty"`
	DisallowReasons []string `json:"disallow_reasons,omitempty"`
}

type LocationAddress struct {
	Address1 *string `json:"address1,omitempty"`
	Address2 *string `json:"address2,omitempty"`
	City     *string `json:"city,omitempty"`
	Country  *string `json:"country,omitempty"`
	State    *string `json:"state,omitempty"`
	Timezone *string `json:"timezone,omitempty"`
	ZipCode  *string `json:"zip_code,omitempty"`
}

type LocationSummary struct {
	ID             string           `json:"location_id"`
	Name           string           `json:"name"`
	LocationType   *string          `json:"location_type,omitempty"`
	IsOwner        *bool            `json:"is_owner,omitempty"`
	OwnerID        *int64           `json:"owner_id,omitempty"`
	Address        *LocationAddress `json:"address,omitempty"`
	GeoCoordinates *GeoCoordinates  `json:"geo_coordinates,omitempty"`
}

type GeoCoordinates struct {
	Latitude  *float32 `json:"latitude,omitempty"`
	Longitude *float32 `json:"longitude,omitempty"`
}

type LocationList struct {
	Locations     []LocationSummary   `json:"user_locations"`
	OperationSets map[string][]string `json:"-"`
}

type LocationAttributes struct {
	Name      *string  `json:"name,omitempty"`
	City      *string  `json:"city,omitempty"`
	Country   *string  `json:"country,omitempty"`
	State     *string  `json:"state,omitempty"`
	Latitude  *float32 `json:"latitude,omitempty"`
	Longitude *float32 `json:"longitude,omitempty"`
	TimeZone  *string  `json:"time_zone,omitempty"`
}

type LocationResource struct {
	ID         string               `json:"id"`
	Type       LocationResourceType `json:"type"`
	Attributes *LocationAttributes  `json:"attributes,omitempty"`
}

type LocationResourceType string

const LocationResourceLocations LocationResourceType = "locations"

type LocationDetail struct {
	Data     LocationResource   `json:"data"`
	Included []LocationResource `json:"included,omitempty"`
	Meta     *LocationMeta      `json:"meta,omitempty"`
}

type LocationMeta struct {
	Time *time.Time `json:"time,omitempty"`
}

type LocationGroup struct {
	ID   *string `json:"id,omitempty"`
	Name *string `json:"name,omitempty"`
}

type LocationGroups struct {
	DeviceGroups []LocationGroup `json:"device_groups,omitempty"`
	IsOwner      *bool           `json:"is_owner,omitempty"`
}

type LocationGroupDevices struct {
	Groups []LocationGroup `json:"groups"`
}

// TimelineEventType is an open server event kind; on_demand was captured.
type TimelineEventType string

const (
	TimelineEventOnDemand TimelineEventType = "on_demand"
	TimelineEventDing     TimelineEventType = "ding"
	TimelineEventMotion   TimelineEventType = "motion"
)

// RecordingStatus is an open server status; ready was captured.
type RecordingStatus string

const RecordingStatusReady RecordingStatus = "ready"

// TimelineState is an open server state; completed was captured.
type TimelineState string

const TimelineStateCompleted TimelineState = "completed"

type TimelineDevice struct {
	ID          *int64  `json:"id,omitempty"`
	Description *string `json:"description,omitempty"`
	Type        *string `json:"type,omitempty"`
}

type TimelineEvent struct {
	ID              string            `json:"event_id"`
	Type            TimelineEventType `json:"event_type"`
	StartTime       time.Time         `json:"start_time"`
	EndTime         *time.Time        `json:"end_time,omitempty"`
	DurationMS      *int              `json:"duration_ms,omitempty"`
	Device          *TimelineDevice   `json:"device,omitempty"`
	SourceID        *string           `json:"source_id,omitempty"`
	IsFavorite      *bool             `json:"is_favorite,omitempty"`
	RecordingStatus *RecordingStatus  `json:"recording_status,omitempty"`
	State           *TimelineState    `json:"state,omitempty"`
	Schema          *string           `json:"schema,omitempty"`
}

type DeviceTimeline struct {
	Items         []TimelineEvent `json:"items"`
	PaginationKey *string         `json:"pagination_key,omitempty"`
}

type HistoryFeedItem struct {
	ID   *string          `json:"id,omitempty"`
	Type *HistoryFeedType `json:"type,omitempty"`
}

type HistoryFeedType string

const HistoryFeedEvent HistoryFeedType = "EVENT"

type HistoryDevices struct {
	Events        []TimelineEvent   `json:"events"`
	Feed          []HistoryFeedItem `json:"feed,omitempty"`
	PaginationKey *string           `json:"pagination_key,omitempty"`
	Schema        *string           `json:"schema,omitempty"`
}

type CapturedTickets struct {
	Host               string   `json:"host"`
	Ticket             string   `json:"ticket"`
	SubscriptionTopics []string `json:"subscriptionTopics,omitempty"`
}

// projectCaptured creates the SDK projection after the generated wire decoder
// has validated the captured response. Unknown fields do not enter the API.
func projectCaptured[T any](wire any) (*T, error) {
	encoded, err := json.Marshal(wire)
	if err != nil {
		return nil, ringapimodels.NewInternalServerError("failed to project captured response", err)
	}

	var result T
	{
		err := json.Unmarshal(encoded, &result)
		if err != nil {
			return nil, ringapimodels.NewInternalServerError("failed to project captured response", err)
		}
	}

	return &result, nil
}
