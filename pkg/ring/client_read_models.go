package ring

import (
	"encoding/json"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"github.com/portpowered/go-ring/pkg/ringtypes"
)

// DeviceDetail keeps operation sets that are derived from the provider's
// envelope beside its schema-generated device projection.
type DeviceDetail struct {
	Device        ringapimodels.DeviceDetailDevice `json:"device"`
	OperationSets map[string][]string              `json:"-"`
}

// DeviceKind is the schema-backed open hardware kind.
type DeviceKind = ringtypes.DeviceKind

const DeviceKindStickUpMiniPTZ DeviceKind = protocol.DeviceKindStickUpMiniPTZ

type DeviceDetailDevice = ringapimodels.DeviceDetailDevice
type BatteryReading = ringtypes.BatteryReading
type DeviceAlerts = ringapimodels.DeviceAlerts
type ConnectionState = ringtypes.ConnectionState

const (
	ConnectionOnline  ConnectionState = protocol.ConnectionOnline
	ConnectionOffline ConnectionState = protocol.ConnectionOffline
)

type PowerMode = ringtypes.PowerMode

const PowerModeWired PowerMode = protocol.PowerModeWired

type DeviceStatus = ringapimodels.DeviceStatus
type OwnerID = ringtypes.OwnerID
type DeviceOwner = ringapimodels.DeviceOwner
type DeviceLegacySettings = ringapimodels.DeviceLegacySettings
type DeviceDetailHealth = ringapimodels.DeviceDetailHealth
type DeviceFeatures = ringapimodels.DeviceFeatures
type VideoRenderingFeature = ringapimodels.VideoRenderingFeature
type FeatureAvailability = ringapimodels.FeatureAvailability
type FeatureEligibility = ringapimodels.FeatureEligibility
type FeatureEnablement = ringapimodels.FeatureEnablement
type LocationAddress = ringapimodels.LocationAddress

type LocationSummary = ringapimodels.LocationSummary
type GeoCoordinates = ringapimodels.GeoCoordinates

// LocationList keeps operation sets derived from the provider envelope beside
// its schema-generated location projection.
type LocationList struct {
	Locations     []ringapimodels.LocationSummary `json:"user_locations"`
	OperationSets map[string][]string             `json:"-"`
}

type LocationAttributes = ringapimodels.LocationAttributes
type LocationResourceType = ringtypes.LocationResourceType

const LocationResourceLocations LocationResourceType = protocol.LocationResourceLocations

type LocationResource = ringapimodels.LocationResource
type LocationDetail = ringapimodels.LocationDetail
type LocationMeta = ringapimodels.LocationMeta
type LocationGroup = ringapimodels.LocationGroup
type LocationGroups = ringapimodels.LocationGroups
type LocationGroupDevices = ringapimodels.LocationGroupDevices
type TimelineEventType = ringtypes.TimelineEventType

const (
	TimelineEventOnDemand TimelineEventType = protocol.TimelineEventOnDemand
	TimelineEventDing     TimelineEventType = protocol.TimelineEventDing
	TimelineEventMotion   TimelineEventType = protocol.TimelineEventMotion
)

type RecordingStatus = ringtypes.RecordingStatus

const RecordingStatusReady RecordingStatus = protocol.RecordingStatusReady

type TimelineState = ringtypes.TimelineState

const TimelineStateCompleted TimelineState = protocol.TimelineStateCompleted

type TimelineDevice = ringapimodels.TimelineDevice
type TimelineEvent = ringapimodels.TimelineEvent
type DeviceTimeline = ringapimodels.DeviceTimeline
type HistoryFeedItem = ringapimodels.HistoryFeedItem
type HistoryFeedType = ringtypes.HistoryFeedType

const HistoryFeedEvent HistoryFeedType = protocol.HistoryFeedEvent

type HistoryDevices = ringapimodels.HistoryDevices
type CapturedTickets = ringapimodels.CapturedTickets

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
