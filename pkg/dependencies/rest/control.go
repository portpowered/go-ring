package rest

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/portpowered/go-ring/internal/generatedhttp"
	"github.com/portpowered/go-ring/internal/ringerrors"
)

// GetMotionDetectionEnabled reads the one settings field supported by the current
// typed public settings API. Other response fields are intentionally ignored.
func (c *Client) GetMotionDetectionEnabled(ctx context.Context, deviceID int64) (*bool, error) {
	var response generatedhttp.DeviceSettings

	req, err := generatedhttp.NewGetDeviceSettingsRequest(generatedServerBase(c.baseURI), deviceID)
	if err != nil {
		return nil, ringerrors.NewNetworkError("failed to build device settings request", err)
	}

	err = c.doGeneratedJSON(ctx, req, &response)
	if err != nil {
		return nil, err
	}

	if response.MotionSettings == nil {
		// A missing setting is a valid unknown state for this optional endpoint.
		//nolint:nilnil // nil, nil represents the API's missing motion state.
		return nil, nil
	}

	return response.MotionSettings.MotionDetectionEnabled, nil
}

// PatchMotionDetectionEnabled changes only the captured motion detection field.
func (c *Client) PatchMotionDetectionEnabled(ctx context.Context, deviceID int64, enabled bool) error {
	body := generatedhttp.DeviceSettingsPatch{
		GeneralSettings: nil,
		MotionSettings: &generatedhttp.MotionSettingsPatch{
			AdditionalProperties:   nil,
			MotionDetectionEnabled: &enabled,
		},
		VideoSettings:        nil,
		VolumeSettings:       nil,
		AdditionalProperties: nil,
	}

	var response json.RawMessage

	req, err := generatedhttp.NewPatchDeviceSettingsRequest(generatedServerBase(c.baseURI), deviceID, body)
	if err != nil {
		return ringerrors.NewNetworkError("failed to build device settings update request", err)
	}

	return c.doGeneratedJSON(ctx, req, &response)
}

// SetSiren toggles the captured legacy doorbot siren route. Duration is not
// configurable: the capture establishes the server's response duration only.
func (c *Client) SetSiren(ctx context.Context, deviceID int64, enabled bool) error {
	var (
		req *http.Request
		err error
	)

	if enabled {
		req, err = generatedhttp.NewTurnSirenOnRequest(generatedServerBase(c.baseURI), deviceID)
	} else {
		req, err = generatedhttp.NewTurnSirenOffRequest(generatedServerBase(c.baseURI), deviceID)
	}

	if err != nil {
		return ringerrors.NewNetworkError("failed to build siren request", err)
	}

	return c.doGeneratedJSON(ctx, req, nil)
}

// SetChimeVolume uses the Python legacy chime update profile.
func (c *Client) SetChimeVolume(ctx context.Context, deviceID int64, description string, volume int) error {
	params := &generatedhttp.SetChimeVolumeParams{
		ChimeDescription:    description,
		ChimeSettingsVolume: volume,
	}

	request, err := generatedhttp.NewSetChimeVolumeRequest(generatedServerBase(c.baseURI), deviceID, params)
	if err != nil {
		return ringerrors.NewNetworkError("failed to build chime volume request", err)
	}

	return c.doGeneratedJSON(ctx, request, nil)
}

// SetDoorbellVolume uses the Python legacy doorbot update profile.
func (c *Client) SetDoorbellVolume(ctx context.Context, deviceID int64, description string, volume int) error {
	return c.updateLegacyDoorbotControls(ctx, deviceID, &generatedhttp.UpdateLegacyDoorbotControlsParams{
		DoorbotDescription:                   description,
		DoorbotSettingsDoorbellVolume:        &volume,
		DoorbotSettingsChimeSettingsType:     nil,
		DoorbotSettingsChimeSettingsEnable:   nil,
		DoorbotSettingsChimeSettingsDuration: nil,
	})
}

