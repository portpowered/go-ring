package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/pkg/generatedhttp"
)

// GetMotionDetectionEnabled reads the one settings field supported by the current
// typed public settings API. Other response fields are intentionally ignored.
func (c *Client) GetMotionDetectionEnabled(ctx context.Context, deviceID int64) (*bool, error) {
	var response generatedhttp.DeviceSettings
	path := settingsPath(deviceID)
	if err := c.doJSONRequest(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	if response.MotionSettings == nil {
		return nil, nil
	}
	return response.MotionSettings.MotionDetectionEnabled, nil
}

// PatchMotionDetectionEnabled changes only the captured motion detection field.
func (c *Client) PatchMotionDetectionEnabled(ctx context.Context, deviceID int64, enabled bool) error {
	body := generatedhttp.DeviceSettingsPatch{MotionSettings: &generatedhttp.MotionSettingsPatch{MotionDetectionEnabled: &enabled}}
	var response json.RawMessage
	return c.doJSONRequest(ctx, http.MethodPatch, settingsPath(deviceID), body, &response)
}

// SetSiren toggles the captured legacy doorbot siren route. Duration is not
// configurable: the capture establishes the server's response duration only.
func (c *Client) SetSiren(ctx context.Context, deviceID int64, enabled bool) error {
	path := protocol.DoorbotSirenOffPath
	if enabled {
		path = protocol.DoorbotSirenOnPath
	}
	path = strings.Replace(path, "{id}", strconv.FormatInt(deviceID, 10), 1)
	return c.doJSONRequest(ctx, http.MethodPut, path, nil, nil)
}

func settingsPath(deviceID int64) string {
	return strings.Replace(protocol.DeviceSettingsPath, "{id}", strconv.FormatInt(deviceID, 10), 1)
}

func legacyPath(pattern string, deviceID int64) string {
	return strings.Replace(pattern, "{id}", strconv.FormatInt(deviceID, 10), 1)
}

// SetChimeVolume uses the Python legacy chime update profile.
func (c *Client) SetChimeVolume(ctx context.Context, deviceID int64, description string, volume int) error {
	return c.setLegacyVolume(ctx, deviceID, protocol.LegacyChimePath, legacyChimeKind, "volume", description, volume)
}

// SetDoorbellVolume uses the Python legacy doorbot update profile.
func (c *Client) SetDoorbellVolume(ctx context.Context, deviceID int64, description string, volume int) error {
	return c.setLegacyVolume(ctx, deviceID, protocol.LegacyDoorbotPath, "doorbot", "doorbell_volume", description, volume)
}

func (c *Client) setLegacyVolume(ctx context.Context, deviceID int64, path, prefix, field, description string, volume int) error {
	query := url.Values{}
	query.Set(prefix+"[description]", description)
	query.Set(prefix+"[settings]["+field+"]", strconv.Itoa(volume))
	return c.doJSONRequest(ctx, http.MethodPut, legacyPath(path, deviceID)+"?"+query.Encode(), nil, nil)
}

// SetLights uses the captured on route and the corresponding legacy off route.
func (c *Client) SetLights(ctx context.Context, deviceID int64, enabled bool) error {
	path := protocol.DoorbotLightOffPath
	if enabled {
		path = protocol.DoorbotLightOnPath
	}
	return c.doJSONRequest(ctx, http.MethodPut, legacyPath(path, deviceID), nil, nil)
}

// SetMotionDetection shares the recorded typed settings PATCH route.
func (c *Client) SetMotionDetection(ctx context.Context, deviceID int64, enabled bool) error {
	return c.PatchMotionDetectionEnabled(ctx, deviceID, enabled)
}

// TestSound uses the Python legacy chime route and query parameter.
func (c *Client) TestSound(ctx context.Context, deviceID int64, kind generatedhttp.TestChimeSoundParamsKind) error {
	query := url.Values{"kind": {string(kind)}}
	return c.doJSONRequest(ctx, http.MethodPost, legacyPath(protocol.LegacyChimeSoundPath, deviceID)+"?"+query.Encode(), nil, nil)
}

// SetInHomeChimeType changes the legacy type field.
func (c *Client) SetInHomeChimeType(ctx context.Context, deviceID int64, description string, value int) error {
	return c.setInHomeChime(ctx, deviceID, description, "type", value)
}

// SetInHomeChimeDuration changes the legacy duration field.
func (c *Client) SetInHomeChimeDuration(ctx context.Context, deviceID int64, description string, value int) error {
	return c.setInHomeChime(ctx, deviceID, description, "duration", value)
}

// SetInHomeChimeEnabled changes the legacy enable field.
func (c *Client) SetInHomeChimeEnabled(ctx context.Context, deviceID int64, description string, enabled bool) error {
	value := 0
	if enabled {
		value = 1
	}
	return c.setInHomeChime(ctx, deviceID, description, "enable", value)
}

func (c *Client) setInHomeChime(ctx context.Context, deviceID int64, description, field string, value int) error {
	query := url.Values{}
	query.Set("doorbot[description]", description)
	query.Set(fmt.Sprintf("doorbot[settings][chime_settings][%s]", field), strconv.Itoa(value))
	return c.doJSONRequest(ctx, http.MethodPut, legacyPath(protocol.LegacyDoorbotPath, deviceID)+"?"+query.Encode(), nil, nil)
}
