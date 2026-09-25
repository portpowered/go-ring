package rest

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/portpowered/go-ring/pkg/dependencymodels"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// GetDevices retrieves all devices from the Ring API
func (c *Client) GetDevices(ctx context.Context) (*dependencymodels.RingDevicesResponse, error) {
	var raw struct {
		Devices []dependencymodels.RingDevice `json:"devices"`
	}
	if err := c.doJSONRequest(ctx, "GET", ringapimodels.RingDevicesV3Endpoint, nil, &raw); err != nil {
		return nil, err
	}

	response := dependencymodels.RingDevicesResponse{}
	for _, device := range raw.Devices {
		switch classifyDevice(device) {
		case "doorbell":
			if device.Owned != nil && !*device.Owned {
				response.AuthorizedDoorbots = append(response.AuthorizedDoorbots, device)
			} else {
				response.Doorbots = append(response.Doorbots, device)
			}
		case "chime":
			response.Chimes = append(response.Chimes, device)
		case "camera":
			response.StickupCams = append(response.StickupCams, device)
		default:
			response.Other = append(response.Other, device)
		}
	}
	return &response, nil
}

func classifyDevice(device dependencymodels.RingDevice) string {
	kind := strings.ToLower(device.Kind)
	family := strings.ToLower(device.Family)
	// The Python reference's model families include opaque hardware names that
	// cannot be inferred from a substring (notably hp_cam_v1/v2 and jbox_v1).
	if category, ok := pythonDeviceKinds[kind]; ok {
		return category
	}
	if family == "doorbots" || strings.HasPrefix(kind, "doorbot") || strings.HasPrefix(kind, "doorbell") ||
		strings.HasPrefix(kind, "lpd_") || strings.HasPrefix(kind, "jbox_") || strings.HasPrefix(kind, "cocoa_doorbell") {
		return "doorbell"
	}
	if family == "chimes" || kind == "chime" || strings.HasPrefix(kind, "chime_") || strings.HasPrefix(kind, "chime_pro") {
		return "chime"
	}
	if family == "stickup_cams" || strings.Contains(kind, "camera") || strings.Contains(kind, "cam") ||
		strings.HasPrefix(kind, "stickup") || strings.HasPrefix(kind, "spotlight") || strings.HasPrefix(kind, "floodlight") {
		return "camera"
	}
	return "other"
}

// Explicit Python kind parity is kept beside device classification. The
// service-provided family remains a fallback for newer, unknown model names.
var pythonDeviceKinds = func() map[string]string {
	m := map[string]string{}
	for category, kinds := range map[string][]string{
		"doorbell": {"doorbot", "doorbell", "doorbell_v3", "doorbell_v4", "doorbell_v5", "doorbell_scallop_lite", "doorbell_oyster", "doorbell_scallop", "lpd_v1", "lpd_v2", "lpd_v3", "lpd_v4", "jbox_v1", "doorbell_graham_cracker", "df_doorbell_clownfish", "doorbell_portal", "cocoa_doorbell", "cocoa_doorbell_v2"},
		"chime":    {"chime", "chime_v2", "chime_pro", "chime_pro_v2"},
		"camera":   {"hp_cam_v1", "floodlight_v2", "floodlight_pro", "cocoa_floodlight", "stickup_cam_mini", "stickup_cam_mini_v2", "stickup_cam_mini_ptz_v1", "stickup_cam_v4", "hp_cam_v2", "spotlightw_v2", "cocoa_spotlight", "stickup_cam_longfin", "stickup_cam", "stickup_cam_v3", "stickup_cam_lunar", "stickup_cam_elite", "stickup_cam_wired", "cocoa_camera"},
		"other":    {"beams_ct200_transformer", "intercom_handset_audio", "intercom_handset_video"},
	} {
		for _, kind := range kinds {
			m[kind] = category
		}
	}
	return m
}()

// RegisterSession registers the client hardware ID with Ring before API use.
func (c *Client) RegisterSession(ctx context.Context) error {
	body := map[string]any{
		"device": map[string]any{
			"hardware_id": c.hardwareID,
			"metadata":    map[string]any{"api_version": 11, "device_model": "go-ring"},
			"os":          "android",
		},
	}
	var response map[string]any
	return c.doJSONRequest(ctx, http.MethodPost, ringapimodels.RingSessionEndpoint, body, &response)
}

// GetDeviceHealth retrieves health data for a specific device
func (c *Client) GetDeviceHealth(ctx context.Context, deviceID int64) (map[string]interface{}, error) {
	var health map[string]interface{}
	endpoint := ringapimodels.RingDevicesEndpoint + "/" + strconv.FormatInt(deviceID, 10) + "/health"
	if err := c.doJSONRequest(ctx, "GET", endpoint, nil, &health); err != nil {
		return nil, err
	}
	return health, nil
}
