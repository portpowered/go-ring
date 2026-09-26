package ring

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/portpowered/go-ring/pkg/generatedhttp"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

func (c *Client) GetDeviceDetail(ctx context.Context, req GetDeviceDetailRequest) (*generatedhttp.DeviceDetail, error) {
	ctx = c.accountContext(ctx, req.Auth)
	id, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return nil, err
	}
	if err = c.ensureSession(ctx); err != nil {
		return nil, err
	}
	return c.restClient.GetDeviceDetail(ctx, id)
}

func (c *Client) ListLocations(ctx context.Context, req ListLocationsRequest) (*generatedhttp.LocationList, error) {
	ctx = c.accountContext(ctx, req.Auth)
	if err := c.ensureSession(ctx); err != nil {
		return nil, err
	}
	return c.restClient.ListLocations(ctx)
}

func locationID(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", ringapimodels.NewBadRequestError("location ID is required", nil)
	}
	return value, nil
}

func (c *Client) GetLocation(ctx context.Context, req GetLocationRequest) (*generatedhttp.LocationDetail, error) {
	ctx = c.accountContext(ctx, req.Auth)
	id, err := locationID(req.LocationID)
	if err != nil {
		return nil, err
	}
	if err = c.ensureSession(ctx); err != nil {
		return nil, err
	}
	return c.restClient.GetLocation(ctx, id, req.Params)
}

func (c *Client) ListLocationGroups(ctx context.Context, req LocationRequest) (*generatedhttp.LocationGroups, error) {
	ctx = c.accountContext(ctx, req.Auth)
	id, err := locationID(req.LocationID)
	if err != nil {
		return nil, err
	}
	if err = c.ensureSession(ctx); err != nil {
		return nil, err
	}
	return c.restClient.ListLocationGroups(ctx, id)
}

func (c *Client) ListLocationDevices(ctx context.Context, req LocationRequest) (*generatedhttp.LocationGroupDevices, error) {
	ctx = c.accountContext(ctx, req.Auth)
	id, err := locationID(req.LocationID)
	if err != nil {
		return nil, err
	}
	if err = c.ensureSession(ctx); err != nil {
		return nil, err
	}
	return c.restClient.ListLocationDevices(ctx, id)
}

func (c *Client) GetDeviceTimeline(ctx context.Context, req GetDeviceTimelineRequest) (*generatedhttp.DeviceTimeline, error) {
	ctx = c.accountContext(ctx, req.Auth)
	id, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return nil, err
	}
	if req.Params.Limit != nil && *req.Params.Limit <= 0 {
		return nil, ringapimodels.NewBadRequestError("timeline limit must be positive", nil)
	}
	if err = c.ensureSession(ctx); err != nil {
		return nil, err
	}
	return c.restClient.GetDeviceTimeline(ctx, id, req.Params)
}

func (c *Client) GetHistoryDevices(ctx context.Context, req GetHistoryDevicesRequest) (*generatedhttp.HistoryDevices, error) {
	ctx = c.accountContext(ctx, req.Auth)
	if err := c.ensureSession(ctx); err != nil {
		return nil, err
	}
	return c.restClient.GetHistoryDevices(ctx, req.Params)
}

func (c *Client) RebootDevice(ctx context.Context, req DeviceIDRequest) error {
	ctx = c.accountContext(ctx, req.Auth)
	id, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return err
	}
	if err = c.ensureSession(ctx); err != nil {
		return err
	}
	return c.restClient.RebootDevice(ctx, id)
}

func (c *Client) SetPersistentLiveViewEnabled(ctx context.Context, req SetPersistentLiveViewEnabledRequest) error {
	ctx = c.accountContext(ctx, req.Auth)
	id, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return err
	}
	if err = c.ensureSession(ctx); err != nil {
		return err
	}
	return c.restClient.SetPersistentLiveViewEnabled(ctx, id, req.Enabled)
}

func recordingID(value int64) (int64, error) {
	if value <= 0 {
		return 0, ringapimodels.NewBadRequestError("recording ID must be positive", nil)
	}
	return value, nil
}

func (c *Client) FavoriteRecording(ctx context.Context, req RecordingIDRequest) error {
	ctx = c.accountContext(ctx, req.Auth)
	id, err := recordingID(req.RecordingID)
	if err != nil {
		return err
	}
	if err = c.ensureSession(ctx); err != nil {
		return err
	}
	return c.restClient.FavoriteRecording(ctx, id)
}

func (c *Client) DeleteRecording(ctx context.Context, req DeleteRecordingRequest) error {
	ctx = c.accountContext(ctx, req.Auth)
	id, err := recordingID(req.RecordingID)
	if err != nil {
		return err
	}
	if err = c.ensureSession(ctx); err != nil {
		return err
	}
	return c.restClient.DeleteRecording(ctx, id, req.ConfirmDeleteFavorite)
}

// GetCapturedTickets reads the recorded location ticket resource. It does not
// replace the separate POST ticket used by OpenSignaling.
func (c *Client) GetCapturedTickets(ctx context.Context, req GetCapturedTicketsRequest) (*generatedhttp.CapturedTickets, error) {
	ctx = c.accountContext(ctx, req.Auth)
	if c.endpoints.SolutionsBaseURL == "" {
		return nil, ringapimodels.NewConnectionError("Solutions endpoint is not configured for this region", nil)
	}
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, err
	}
	wire, err := generatedhttp.NewClientWithResponses(c.endpoints.SolutionsBaseURL, generatedhttp.WithHTTPClient(c.restClient.HTTPClient()))
	if err != nil {
		return nil, ringapimodels.NewBadRequestError("invalid Solutions endpoint", err)
	}
	response, err := wire.GetCapturedLocationTicketsWithResponse(ctx, &req.Params, func(_ context.Context, request *http.Request) error {
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Accept", "application/json")
		request.Header.Set("User-Agent", c.userAgent)
		if hardwareID := c.hardwareIDFor(ctx); hardwareID != "" {
			request.Header.Set("hardware_id", hardwareID)
		}
		return nil
	})
	if err != nil {
		var syntax *json.SyntaxError
		var unmarshal *json.UnmarshalTypeError
		if errors.As(err, &syntax) || errors.As(err, &unmarshal) {
			return nil, ringapimodels.NewInternalServerError("captured ticket response is malformed", err)
		}
		return nil, ringapimodels.NewNetworkError("captured ticket request failed", err)
	}
	if response.JSON200 != nil {
		if response.JSON200.Ticket == "" {
			return nil, ringapimodels.NewInternalServerError("captured ticket response lacks a ticket", nil)
		}
		return response.JSON200, nil
	}
	if response.StatusCode() < 200 || response.StatusCode() >= 300 {
		return nil, ringapimodels.ClassifyHTTPError(response.HTTPResponse, string(response.Body))
	}
	return nil, ringapimodels.NewInternalServerError("captured ticket response was not JSON", nil)
}
