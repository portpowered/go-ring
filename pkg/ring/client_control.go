package ring

import (
	"context"

	"github.com/portpowered/go-ring/internal/generatedhttp"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// SetVolume uses the legacy chime or doorbell route selected by Kind.
func (c *Client) SetVolume(ctx context.Context, req SetVolumeRequest) error {
	ctx = c.accountContext(ctx, req.Auth)

	if req.Volume < 0 || req.Volume > 11 {
		return ringapimodels.NewBadRequestError("volume must be between 0 and 11", nil)
	}

	if !req.Kind.Valid() {
		return ringapimodels.NewBadRequestError("volume kind must be chime or doorbell", nil)
	}

	if req.Description == "" {
		return ringapimodels.NewBadRequestError("device description is required for legacy volume update", nil)
	}

	deviceIDInt, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return err
	}

	if req.Kind == ringapimodels.VolumeKindChime {
		return c.restClient.SetChimeVolume(ctx, deviceIDInt, req.Description, req.Volume)
	}

	return c.restClient.SetDoorbellVolume(ctx, deviceIDInt, req.Description, req.Volume)
}

// SetLights sets the lights for a device (floodlight cams).
func (c *Client) SetLights(ctx context.Context, req SetLightsRequest) error {
	ctx = c.accountContext(ctx, req.Auth)

	deviceIDInt, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return err
	}

	return c.restClient.SetLights(ctx, deviceIDInt, req.Enabled)
}

// SetMotionDetection sets motion detection for a device.
func (c *Client) SetMotionDetection(ctx context.Context, req SetMotionDetectionRequest) error {
	ctx = c.accountContext(ctx, req.Auth)

	deviceIDInt, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return err
	}

	return c.restClient.SetMotionDetection(ctx, deviceIDInt, req.Enabled)
}

// TestSound tests a sound on a chime device
// req.Sound can be "ding" or "motion".
func (c *Client) TestSound(ctx context.Context, req TestSoundRequest) error {
	ctx = c.accountContext(ctx, req.Auth)

	wireKind := generatedhttp.TestChimeSoundParamsKind(req.Sound)
	if !req.Sound.Valid() || !wireKind.Valid() {
		return ringapimodels.NewBadRequestError("sound must be 'ding' or 'motion'", nil)
	}

	deviceIDInt, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return err
	}

	return c.restClient.TestSound(ctx, deviceIDInt, wireKind)
}

// SetInHomeChime sets in-home chime settings for a doorbell
// req.Settings can include: "type" (e.g., "Mechanical"), "enabled" (bool), "duration" (int).
func (c *Client) SetInHomeChime(ctx context.Context, req SetInHomeChimeRequest) error {
	ctx = c.accountContext(ctx, req.Auth)

	deviceIDInt, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return err
	}

	settings := req.Settings

	count := 0

	if settings.Type != nil {
		count++
	}

	if settings.Enabled != nil {
		count++
	}

	if settings.Duration != nil {
		count++
	}

	if count != 1 {
		return ringapimodels.NewBadRequestError("exactly one in-home chime setting is required", nil)
	}

	if req.Description == "" {
		return ringapimodels.NewBadRequestError("device description is required for legacy in-home chime update", nil)
	}

	if (settings.Type != nil && *settings.Type < 0) || (settings.Duration != nil && *settings.Duration < 0) {
		return ringapimodels.NewBadRequestError("negative in-home chime setting", nil)
	}

	if settings.Type != nil {
		return c.restClient.SetInHomeChimeType(ctx, deviceIDInt, req.Description, *settings.Type)
	}

	if settings.Duration != nil {
		return c.restClient.SetInHomeChimeDuration(ctx, deviceIDInt, req.Description, *settings.Duration)
	}

	return c.restClient.SetInHomeChimeEnabled(ctx, deviceIDInt, req.Description, *settings.Enabled)
}
