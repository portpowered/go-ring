package rest

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/pkg/generatedhttp"
)

func capturedIDPath(pattern string, id int64) string {
	return strings.Replace(pattern, "{id}", strconv.FormatInt(id, 10), 1)
}

func capturedLocationPath(pattern, id string) string {
	return strings.Replace(pattern, "{id}", url.PathEscape(id), 1)
}

func capturedQuery(path string, query url.Values) string {
	if len(query) == 0 {
		return path
	}
	return path + "?" + query.Encode()
}

// GetDeviceDetail returns the captured v3 detail envelope.
func (c *Client) GetDeviceDetail(ctx context.Context, id int64) (*generatedhttp.DeviceDetail, error) {
	var result generatedhttp.DeviceDetail
	err := c.doJSONRequest(ctx, http.MethodGet, capturedIDPath(protocol.DeviceDetailV3Path, id), nil, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) ListLocations(ctx context.Context) (*generatedhttp.LocationList, error) {
	var result generatedhttp.LocationList
	err := c.doJSONRequest(ctx, http.MethodGet, protocol.LocationListV3Path, nil, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) GetLocation(ctx context.Context, id string, params generatedhttp.GetLocationParams) (*generatedhttp.LocationDetail, error) {
	query := url.Values{}
	if params.Include != nil {
		query.Set("include", *params.Include)
	}
	var result generatedhttp.LocationDetail
	err := c.doJSONRequest(ctx, http.MethodGet, capturedQuery(capturedLocationPath(protocol.LocationDetailV4Path, id), query), nil, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) ListLocationGroups(ctx context.Context, id string) (*generatedhttp.LocationGroups, error) {
	var result generatedhttp.LocationGroups
	err := c.doJSONRequest(ctx, http.MethodGet, capturedLocationPath(protocol.LocationGroupsPath, id), nil, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) ListLocationDevices(ctx context.Context, id string) (*generatedhttp.LocationGroupDevices, error) {
	var result generatedhttp.LocationGroupDevices
	err := c.doJSONRequest(ctx, http.MethodGet, capturedLocationPath(protocol.LocationDevicesPath, id), nil, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) GetDeviceTimeline(ctx context.Context, id int64, params generatedhttp.GetDeviceTimelineParams) (*generatedhttp.DeviceTimeline, error) {
	query := url.Values{}
	if params.StartTime != nil {
		query.Set("start_time", params.StartTime.Format(time.RFC3339))
	}
	if params.EndTime != nil {
		query.Set("end_time", params.EndTime.Format(time.RFC3339))
	}
	if params.Order != nil {
		query.Set("order", *params.Order)
	}
	if params.Limit != nil {
		query.Set("limit", strconv.Itoa(*params.Limit))
	}
	if params.Capabilities != nil {
		query.Set("capabilities", *params.Capabilities)
	}
	var result generatedhttp.DeviceTimeline
	err := c.doJSONRequest(ctx, http.MethodGet, capturedQuery(capturedIDPath(protocol.DeviceTimelinePath, id), query), nil, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) GetHistoryDevices(ctx context.Context, params generatedhttp.GetHistoryDevicesParams) (*generatedhttp.HistoryDevices, error) {
	query := url.Values{}
	if params.SourceIds != nil {
		query.Set("source_ids", *params.SourceIds)
	}
	if params.Capabilities != nil {
		query.Set("capabilities", *params.Capabilities)
	}
	var result generatedhttp.HistoryDevices
	err := c.doJSONRequest(ctx, http.MethodGet, capturedQuery(protocol.HistoryDevicesPath, query), nil, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) RebootDevice(ctx context.Context, id int64) error {
	return c.doJSONRequest(ctx, http.MethodPatch, capturedIDPath(protocol.DeviceCommandPath, id), generatedhttp.DeviceCommand{CommandName: generatedhttp.Reboot}, nil)
}

func (c *Client) SetPersistentLiveViewEnabled(ctx context.Context, id int64, enabled bool) error {
	body := generatedhttp.LiveViewSettingRequest{Entity: generatedhttp.LiveViewSetting{LiveViewEnabled: enabled}}
	return c.doJSONRequest(ctx, http.MethodPut, capturedIDPath(protocol.PersistentLiveViewPath, id), body, nil)
}

func (c *Client) FavoriteRecording(ctx context.Context, id int64) error {
	return c.doJSONRequest(ctx, http.MethodPut, capturedIDPath(protocol.RecordingFavoritePath, id), nil, nil)
}

func (c *Client) DeleteRecording(ctx context.Context, id int64, confirmFavorite *bool) error {
	query := url.Values{}
	if confirmFavorite != nil {
		query.Set("confirm_delete_favorite", strconv.FormatBool(*confirmFavorite))
	}
	return c.doJSONRequest(ctx, http.MethodDelete, capturedQuery(capturedIDPath(protocol.RecordingDeletePath, id), query), nil, nil)
}
