package rest

import (
	"context"
	"net/http"
	"strconv"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/pkg/generatedhttp"
)

// GetDevices retrieves all devices from the Ring API
func (c *Client) GetDevices(ctx context.Context) (*generatedhttp.DeviceList, error) {
	var raw generatedhttp.DeviceList
	if err := c.doJSONRequest(ctx, http.MethodGet, protocol.DevicesV3Path, nil, &raw); err != nil {
		return nil, err
	}

	return &raw, nil
}

// RegisterSession registers the client hardware ID with Ring before API use.
func (c *Client) RegisterSession(ctx context.Context) error {
	body := generatedhttp.ClientSessionRegistration{
		Device: generatedhttp.ClientSessionRegistration_Device{
			HardwareId: c.hardwareID,
			Metadata: generatedhttp.ClientSessionRegistration_Device_Metadata{
				ApiVersion:  generatedhttp.N11,
				DeviceModel: deviceModel,
			},
			Os: generatedhttp.Android,
		},
	}
	return c.doJSONRequest(ctx, http.MethodPost, protocol.SessionPath, body, nil)
}

// GetDeviceHealth retrieves health data for a specific device
func (c *Client) GetDeviceHealth(ctx context.Context, deviceID int64) (*generatedhttp.LegacyDeviceHealth, error) {
	var health generatedhttp.LegacyDeviceHealth
	endpoint := protocol.DevicesPath + "/" + strconv.FormatInt(deviceID, 10) + "/health"
	if err := c.doJSONRequest(ctx, http.MethodGet, endpoint, nil, &health); err != nil {
		return nil, err
	}
	return &health, nil
}

func (c *Client) GetFamilyDeviceHealth(ctx context.Context, deviceID int64, family generatedhttp.DeviceFamilyCode) (*generatedhttp.FamilyHealthResponse, error) {
	pattern := protocol.DoorbotHealthPath
	if family == generatedhttp.Chimes {
		pattern = protocol.ChimeHealthPath
	}
	endpoint := capturedIDPath(pattern, deviceID)
	var health generatedhttp.FamilyHealthResponse
	if err := c.doJSONRequest(ctx, http.MethodGet, endpoint, nil, &health); err != nil {
		return nil, err
	}
	return &health, nil
}
