package rest

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// GetMotionDetectionEnabled reads the one settings field supported by the current
// typed public settings API. Other response fields are intentionally ignored.
func (c *Client) GetMotionDetectionEnabled(ctx context.Context, deviceID int64) (bool, error) {
	var response struct {
		MotionSettings struct {
			MotionDetectionEnabled *bool `json:"motion_detection_enabled"`
		} `json:"motion_settings"`
	}
	path := settingsPath(deviceID)
	if err := c.doJSONRequest(ctx, "GET", path, nil, &response); err != nil {
		return false, err
	}
	if response.MotionSettings.MotionDetectionEnabled == nil {
		return false, ringapimodels.NewBadRequestError("settings response omitted motion_detection_enabled", nil)
	}
	return *response.MotionSettings.MotionDetectionEnabled, nil
}

// PatchMotionDetectionEnabled changes only the captured motion detection field.
func (c *Client) PatchMotionDetectionEnabled(ctx context.Context, deviceID int64, enabled bool) error {
	body := struct {
		MotionSettings struct {
			MotionDetectionEnabled bool `json:"motion_detection_enabled"`
		} `json:"motion_settings"`
	}{}
	body.MotionSettings.MotionDetectionEnabled = enabled
	var response json.RawMessage
	return c.doJSONRequest(ctx, "PATCH", settingsPath(deviceID), body, &response)
}

// SetSiren toggles the captured legacy doorbot siren route. Duration is not
// configurable: the capture establishes the server's response duration only.
func (c *Client) SetSiren(ctx context.Context, deviceID int64, enabled bool) error {
	path := protocol.DoorbotSirenOffPath
	if enabled {
		path = protocol.DoorbotSirenOnPath
	}
	path = strings.Replace(path, "{id}", strconv.FormatInt(deviceID, 10), 1)
	return c.doJSONRequest(ctx, "PUT", path, nil, nil)
}

func settingsPath(deviceID int64) string {
	return strings.Replace(protocol.DeviceSettingsPath, "{id}", strconv.FormatInt(deviceID, 10), 1)
}

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
