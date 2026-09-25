package ring

import (
	"context"
	"fmt"

	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// SetVolume sets the volume for a device
func (c *Client) SetVolume(ctx context.Context, req SetVolumeRequest) error {
	if req.Volume < 0 || req.Volume > 11 {
		return ringapimodels.NewBadRequestError("volume must be between 0 and 11", nil)
	}
	if req.Kind != "chime" && req.Kind != "doorbell" {
		return ringapimodels.NewBadRequestError("volume kind must be chime or doorbell", nil)
	}
	if req.Description == "" {
		return ringapimodels.NewBadRequestError("device description is required for legacy volume update", nil)
	}
	deviceIDInt, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return err
	}
	return c.restClient.SetVolume(ctx, deviceIDInt, req.Kind, req.Description, req.Volume)
}

// SetLights sets the lights for a device (floodlight cams)
// req.State can be "on" or "off"
// req.Duration is optional and specifies how long to keep lights on (in seconds)
func (c *Client) SetLights(ctx context.Context, req SetLightsRequest) error {
	if req.State != "on" && req.State != "off" {
		return ringapimodels.NewBadRequestError("state must be 'on' or 'off'", nil)
	}
	if req.Duration != nil {
		return ringapimodels.NewBadRequestError("light duration is not present in the supported legacy request", nil)
	}
	deviceIDInt, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return err
	}
	return c.restClient.SetLights(ctx, deviceIDInt, req.State)
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
	if req.Kind != string(ringapimodels.SoundKindDing) && req.Kind != string(ringapimodels.SoundKindMotion) {
		return ringapimodels.NewBadRequestError("kind must be 'ding' or 'motion'", nil)
	}
	deviceIDInt, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return err
	}
	return c.restClient.TestSound(ctx, deviceIDInt, req.Kind)
}

// SetInHomeChime sets in-home chime settings for a doorbell
// req.Settings can include: "type" (e.g., "Mechanical"), "enabled" (bool), "duration" (int)
func (c *Client) SetInHomeChime(ctx context.Context, req SetInHomeChimeRequest) error {
	deviceIDInt, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return err
	}
	if req.Description == "" || len(req.Settings) != 1 {
		return ringapimodels.NewBadRequestError("one in-home chime setting and device description are required", nil)
	}
	for key, raw := range req.Settings {
		field := key
		if key == "enabled" {
			field = "enable"
		}
		if field != "type" && field != "enable" && field != "duration" {
			return ringapimodels.NewBadRequestError("unsupported in-home chime setting", nil)
		}
		value := 0
		switch v := raw.(type) {
		case bool:
			if v {
				value = 1
			}
		case int:
			value = v
		default:
			return ringapimodels.NewBadRequestError(fmt.Sprintf("invalid %s setting", key), nil)
		}
		if value < 0 {
			return ringapimodels.NewBadRequestError("negative in-home chime setting", nil)
		}
		return c.restClient.SetInHomeChime(ctx, deviceIDInt, req.Description, field, value)
	}
	return ringapimodels.NewBadRequestError("missing in-home chime setting", nil)
}
