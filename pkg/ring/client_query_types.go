package ring

import (
	"strings"
	"time"

	"github.com/portpowered/go-ring/internal/generatedhttp"
)

// DeviceFamily selects a legacy family endpoint. Only doorbots and chimes
// currently have a captured family-specific health operation.
type DeviceFamily string

const (
	DeviceFamilyDoorbells DeviceFamily = "doorbots"
	DeviceFamilyChimes    DeviceFamily = "chimes"
	DeviceFamilyCameras   DeviceFamily = "stickup_cams"
	DeviceFamilyOther     DeviceFamily = "other"
)

// HistoryKind is an open recording kind. These values are known from legacy
// history; unfamiliar server kinds may still be passed by conversion.
type HistoryKind string

const (
	HistoryDing     HistoryKind = "ding"
	HistoryMotion   HistoryKind = "motion"
	HistoryOnDemand HistoryKind = "on_demand"
)

// LocationExpansion is an open set of location detail expansions.
// The named values were observed in recordings; future values may be passed.
type LocationExpansion string

const (
	LocationCapabilities LocationExpansion = "capabilities"
	LocationPresentation LocationExpansion = "presentation"
	LocationStatus       LocationExpansion = "status"
)

type LocationParams struct {
	Include []LocationExpansion
}

// TimelineOrder is an open server value. Descending order was recorded.
type TimelineOrder string

const (
	TimelineAscending  TimelineOrder = "asc"
	TimelineDescending TimelineOrder = "desc"
)

// EventCapability is an open server capability token.
type EventCapability string

const (
	CapabilityOfflineEvent EventCapability = "offline_event"
	CapabilityVehicle      EventCapability = "vehicle"
	CapabilityRingtercom   EventCapability = "ringtercom"
)

type TimelineParams struct {
	StartTime    *time.Time
	EndTime      *time.Time
	Order        *TimelineOrder
	Limit        *int
	Capabilities []EventCapability
}

type HistoryDevicesParams struct {
	SourceIDs    []string
	Capabilities []EventCapability
}

// SignalingTransport is an open transport selector. Only WebSocket is captured.
type SignalingTransport string

const SignalingTransportWebSocket SignalingTransport = "ws"

type CapturedTicketsParams struct {
	AllowUserOnly                    *bool
	LocationID                       *string
	LocationSubscription             *string
	EnableExtendedEmergencyCellUsage *bool
	RequestedTransport               *SignalingTransport
}

func joinCapabilities(values []EventCapability) *string {
	if len(values) == 0 {
		return nil
	}

	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = string(value)
	}

	joined := strings.Join(parts, ",")

	return &joined
}

func (p LocationParams) wire() generatedhttp.GetLocationParams {
	if len(p.Include) == 0 {
		return generatedhttp.GetLocationParams{Include: nil}
	}

	parts := make([]string, len(p.Include))
	for i, value := range p.Include {
		parts[i] = string(value)
	}

	joined := strings.Join(parts, ",")

	return generatedhttp.GetLocationParams{Include: &joined}
}

func (p TimelineParams) wire() generatedhttp.GetDeviceTimelineParams {
	var order *string

	if p.Order != nil {
		value := string(*p.Order)
		order = &value
	}

	return generatedhttp.GetDeviceTimelineParams{
		StartTime:    p.StartTime,
		EndTime:      p.EndTime,
		Order:        order,
		Limit:        p.Limit,
		Capabilities: joinCapabilities(p.Capabilities),
	}
}

func (p HistoryDevicesParams) wire() generatedhttp.GetHistoryDevicesParams {
	var sourceIDs *string

	if len(p.SourceIDs) > 0 {
		value := strings.Join(p.SourceIDs, ",")
		sourceIDs = &value
	}

	return generatedhttp.GetHistoryDevicesParams{SourceIds: sourceIDs, Capabilities: joinCapabilities(p.Capabilities)}
}

func (p CapturedTicketsParams) wire() generatedhttp.GetCapturedLocationTicketsParams {
	var transport *string

	if p.RequestedTransport != nil {
		value := string(*p.RequestedTransport)
		transport = &value
	}

	return generatedhttp.GetCapturedLocationTicketsParams{
		AllowUserOnly:                    p.AllowUserOnly,
		LocationID:                       p.LocationID,
		LocationSubscription:             p.LocationSubscription,
		EnableExtendedEmergencyCellUsage: p.EnableExtendedEmergencyCellUsage,
		RequestedTransport:               transport,
	}
}
