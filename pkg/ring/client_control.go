package ring

import (
	"context"

	"github.com/portpowered/go-ring/pkg/generatedhttp"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// SetVolume sets the volume for a device
func (c *Client) SetVolume(ctx context.Context, req SetVolumeRequest) error {
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

// SetLights sets the lights for a device (floodlight cams)
// req.State can be "on" or "off"
// req.Duration is optional and specifies how long to keep lights on (in seconds)
func (c *Client) SetLights(ctx context.Context, req SetLightsRequest) error {
	if !req.State.Valid() {
		return ringapimodels.NewBadRequestError("state must be 'on' or 'off'", nil)
	}
	if req.Duration != nil {
		return ringapimodels.NewBadRequestError("light duration is not present in the supported legacy request", nil)
	}
	deviceIDInt, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return err
	}
	return c.restClient.SetLights(ctx, deviceIDInt, req.State == ringapimodels.LightStateOn)
}

// SetMotionDetection sets motion detection for a device
func (c *Client) SetMotionDetection(ctx context.Context, req SetMotionDetectionRequest) error {
	deviceIDInt, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return err
	}
	return c.restClient.SetMotionDetection(ctx, deviceIDInt, req.Enabled)
}

// TestSound tests a sound on a chime device
// req.Kind can be "ding" or "motion"
func (c *Client) TestSound(ctx context.Context, req TestSoundRequest) error {
	wireKind := generatedhttp.TestChimeSoundParamsKind(req.Kind)
	if !req.Kind.Valid() || !wireKind.Valid() {
		return ringapimodels.NewBadRequestError("kind must be 'ding' or 'motion'", nil)
	}
	deviceIDInt, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return err
	}
	return c.restClient.TestSound(ctx, deviceIDInt, wireKind)
}

// SetInHomeChime sets in-home chime settings for a doorbell
// req.Settings can include: "type" (e.g., "Mechanical"), "enabled" (bool), "duration" (int)
func (c *Client) SetInHomeChime(ctx context.Context, req SetInHomeChimeRequest) error {
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
	if req.Description == "" || count != 1 {
		return ringapimodels.NewBadRequestError("one in-home chime setting and device description are required", nil)
	}
	if settings.Type != nil {
		if *settings.Type < 0 {
			return ringapimodels.NewBadRequestError("negative in-home chime setting", nil)
		}
		return c.restClient.SetInHomeChimeType(ctx, deviceIDInt, req.Description, *settings.Type)
	}
	if settings.Duration != nil {
		if *settings.Duration < 0 {
			return ringapimodels.NewBadRequestError("negative in-home chime setting", nil)
		}
		return c.restClient.SetInHomeChimeDuration(ctx, deviceIDInt, req.Description, *settings.Duration)
	}
	return c.restClient.SetInHomeChimeEnabled(ctx, deviceIDInt, req.Description, *settings.Enabled)
}
