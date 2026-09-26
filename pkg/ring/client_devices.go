package ring

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/portpowered/go-ring/pkg/generatedhttp"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// ListDevices retrieves all devices associated with the account
func (c *Client) ListDevices(ctx context.Context, req ListDevicesRequest) (*ringapimodels.DevicesResponse, error) {
	ctx = c.accountContext(ctx, req.Auth)
	if err := c.ensureSession(ctx); err != nil {
		return nil, err
	}
	rawResponse, err := c.restClient.GetDevices(ctx)
	if err != nil {
		return nil, err
	}

	response := &ringapimodels.DevicesResponse{}

	for _, raw := range rawResponse.Devices {
		switch classifyDevice(raw) {
		case generatedhttp.Doorbots:
			response.Doorbells = append(response.Doorbells, *convertToDoorbell(raw))
		case generatedhttp.Chimes:
			response.Chimes = append(response.Chimes, *convertToChime(raw))
		case generatedhttp.StickupCams:
			response.StickUpCams = append(response.StickUpCams, *convertToStickUpCam(raw))
		default:
			response.Other = append(response.Other, *convertToOther(raw))
		}
	}

	return response, nil
}

// GetDevice retrieves a specific device by ID
func (c *Client) GetDevice(ctx context.Context, req GetDeviceRequest) (ringapimodels.Device, error) {
	ctx = c.accountContext(ctx, req.Auth)
	devices, err := c.ListDevices(ctx, ListDevicesRequest{Auth: req.Auth})
	if err != nil {
		return nil, err
	}

	// Search through all devices
	allDevices := devices.GetAllDevices()
	for _, device := range allDevices {
		if device.GetID() == req.DeviceID {
			return device, nil
		}
	}

	return nil, ringapimodels.NewNotFoundError("device not found", nil)
}

// UpdateDeviceHealth refreshes health data for a device
func (c *Client) UpdateDeviceHealth(ctx context.Context, req UpdateDeviceHealthRequest) (*ringapimodels.DeviceHealth, error) {
	ctx = c.accountContext(ctx, req.Auth)
	// Convert string deviceID to int64 for REST API call
	deviceIDInt, err := strconv.ParseInt(req.DeviceID, 10, 64)
	if err != nil {
		return nil, ringapimodels.NewBadRequestError("invalid device ID format", err)
	}
	if req.Family != "" {
		if req.Family != generatedhttp.Doorbots && req.Family != generatedhttp.Chimes {
			return nil, ringapimodels.NewBadRequestError("unsupported health device family", nil)
		}
		response, healthErr := c.restClient.GetFamilyDeviceHealth(ctx, deviceIDInt, req.Family)
		if healthErr != nil {
			return nil, healthErr
		}
		return convertFamilyDeviceHealth(response), nil
	}

	health, err := c.restClient.GetDeviceHealth(ctx, deviceIDInt)
	if err != nil {
		return nil, err
	}

	return convertToDeviceHealth(health), nil
}

// getDeviceName returns the device name, using Description if Name is empty
func getDeviceName(raw generatedhttp.Device) string {
	if raw.Name != nil && *raw.Name != "" {
		return *raw.Name
	}
	return raw.Description
}

// getDeviceTimezone returns the device timezone, using TimeZone if Timezone is empty
func getDeviceTimezone(raw generatedhttp.Device) string {
	if raw.Timezone != nil && *raw.Timezone != "" {
		return *raw.Timezone
	}
	return wireValue(raw.TimeZone)
}

func wireValue[T any](value *T) T {
	if value != nil {
		return *value
	}
	var zero T
	return zero
}

// classifyDevice uses the kind catalog generated from OpenAPI. The wire kind
// and family fields stay open so unknown hardware is still decoded.
func classifyDevice(raw generatedhttp.Device) generatedhttp.DeviceFamilyCode {
	kind := strings.ToLower(raw.Kind)
	switch {
	case generatedhttp.DoorbellDeviceKind(kind).Valid():
		return generatedhttp.Doorbots
	case generatedhttp.ChimeDeviceKind(kind).Valid():
		return generatedhttp.Chimes
	case generatedhttp.CameraDeviceKind(kind).Valid():
		return generatedhttp.StickupCams
	case generatedhttp.OtherDeviceKind(kind).Valid():
		return generatedhttp.Other
	}
	if raw.Family != nil {
		family := generatedhttp.DeviceFamilyCode(strings.ToLower(*raw.Family))
		if family.Valid() {
			return family
		}
	}
	return generatedhttp.Other
}

