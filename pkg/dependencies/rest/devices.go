package rest

import (
	"context"

	"github.com/portpowered/go-ring/internal/generatedhttp"
	"github.com/portpowered/go-ring/internal/ringerrors"
)

// GetDevices retrieves all devices from the Ring API.
func (c *Client) GetDevices(ctx context.Context) (*generatedhttp.DeviceList, error) {
	var raw generatedhttp.DeviceList

	req, err := generatedhttp.NewListDevicesRequest(generatedServerBase(c.baseURI))
	if err != nil {
		return nil, ringerrors.NewNetworkError("failed to build list devices request", err)
	}

	err = c.doGeneratedJSON(ctx, req, &raw)
	if err != nil {
		return nil, err
	}

	return &raw, nil
}

// RegisterSession registers the client hardware ID with Ring before API use.
func (c *Client) RegisterSession(ctx context.Context) error {
	body := generatedhttp.ClientSessionRegistration{
		Device: generatedhttp.ClientSessionRegistration_Device{
			HardwareId: c.hardwareIDFor(ctx),
			Metadata: generatedhttp.ClientSessionRegistration_Device_Metadata{
				ApiVersion:           generatedhttp.N11,
				DeviceModel:          deviceModel,
				AdditionalProperties: nil,
			},
			AdditionalProperties: nil,
			Os:                   generatedhttp.ClientSessionRegistrationDeviceOsAndroid,
		},
		AdditionalProperties: nil,
	}

	req, err := generatedhttp.NewRegisterClientSessionRequest(generatedServerBase(c.baseURI), body)
	if err != nil {
		return ringerrors.NewNetworkError("failed to build client session request", err)
	}

	return c.doGeneratedJSON(ctx, req, nil)
}

// GetDeviceHealth retrieves health data for a specific device.
func (c *Client) GetDeviceHealth(ctx context.Context, deviceID int64) (*generatedhttp.LegacyDeviceHealth, error) {
	var health generatedhttp.LegacyDeviceHealth

	req, err := generatedhttp.NewGetLegacyDeviceHealthRequest(generatedServerBase(c.baseURI), deviceID)
	if err != nil {
		return nil, ringerrors.NewNetworkError("failed to build device health request", err)
	}

	err = c.doGeneratedJSON(ctx, req, &health)
	if err != nil {
		return nil, err
	}

	return &health, nil
}
