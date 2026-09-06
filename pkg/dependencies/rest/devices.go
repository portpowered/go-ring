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
