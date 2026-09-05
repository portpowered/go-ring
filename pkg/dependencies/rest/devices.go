package rest

import (
	"context"
	"strconv"

	"github.com/portpowered/go-ring/pkg/dependencymodels"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// GetDevices retrieves all devices from the Ring API
func (c *Client) GetDevices(ctx context.Context) (*dependencymodels.RingDevicesResponse, error) {
	var response dependencymodels.RingDevicesResponse
	if err := c.doJSONRequest(ctx, "GET", ringapimodels.RingDevicesEndpoint, nil, &response); err != nil {
		return nil, err
	}
	return &response, nil
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
