package ring

import (
	"context"
	"strconv"

	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// GetDeviceSettings reads the motion_detection_enabled field from the captured
// v1 settings resource. Unrecognized settings are ignored.
func (c *Client) GetDeviceSettings(ctx context.Context, req GetDeviceSettingsRequest) (DeviceSettings, error) {
	id, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return DeviceSettings{}, err
	}
	enabled, err := c.restClient.GetMotionDetectionEnabled(ctx, id)
	if err != nil {
		return DeviceSettings{}, err
	}
	return DeviceSettings{MotionDetectionEnabled: enabled}, nil
}

// PatchDeviceSettings updates only the supported motion detection flag. A nil
// field is rejected instead of silently sending an empty or guessed patch.
func (c *Client) PatchDeviceSettings(ctx context.Context, req PatchDeviceSettingsRequest) error {
	id, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return err
	}
	if req.MotionDetectionEnabled == nil {
		return ringapimodels.NewBadRequestError("no supported settings fields were supplied", nil)
	}
	return c.restClient.PatchMotionDetectionEnabled(ctx, id, *req.MotionDetectionEnabled)
}

// SetSiren calls the captured on/off doorbot route. The captured server response
// reports 30 seconds for the on call, but the request does not accept duration.
func (c *Client) SetSiren(ctx context.Context, req SetSirenRequest) error {
	id, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return err
	}
	return c.restClient.SetSiren(ctx, id, req.Enabled)
}

func settingsDeviceID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, ringapimodels.NewBadRequestError("invalid device ID", err)
	}
	return id, nil
}
