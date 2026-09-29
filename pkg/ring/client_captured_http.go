package ring

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/portpowered/go-ring/internal/generatedhttp"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

func (c *Client) GetDeviceDetail(ctx context.Context, req GetDeviceDetailRequest) (*DeviceDetail, error) {
	ctx = c.accountContext(ctx, req.Auth)

	id, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return nil, err
	}

	{
		err = c.ensureSession(ctx)
		if err != nil {
			return nil, err
		}
	}

	wire, err := c.restClient.GetDeviceDetail(ctx, id)
	if err != nil {
		return nil, err
	}

	detail, err := projectCaptured[DeviceDetail](wire)
	if err != nil {
		return nil, err
	}

	detail.OperationSets = operationNames(wire.DeviceOperationSet)

	return detail, nil
}

// GetDeviceStatus returns the connection and battery state derived from device detail.
func (c *Client) GetDeviceStatus(ctx context.Context, req GetDeviceDetailRequest) (*DeviceStatus, error) {
	detail, err := c.GetDeviceDetail(ctx, req)
	if err != nil {
		return nil, err
	}

	status := detail.Device.Status()

	return &status, nil
}

func (c *Client) ListLocations(ctx context.Context, req ListLocationsRequest) (*LocationList, error) {
	ctx = c.accountContext(ctx, req.Auth)
	{
		err := c.ensureSession(ctx)
		if err != nil {
			return nil, err
		}
	}

	wire, err := c.restClient.ListLocations(ctx)
	if err != nil {
		return nil, err
	}

	locations, err := projectCaptured[LocationList](wire)
	if err != nil {
		return nil, err
	}

	locations.OperationSets = operationNames(wire.LocationOperationSets)

	return locations, nil
}

func operationNames(sets *map[string]generatedhttp.DeviceOperationSet) map[string][]string {
	if sets == nil {
		return nil
	}

	names := make(map[string][]string, len(*sets))

	for name, operations := range *sets {
		for operation := range operations {
			names[name] = append(names[name], operation)
		}

		sort.Strings(names[name])
	}

	return names
}

func locationID(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", ringapimodels.NewBadRequestError("location ID is required", nil)
	}

	return value, nil
}

func (c *Client) GetLocation(ctx context.Context, req GetLocationRequest) (*LocationDetail, error) {
	ctx = c.accountContext(ctx, req.Auth)

	id, err := locationID(req.LocationID)
	if err != nil {
		return nil, err
	}

	{
		err = c.ensureSession(ctx)
		if err != nil {
			return nil, err
		}
	}

	wire, err := c.restClient.GetLocation(ctx, id, req.Params.wire())
	if err != nil {
		return nil, err
	}

	return projectCaptured[LocationDetail](wire)
}

func (c *Client) ListLocationGroups(ctx context.Context, req LocationRequest) (*LocationGroups, error) {
	ctx = c.accountContext(ctx, req.Auth)

	id, err := locationID(req.LocationID)
	if err != nil {
		return nil, err
	}

	{
		err = c.ensureSession(ctx)
		if err != nil {
			return nil, err
		}
	}

	wire, err := c.restClient.ListLocationGroups(ctx, id)
	if err != nil {
		return nil, err
	}

	return projectCaptured[LocationGroups](wire)
}

func (c *Client) ListLocationDevices(ctx context.Context, req LocationRequest) (*LocationGroupDevices, error) {
	ctx = c.accountContext(ctx, req.Auth)

	id, err := locationID(req.LocationID)
	if err != nil {
		return nil, err
	}

	{
		err = c.ensureSession(ctx)
		if err != nil {
			return nil, err
		}
	}

	wire, err := c.restClient.ListLocationDevices(ctx, id)
	if err != nil {
		return nil, err
	}

	return projectCaptured[LocationGroupDevices](wire)
}

func (c *Client) GetDeviceTimeline(ctx context.Context, req GetDeviceTimelineRequest) (*DeviceTimeline, error) {
	ctx = c.accountContext(ctx, req.Auth)

	id, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return nil, err
	}

	if req.Params.Limit != nil && *req.Params.Limit <= 0 {
		return nil, ringapimodels.NewBadRequestError("timeline limit must be positive", nil)
	}

	{
		err = c.ensureSession(ctx)
		if err != nil {
			return nil, err
		}
	}

	wire, err := c.restClient.GetDeviceTimeline(ctx, id, req.Params.wire())
	if err != nil {
		return nil, err
	}

	return projectCaptured[DeviceTimeline](wire)
}

