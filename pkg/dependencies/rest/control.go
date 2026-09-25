package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/portpowered/go-ring/internal/protocol"
)

// GetMotionDetectionEnabled reads the one settings field supported by the current
// typed public settings API. Other response fields are intentionally ignored.
func (c *Client) GetMotionDetectionEnabled(ctx context.Context, deviceID int64) (*bool, error) {
	var response struct {
		MotionSettings struct {
			MotionDetectionEnabled *bool `json:"motion_detection_enabled"`
		} `json:"motion_settings"`
	}
	path := settingsPath(deviceID)
	if err := c.doJSONRequest(ctx, "GET", path, nil, &response); err != nil {
		return nil, err
	}
	return response.MotionSettings.MotionDetectionEnabled, nil
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

func legacyPath(pattern string, deviceID int64) string {
	return strings.Replace(pattern, "{id}", strconv.FormatInt(deviceID, 10), 1)
}

// SetVolume uses the Python legacy chime or doorbot update profile.
func (c *Client) SetVolume(ctx context.Context, deviceID int64, kind, description string, volume int) error {
	path, prefix := protocol.LegacyDoorbotPath, "doorbot"
	field := "doorbell_volume"
	if kind == "chime" {
		path, prefix, field = protocol.LegacyChimePath, "chime", "volume"
	}
	query := url.Values{}
	query.Set(prefix+"[description]", description)
	query.Set(prefix+"[settings]["+field+"]", strconv.Itoa(volume))
	return c.doJSONRequest(ctx, "PUT", legacyPath(path, deviceID)+"?"+query.Encode(), nil, nil)
}

// SetLights uses the captured on route and the corresponding legacy off route.
func (c *Client) SetLights(ctx context.Context, deviceID int64, state string) error {
	path := protocol.DoorbotLightOffPath
	if state == "on" {
		path = protocol.DoorbotLightOnPath
	}
	return c.doJSONRequest(ctx, "PUT", legacyPath(path, deviceID), nil, nil)
}

// SetMotionDetection shares the recorded typed settings PATCH route.
func (c *Client) SetMotionDetection(ctx context.Context, deviceID int64, enabled bool) error {
	return c.PatchMotionDetectionEnabled(ctx, deviceID, enabled)
}

// TestSound uses the Python legacy chime route and query parameter.
func (c *Client) TestSound(ctx context.Context, deviceID int64, kind string) error {
	query := url.Values{"kind": {kind}}
	return c.doJSONRequest(ctx, "POST", legacyPath(protocol.LegacyChimeSoundPath, deviceID)+"?"+query.Encode(), nil, nil)
}

// SetInHomeChime changes one legacy chime field, matching the Python fixture.
func (c *Client) SetInHomeChime(ctx context.Context, deviceID int64, description, field string, value int) error {
	query := url.Values{}
	query.Set("doorbot[description]", description)
	query.Set(fmt.Sprintf("doorbot[settings][chime_settings][%s]", field), strconv.Itoa(value))
	return c.doJSONRequest(ctx, "PUT", legacyPath(protocol.LegacyDoorbotPath, deviceID)+"?"+query.Encode(), nil, nil)
}
