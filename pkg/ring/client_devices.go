package ring

import (
	"context"
	"strconv"

	"github.com/portpowered/go-ring/internal/generatedhttp"
	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// ListDevices retrieves all devices associated with the account.
func (c *Client) ListDevices(ctx context.Context, req ListDevicesRequest) (*ringapimodels.DevicesResponse, error) {
	ctx = c.accountContext(ctx, req.Auth)
	{
		err := c.ensureSession(ctx)
		if err != nil {
			return nil, err
		}
	}

	rawResponse, err := c.restClient.GetDevices(ctx)
	if err != nil {
		return nil, err
	}

	response := &ringapimodels.DevicesResponse{Devices: make([]ringapimodels.Device, 0, len(rawResponse.Devices))}
	for _, raw := range rawResponse.Devices {
		response.Devices = append(response.Devices, convertDevice(raw))
	}

	return response, nil
}

// GetDevice retrieves a specific device by ID.
func (c *Client) GetDevice(ctx context.Context, req GetDeviceRequest) (*ringapimodels.Device, error) {
	ctx = c.accountContext(ctx, req.Auth)

	devices, err := c.ListDevices(ctx, ListDevicesRequest{Auth: req.Auth})
	if err != nil {
		return nil, err
	}

	for i := range devices.Devices {
		if devices.Devices[i].ID == req.DeviceID {
			return &devices.Devices[i], nil
		}
	}

	return nil, ringapimodels.NewNotFoundError("device not found", nil)
}

// UpdateDeviceHealth refreshes health data via the generic device route.
func (c *Client) UpdateDeviceHealth(
	ctx context.Context,
	req UpdateDeviceHealthRequest,
) (*ringapimodels.DeviceHealth, error) {
	ctx = c.accountContext(ctx, req.Auth)

	deviceIDInt, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return nil, err
	}

	health, err := c.restClient.GetDeviceHealth(ctx, deviceIDInt)
	if err != nil {
		return nil, err
	}

	return convertToDeviceHealth(health), nil
}

// getDeviceName returns the device name, using Description if Name is empty.
func getDeviceName(raw generatedhttp.Device) string {
	if raw.Name != nil && *raw.Name != "" {
		return *raw.Name
	}

	return raw.Description
}

// getDeviceTimezone returns the device timezone, using TimeZone if Timezone is empty.
func getDeviceTimezone(raw generatedhttp.Device) string {
	if raw.Timezone != nil && *raw.Timezone != "" {
		return *raw.Timezone
	}

	return wireString(raw.TimeZone)
}

func wireString(value *string) string {
	if value != nil {
		return *value
	}

	return ""
}

func convertDevice(raw generatedhttp.Device) ringapimodels.Device {
	device := ringapimodels.Device{
		ID:                     strconv.FormatInt(raw.Id, 10),
		Name:                   getDeviceName(raw),
		Kind:                   raw.Kind,
		Type:                   deviceType(raw),
		Family:                 wireString(raw.Family),
		Address:                wireString(raw.Address),
		Timezone:               getDeviceTimezone(raw),
		WifiName:               raw.WifiName,
		WifiSignalStrength:     raw.WifiSignalStrength,
		Volume:                 raw.Volume,
		LightBrightness:        raw.LightBrightness,
		MotionDetectionEnabled: raw.MotionDetectionEnabled,
		Capabilities:           deviceCapabilities(raw),
		Health:                 nil,
	}
	if raw.Health != nil {
		device.Health = convertInventoryHealth(raw.Health)
	}

	return device
}

// deviceType uses the schema's known hardware catalogs. Capabilities describe
// supported operations independently and cannot establish a hardware class.
func deviceType(raw generatedhttp.Device) ringapimodels.DeviceType {
	switch wireString(raw.Family) {
	case string(generatedhttp.Doorbots):
		return ringapimodels.DeviceTypeDoorbell
	case string(generatedhttp.Chimes):
		return ringapimodels.DeviceTypeChime
	case string(generatedhttp.StickupCams):
		return ringapimodels.DeviceTypeCamera
	case "", string(generatedhttp.Other):
		// Missing and generic families may still have a known hardware kind.
	default:
		return ringapimodels.DeviceTypeOther
	}

	switch {
	case generatedhttp.CameraDeviceKind(raw.Kind).Valid():
		return ringapimodels.DeviceTypeCamera
	case generatedhttp.DoorbellDeviceKind(raw.Kind).Valid():
		return ringapimodels.DeviceTypeDoorbell
	case generatedhttp.ChimeDeviceKind(raw.Kind).Valid():
		return ringapimodels.DeviceTypeChime
	default:
		return ringapimodels.DeviceTypeOther
	}
}

func deviceCapabilities(raw generatedhttp.Device) []ringapimodels.DeviceCapability {
	capabilities := make([]ringapimodels.DeviceCapability, 0)
	if raw.HasLight != nil && *raw.HasLight {
		capabilities = append(capabilities, ringapimodels.DeviceCapabilityLight)
	}

	if raw.MotionDetectionEnabled != nil {
		capabilities = append(capabilities, ringapimodels.DeviceCapabilityMotionDetection)
	}

	if raw.Health == nil {
		return capabilities
	}

	if raw.Health.SirenOn != nil {
		capabilities = append(capabilities, ringapimodels.DeviceCapabilitySiren)
	}

	if raw.Health.VodEnabled != nil && *raw.Health.VodEnabled {
		capabilities = append(capabilities, ringapimodels.DeviceCapabilityLiveView)
	}

	if raw.Health.SupportedRpcCommands == nil {
		return capabilities
	}

	for _, command := range *raw.Health.SupportedRpcCommands {
		switch command {
		case protocol.RPCPanStep:
			capabilities = append(capabilities, ringapimodels.DeviceCapabilityPtzPanStep)
		case protocol.RPCTiltStep:
			capabilities = append(capabilities, ringapimodels.DeviceCapabilityPtzTiltStep)
		case protocol.RPCPanContinuous:
			capabilities = append(capabilities, ringapimodels.DeviceCapabilityPtzPanContinuous)
		case protocol.RPCTiltContinuous:
			capabilities = append(capabilities, ringapimodels.DeviceCapabilityPtzTiltContinuous)
		}
	}

	return capabilities
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