// convertToDoorbell converts a raw device to a Doorbell
func convertToDoorbell(raw generatedhttp.Device) *ringapimodels.Doorbell {
	doorbell := &ringapimodels.Doorbell{
		ID:                     strconv.FormatInt(raw.Id, 10),
		Name:                   getDeviceName(raw),
		Family:                 wireValue(raw.Family),
		Address:                wireValue(raw.Address),
		Timezone:               getDeviceTimezone(raw),
		WifiName:               wireValue(raw.WifiName),
		WifiSignalStrength:     wireValue(raw.WifiSignalStrength),
		Volume:                 wireValue(raw.Volume),
		HasLight:               wireValue(raw.HasLight),
		LightBrightness:        raw.LightBrightness,
		MotionDetectionEnabled: wireValue(raw.MotionDetectionEnabled),
	}

	if raw.Health != nil {
		doorbell.Health = convertInventoryHealth(raw.Health)
	}

	return doorbell
}

// convertToChime converts a raw device to a Chime
func convertToChime(raw generatedhttp.Device) *ringapimodels.Chime {
	chime := &ringapimodels.Chime{
		ID:                 strconv.FormatInt(raw.Id, 10),
		Name:               getDeviceName(raw),
		Family:             wireValue(raw.Family),
		Address:            wireValue(raw.Address),
		Timezone:           getDeviceTimezone(raw),
		WifiName:           wireValue(raw.WifiName),
		WifiSignalStrength: wireValue(raw.WifiSignalStrength),
		Volume:             wireValue(raw.Volume),
	}

	if raw.Health != nil {
		chime.Health = convertInventoryHealth(raw.Health)
	}

	return chime
}

// convertToStickUpCam converts a raw device to a StickUpCam
func convertToStickUpCam(raw generatedhttp.Device) *ringapimodels.StickUpCam {
	stickupCam := &ringapimodels.StickUpCam{
		ID:                     strconv.FormatInt(raw.Id, 10),
		Name:                   getDeviceName(raw),
		Description:            raw.Kind,
		Family:                 wireValue(raw.Family),
		Address:                wireValue(raw.Address),
		Timezone:               getDeviceTimezone(raw),
		WifiName:               wireValue(raw.WifiName),
		WifiSignalStrength:     wireValue(raw.WifiSignalStrength),
		Volume:                 wireValue(raw.Volume),
		HasLight:               wireValue(raw.HasLight),
		LightBrightness:        raw.LightBrightness,
		MotionDetectionEnabled: wireValue(raw.MotionDetectionEnabled),
	}

	if raw.Health != nil {
		stickupCam.Health = convertInventoryHealth(raw.Health)
	}

	return stickupCam
}

// convertToOther converts a raw device to an Other device (e.g., Intercom)
func convertToOther(raw generatedhttp.Device) *ringapimodels.Other {
	other := &ringapimodels.Other{
		ID:       strconv.FormatInt(raw.Id, 10),
		Name:     getDeviceName(raw),
		Family:   wireValue(raw.Family),
		Address:  wireValue(raw.Address),
		Timezone: getDeviceTimezone(raw),
		Kind:     raw.Kind,
	}

	if raw.Health != nil {
		other.Health = convertInventoryHealth(raw.Health)
	}

	return other
}

// convertInventoryHealth maps typed inventory health into the public projection.
func convertInventoryHealth(raw *generatedhttp.DeviceHealth) *ringapimodels.DeviceHealth {
	return &ringapimodels.DeviceHealth{
		BatteryLevel:    raw.BatteryLevel,
		BatteryStatus:   raw.BatteryStatus,
		SignalStrength:  raw.SignalStrength,
		FirmwareVersion: raw.FirmwareVersion,
		LastUpdate:      raw.LastUpdate,
	}
}

// convertToDeviceHealth converts the separate legacy health endpoint's JSON.
func convertToDeviceHealth(raw *generatedhttp.LegacyDeviceHealth) *ringapimodels.DeviceHealth {
	return &ringapimodels.DeviceHealth{
		BatteryLevel:    raw.BatteryLevel,
		BatteryStatus:   raw.BatteryStatus,
		SignalStrength:  raw.SignalStrength,
		FirmwareVersion: raw.FirmwareVersion,
		LastUpdate:      raw.LastUpdate,
	}
}

func convertFamilyDeviceHealth(response *generatedhttp.FamilyHealthResponse) *ringapimodels.DeviceHealth {
	raw := response.DeviceHealth
	health := &ringapimodels.DeviceHealth{
		BatteryLevel:    raw.BatteryPercentage,
		BatteryStatus:   raw.BatteryPercentageCategory,
		SignalStrength:  raw.LatestSignalStrength,
		FirmwareVersion: raw.Firmware,
	}
	if raw.UpdatedAt != nil {
		updated := raw.UpdatedAt.Format(time.RFC3339)
		health.LastUpdate = &updated
	}
	return health
}