// SetLights uses the captured on route and the corresponding legacy off route.
func (c *Client) SetLights(ctx context.Context, deviceID int64, enabled bool) error {
	var (
		req *http.Request
		err error
	)

	if enabled {
		req, err = generatedhttp.NewTurnFloodlightOnRequest(generatedServerBase(c.baseURI), deviceID)
	} else {
		req, err = generatedhttp.NewTurnFloodlightOffRequest(generatedServerBase(c.baseURI), deviceID)
	}

	if err != nil {
		return ringerrors.NewNetworkError("failed to build light request", err)
	}

	return c.doGeneratedJSON(ctx, req, nil)
}

// SetMotionDetection shares the recorded typed settings PATCH route.
func (c *Client) SetMotionDetection(ctx context.Context, deviceID int64, enabled bool) error {
	return c.PatchMotionDetectionEnabled(ctx, deviceID, enabled)
}

// TestSound uses the Python legacy chime route and query parameter.
func (c *Client) TestSound(ctx context.Context, deviceID int64, kind generatedhttp.TestChimeSoundParamsKind) error {
	params := &generatedhttp.TestChimeSoundParams{Kind: kind}

	request, err := generatedhttp.NewTestChimeSoundRequest(generatedServerBase(c.baseURI), deviceID, params)
	if err != nil {
		return ringerrors.NewNetworkError("failed to build chime sound request", err)
	}

	return c.doGeneratedJSON(ctx, request, nil)
}

// SetInHomeChimeType changes the legacy type field.
func (c *Client) SetInHomeChimeType(ctx context.Context, deviceID int64, description string, value int) error {
	return c.updateLegacyDoorbotControls(ctx, deviceID, &generatedhttp.UpdateLegacyDoorbotControlsParams{
		DoorbotDescription:                   description,
		DoorbotSettingsDoorbellVolume:        nil,
		DoorbotSettingsChimeSettingsType:     &value,
		DoorbotSettingsChimeSettingsEnable:   nil,
		DoorbotSettingsChimeSettingsDuration: nil,
	})
}

// SetInHomeChimeDuration changes the legacy duration field.
func (c *Client) SetInHomeChimeDuration(ctx context.Context, deviceID int64, description string, value int) error {
	return c.updateLegacyDoorbotControls(ctx, deviceID, &generatedhttp.UpdateLegacyDoorbotControlsParams{
		DoorbotDescription:                   description,
		DoorbotSettingsDoorbellVolume:        nil,
		DoorbotSettingsChimeSettingsType:     nil,
		DoorbotSettingsChimeSettingsEnable:   nil,
		DoorbotSettingsChimeSettingsDuration: &value,
	})
}

// SetInHomeChimeEnabled changes the legacy enable field.
func (c *Client) SetInHomeChimeEnabled(ctx context.Context, deviceID int64, description string, enabled bool) error {
	value := generatedhttp.UpdateLegacyDoorbotControlsParamsDoorbotSettingsChimeSettingsEnable(0)
	if enabled {
		value = generatedhttp.UpdateLegacyDoorbotControlsParamsDoorbotSettingsChimeSettingsEnable(1)
	}

	return c.updateLegacyDoorbotControls(ctx, deviceID, &generatedhttp.UpdateLegacyDoorbotControlsParams{
		DoorbotDescription:                   description,
		DoorbotSettingsDoorbellVolume:        nil,
		DoorbotSettingsChimeSettingsType:     nil,
		DoorbotSettingsChimeSettingsEnable:   &value,
		DoorbotSettingsChimeSettingsDuration: nil,
	})
}

func (c *Client) updateLegacyDoorbotControls(
	ctx context.Context,
	deviceID int64,
	params *generatedhttp.UpdateLegacyDoorbotControlsParams,
) error {
	request, err := generatedhttp.NewUpdateLegacyDoorbotControlsRequest(generatedServerBase(c.baseURI), deviceID, params)
	if err != nil {
		return ringerrors.NewNetworkError("failed to build legacy doorbot controls request", err)
	}

	return c.doGeneratedJSON(ctx, request, nil)
}
