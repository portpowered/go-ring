package rest

import (
	"context"
	"strconv"

	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// SetVolume sets the volume for a device
func (c *Client) SetVolume(ctx context.Context, deviceID int64, volume int) error {
	endpoint := ringapimodels.RingDevicesEndpoint + "/" + strconv.FormatInt(deviceID, 10) + "/volume"
	body := map[string]interface{}{
		"volume": volume,
	}
	return c.doJSONRequest(ctx, "PUT", endpoint, body, nil)
}

// SetLights sets the lights for a device (floodlight cams)
func (c *Client) SetLights(ctx context.Context, deviceID int64, state string, duration *int) error {
	endpoint := ringapimodels.RingDevicesEndpoint + "/" + strconv.FormatInt(deviceID, 10) + "/lights"
	body := map[string]interface{}{
		"state": state,
	}
	if duration != nil {
		body["duration"] = *duration
	}
	return c.doJSONRequest(ctx, "PUT", endpoint, body, nil)
}

// SetMotionDetection sets motion detection for a device
func (c *Client) SetMotionDetection(ctx context.Context, deviceID int64, enabled bool) error {
	endpoint := ringapimodels.RingDevicesEndpoint + "/" + strconv.FormatInt(deviceID, 10) + "/motion_detection"
	body := map[string]interface{}{
		"enabled": enabled,
	}
	return c.doJSONRequest(ctx, "PUT", endpoint, body, nil)
}

// TestSound tests a sound on a chime device
func (c *Client) TestSound(ctx context.Context, deviceID int64, kind string) error {
	endpoint := ringapimodels.RingDevicesEndpoint + "/" + strconv.FormatInt(deviceID, 10) + "/test_sound"
	body := map[string]interface{}{
		"kind": kind,
	}
	return c.doJSONRequest(ctx, "POST", endpoint, body, nil)
}

// SetInHomeChime sets in-home chime settings for a doorbell
func (c *Client) SetInHomeChime(ctx context.Context, deviceID int64, settings map[string]interface{}) error {
	endpoint := ringapimodels.RingDevicesEndpoint + "/" + strconv.FormatInt(deviceID, 10) + "/in_home_chime"
	return c.doJSONRequest(ctx, "PUT", endpoint, settings, nil)
}