func (c *Client) GetHistoryDevices(ctx context.Context, req GetHistoryDevicesRequest) (*HistoryDevices, error) {
	ctx = c.accountContext(ctx, req.Auth)
	{
		err := c.ensureSession(ctx)
		if err != nil {
			return nil, err
		}
	}

	wire, err := c.restClient.GetHistoryDevices(ctx, req.Params.wire())
	if err != nil {
		return nil, err
	}

	return projectCaptured[HistoryDevices](wire)
}

func (c *Client) RebootDevice(ctx context.Context, req DeviceIDRequest) error {
	ctx = c.accountContext(ctx, req.Auth)

	id, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return err
	}

	{
		err = c.ensureSession(ctx)
		if err != nil {
			return err
		}
	}

	return c.restClient.RebootDevice(ctx, id)
}

// UnlockIntercom requests an unlock on an intercom device ID.
func (c *Client) UnlockIntercom(ctx context.Context, req DeviceIDRequest) error {
	ctx = c.accountContext(ctx, req.Auth)

	id, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return err
	}

	{
		err = c.ensureSession(ctx)
		if err != nil {
			return err
		}
	}

	return c.restClient.UnlockIntercom(ctx, id)
}

func (c *Client) SetPersistentLiveViewEnabled(ctx context.Context, req SetPersistentLiveViewEnabledRequest) error {
	ctx = c.accountContext(ctx, req.Auth)

	id, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return err
	}

	{
		err = c.ensureSession(ctx)
		if err != nil {
			return err
		}
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

	{
		err = c.ensureSession(ctx)
		if err != nil {
			return err
		}
	}

	return c.restClient.FavoriteRecording(ctx, id)
}

func (c *Client) DeleteRecording(ctx context.Context, req DeleteRecordingRequest) error {
	ctx = c.accountContext(ctx, req.Auth)

	id, err := recordingID(req.RecordingID)
	if err != nil {
		return err
	}

	{
		err = c.ensureSession(ctx)
		if err != nil {
			return err
		}
	}

	return c.restClient.DeleteRecording(ctx, id, req.ConfirmDeleteFavorite)
}

// GetCapturedTickets reads the recorded location ticket resource. It does not
// replace the separate POST ticket used by OpenSignaling.
func (c *Client) GetCapturedTickets(ctx context.Context, req GetCapturedTicketsRequest) (*CapturedTickets, error) {
	ctx = c.accountContext(ctx, req.Auth)

	if c.endpoints.SolutionsBaseURL == "" {
		return nil, ringapimodels.NewConnectionError("Solutions endpoint is not configured for this region", nil)
	}

	token, err := c.getToken(ctx)
	if err != nil {
		return nil, err
	}

	wire, err := generatedhttp.NewClientWithResponses(
		c.endpoints.SolutionsBaseURL,
		generatedhttp.WithHTTPClient(c.restClient.HTTPClient()),
	)
	if err != nil {
		return nil, ringapimodels.NewBadRequestError("invalid Solutions endpoint", err)
	}

	params := req.Params.wire()

	response, err := wire.GetCapturedLocationTicketsWithResponse(
		ctx,
		&params,
		func(_ context.Context, request *http.Request) error {
			request.Header.Set(string(generatedhttp.Authorization), "Bearer "+token)
			request.Header.Set(string(generatedhttp.Accept), "application/json")
			request.Header.Set(string(generatedhttp.UserAgent), c.userAgent)

			if hardwareID := c.hardwareIDFor(ctx); hardwareID != "" {
				request.Header.Set(string(generatedhttp.HardwareId), hardwareID)
			}

			return nil
		},
	)
	if err != nil {
		var (
			syntax    *json.SyntaxError
			unmarshal *json.UnmarshalTypeError
		)

		if errors.As(err, &syntax) || errors.As(err, &unmarshal) {
			return nil, ringapimodels.NewInternalServerError("captured ticket response is malformed", err)
		}

		return nil, ringapimodels.NewNetworkError("captured ticket request failed", err)
	}

	if response.JSON200 != nil {
		if response.JSON200.Ticket == "" {
			return nil, ringapimodels.NewInternalServerError("captured ticket response lacks a ticket", nil)
		}

		return projectCaptured[CapturedTickets](response.JSON200)
	}

	if response.StatusCode() < 200 || response.StatusCode() >= 300 {
		return nil, ringapimodels.ClassifyHTTPError(response.HTTPResponse, string(response.Body))
	}

	return nil, ringapimodels.NewInternalServerError("captured ticket response was not JSON", nil)
}
