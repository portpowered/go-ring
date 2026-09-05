package ring

import (
	"context"
	"strconv"

	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// SetVolume sets the volume for a device
func (c *Client) SetVolume(ctx context.Context, req SetVolumeRequest) error {
	if req.Volume < 0 || req.Volume > 11 {
		return ringapimodels.NewBadRequestError("volume must be between 0 and 11", nil)
	}
	deviceIDInt, err := strconv.ParseInt(req.DeviceID, 10, 64)
	if err != nil {
		return ringapimodels.NewBadRequestError("invalid device ID format", err)
	}
	return c.restClient.SetVolume(ctx, deviceIDInt, req.Volume)
}

// SetLights sets the lights for a device (floodlight cams)
// req.State can be "on" or "off"
// req.Duration is optional and specifies how long to keep lights on (in seconds)
func (c *Client) SetLights(ctx context.Context, req SetLightsRequest) error {
	if req.State != "on" && req.State != "off" {
		return ringapimodels.NewBadRequestError("state must be 'on' or 'off'", nil)
	}
	deviceIDInt, err := strconv.ParseInt(req.DeviceID, 10, 64)
	if err != nil {
		return ringapimodels.NewBadRequestError("invalid device ID format", err)
	}
	return c.restClient.SetLights(ctx, deviceIDInt, req.State, req.Duration)
}

// SetMotionDetection sets motion detection for a device
func (c *Client) SetMotionDetection(ctx context.Context, req SetMotionDetectionRequest) error {
	deviceIDInt, err := strconv.ParseInt(req.DeviceID, 10, 64)
	if err != nil {
		return ringapimodels.NewBadRequestError("invalid device ID format", err)
	}
	return c.restClient.SetMotionDetection(ctx, deviceIDInt, req.Enabled)
}

// TestSound tests a sound on a chime device
// req.Kind can be "ding" or "motion"
func (c *Client) TestSound(ctx context.Context, req TestSoundRequest) error {
	if req.Kind != string(ringapimodels.SoundKindDing) && req.Kind != string(ringapimodels.SoundKindMotion) {
		return ringapimodels.NewBadRequestError("kind must be 'ding' or 'motion'", nil)
	}
	deviceIDInt, err := strconv.ParseInt(req.DeviceID, 10, 64)
	if err != nil {
		return ringapimodels.NewBadRequestError("invalid device ID format", err)
	}
	return c.restClient.TestSound(ctx, deviceIDInt, req.Kind)
}

// SetInHomeChime sets in-home chime settings for a doorbell
// req.Settings can include: "type" (e.g., "Mechanical"), "enabled" (bool), "duration" (int)
func (c *Client) SetInHomeChime(ctx context.Context, req SetInHomeChimeRequest) error {
	deviceIDInt, err := strconv.ParseInt(req.DeviceID, 10, 64)
	if err != nil {
		return ringapimodels.NewBadRequestError("invalid device ID format", err)
	}
	return c.restClient.SetInHomeChime(ctx, deviceIDInt, req.Settings)
}
