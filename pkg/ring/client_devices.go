package ring

import (
	"context"
	"strconv"
	"strings"

	"github.com/portpowered/go-ring/pkg/dependencymodels"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// ListDevices retrieves all devices associated with the account
func (c *Client) ListDevices(ctx context.Context) (*ringapimodels.DevicesResponse, error) {
	if err := c.ensureSession(ctx); err != nil {
		return nil, err
	}
	rawResponse, err := c.restClient.GetDevices(ctx)
	if err != nil {
		return nil, err
	}

	response := &ringapimodels.DevicesResponse{}

	// Convert doorbells
	for _, raw := range rawResponse.Doorbots {
		doorbell := convertToDoorbell(raw)
		response.Doorbells = append(response.Doorbells, doorbell)
	}

	// Convert authorized doorbells (shared doorbells) and merge with doorbells
	for _, raw := range rawResponse.AuthorizedDoorbots {
		doorbell := convertToDoorbell(raw)
		response.Doorbells = append(response.Doorbells, doorbell)
	}

	// Convert chimes
	for _, raw := range rawResponse.Chimes {
		chime := convertToChime(raw)
		response.Chimes = append(response.Chimes, chime)
	}

	// Convert stickup cams
	for _, raw := range rawResponse.StickupCams {
		stickupCam := convertToStickUpCam(raw)
		response.StickUpCams = append(response.StickUpCams, stickupCam)
	}

	// Convert other devices (filter to only intercom types)
	for _, raw := range rawResponse.Other {
		// Filter to only include intercom devices (based on Python ring-doorbell library)
		if isIntercomDevice(raw.Kind) {
			other := convertToOther(raw)
			response.Other = append(response.Other, other)
		}
	}

	return response, nil
}

// GetDevice retrieves a specific device by ID
func (c *Client) GetDevice(ctx context.Context, req GetDeviceRequest) (ringapimodels.Device, error) {
	devices, err := c.ListDevices(ctx)
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
	// Convert string deviceID to int64 for REST API call
	deviceIDInt, err := strconv.ParseInt(req.DeviceID, 10, 64)
	if err != nil {
		return nil, ringapimodels.NewBadRequestError("invalid device ID format", err)
	}

	health, err := c.restClient.GetDeviceHealth(ctx, deviceIDInt)
	if err != nil {
		return nil, err
	}

	return convertToDeviceHealth(health), nil
}

// getDeviceName returns the device name, using Description if Name is empty
func getDeviceName(raw dependencymodels.RingDevice) string {
	if raw.Name != "" {
		return raw.Name
	}
	return raw.Description
}

// getDeviceTimezone returns the device timezone, using TimeZone if Timezone is empty
func getDeviceTimezone(raw dependencymodels.RingDevice) string {
	if raw.Timezone != "" {
		return raw.Timezone
	}
	return raw.TimeZone
}

// isIntercomDevice checks if the device kind indicates it's an intercom device
func isIntercomDevice(kind string) bool {
	return strings.HasPrefix(kind, "intercom_")
}

// convertToDoorbell converts a raw device to a Doorbell
func convertToDoorbell(raw dependencymodels.RingDevice) *ringapimodels.Doorbell {
	doorbell := &ringapimodels.Doorbell{
		ID:                     strconv.FormatInt(raw.ID, 10),
		Name:                   getDeviceName(raw),
		Family:                 raw.Family,
		Address:                raw.Address,
		Timezone:               getDeviceTimezone(raw),
		WifiName:               raw.WifiName,
		WifiSignalStrength:     raw.WifiSignalStrength,
		Volume:                 raw.Volume,
		HasLight:               raw.HasLight,
		LightBrightness:        raw.LightBrightness,
		MotionDetectionEnabled: raw.MotionDetectionEnabled,
	}

	if raw.Health != nil {
		doorbell.Health = convertToDeviceHealth(raw.Health)
	}

	return doorbell
}

// convertToChime converts a raw device to a Chime
func convertToChime(raw dependencymodels.RingDevice) *ringapimodels.Chime {
	chime := &ringapimodels.Chime{
		ID:                 strconv.FormatInt(raw.ID, 10),
		Name:               getDeviceName(raw),
		Family:             raw.Family,
		Address:            raw.Address,
		Timezone:           getDeviceTimezone(raw),
		WifiName:           raw.WifiName,
		WifiSignalStrength: raw.WifiSignalStrength,
		Volume:             raw.Volume,
	}

	if raw.Health != nil {
		chime.Health = convertToDeviceHealth(raw.Health)
	}

	return chime
}

// convertToStickUpCam converts a raw device to a StickUpCam
func convertToStickUpCam(raw dependencymodels.RingDevice) *ringapimodels.StickUpCam {
	stickupCam := &ringapimodels.StickUpCam{
		ID:                     strconv.FormatInt(raw.ID, 10),
		Name:                   getDeviceName(raw),
		Description:            raw.Kind,
		Family:                 raw.Family,
		Address:                raw.Address,
		Timezone:               getDeviceTimezone(raw),
		WifiName:               raw.WifiName,
		WifiSignalStrength:     raw.WifiSignalStrength,
		Volume:                 raw.Volume,
		HasLight:               raw.HasLight,
		LightBrightness:        raw.LightBrightness,
		MotionDetectionEnabled: raw.MotionDetectionEnabled,
	}

	if raw.Health != nil {
		stickupCam.Health = convertToDeviceHealth(raw.Health)
	}

	return stickupCam
}

// convertToOther converts a raw device to an Other device (e.g., Intercom)
func convertToOther(raw dependencymodels.RingDevice) *ringapimodels.Other {
	other := &ringapimodels.Other{
		ID:       strconv.FormatInt(raw.ID, 10),
		Name:     getDeviceName(raw),
		Family:   raw.Family,
		Address:  raw.Address,
		Timezone: getDeviceTimezone(raw),
		Kind:     raw.Kind,
	}

	if raw.Health != nil {
		other.Health = convertToDeviceHealth(raw.Health)
	}

	return other
}

// convertToDeviceHealth converts raw health data to DeviceHealth
func convertToDeviceHealth(raw map[string]interface{}) *ringapimodels.DeviceHealth {
	health := &ringapimodels.DeviceHealth{}

	if batteryLevel, ok := raw["battery_level"].(float64); ok {
		level := int(batteryLevel)
		health.BatteryLevel = &level
	}

	if batteryStatus, ok := raw["battery_status"].(string); ok {
		health.BatteryStatus = &batteryStatus
	}

	if signalStrength, ok := raw["signal_strength"].(float64); ok {
		strength := int(signalStrength)
		health.SignalStrength = &strength
	}

	if firmwareVersion, ok := raw["firmware_version"].(string); ok {
		health.FirmwareVersion = &firmwareVersion
	}

	if lastUpdate, ok := raw["last_update"].(string); ok {
		health.LastUpdate = &lastUpdate
	}

	return health
}
