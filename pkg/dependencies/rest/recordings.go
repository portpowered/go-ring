package rest

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/pkg/dependencymodels"
	"github.com/portpowered/go-ring/pkg/generatedhttp"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// GetRecordingShareURL follows the pinned Python legacy share/play profile.
// The C1 recording does not establish this route's response.
func (c *Client) GetRecordingShareURL(ctx context.Context, recordingID int64) (string, error) {
	path := strings.Replace(protocol.RecordingSharePath, "{id}", strconv.FormatInt(recordingID, 10), 1)
	var response generatedhttp.RecordingShare
	if err := c.doJSONRequest(ctx, http.MethodGet, path, nil, &response); err != nil {
		return "", err
	}
	if response.Url == "" {
		return "", ringapimodels.NewBadRequestError("recording share response lacks URL", nil)
	}
	return response.Url, nil
}

// GetDeviceHistory retrieves the history of recordings for a device
func (c *Client) GetDeviceHistory(ctx context.Context, deviceID int64, limit int, kind string) (*dependencymodels.RingRecordingHistoryResponse, error) {
	// Build endpoint with device ID in the path: /clients_api/doorbots/{device_id}/history
	endpoint := "/clients_api/doorbots/" + strconv.FormatInt(deviceID, 10) + "/history"
	params := url.Values{}

	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}
	if kind != "" {
		params.Set("kind", kind)
	}

	if encoded := params.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}

	// The API returns an array directly, not wrapped in an object
	var recordings []dependencymodels.RingRecording
	if err := c.doJSONRequest(ctx, "GET", endpoint, nil, &recordings); err != nil {
		return nil, err
	}

	// Populate DeviceID from doorbot.id for each recording
	for i := range recordings {
		recordings[i].DeviceID = recordings[i].Doorbot.ID
	}

	response := &dependencymodels.RingRecordingHistoryResponse{
		Recordings: recordings,
	}
	return response, nil
}

// GetActiveDings retrieves currently active dings
func (c *Client) GetActiveDings(ctx context.Context) (*dependencymodels.RingRecordingHistoryResponse, error) {
	// The API returns an array directly, not wrapped in an object
	var recordings []dependencymodels.RingRecording
	if err := c.doJSONRequest(ctx, "GET", ringapimodels.RingDingsActiveEndpoint, nil, &recordings); err != nil {
		return nil, err
	}

	// Populate DeviceID from doorbot.id for each recording
	for i := range recordings {
		recordings[i].DeviceID = recordings[i].Doorbot.ID
	}

	response := &dependencymodels.RingRecordingHistoryResponse{
		Recordings: recordings,
	}
	return response, nil
}

// GetRecording retrieves a video stream for a recording
// The endpoint returns video/mp4 directly in the response body
func (c *Client) GetRecording(ctx context.Context, recordingID int64) (*ringapimodels.VideoStream, error) {
	endpoint := strings.Replace(ringapimodels.RingRecordingEndpoint, "{id}", strconv.FormatInt(recordingID, 10), 1)

	// Make a raw HTTP request (not JSON) to get the video stream
	url := c.baseURI + endpoint
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, ringapimodels.NewNetworkError("failed to create request", err)
	}

	// Get token and set authorization header
	token, err := c.getToken(ctx)
	if err == nil && token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	// Don't set JSON headers for video requests - accept video/mp4
	req.Header.Set("Accept", "video/mp4,*/*")
	req.Header.Set("User-Agent", c.userAgent)

	if c.hardwareID != "" {
		req.Header.Set("hardware_id", c.hardwareID)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, ringapimodels.NewNetworkError("failed to get recording", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, ringapimodels.NewHTTPError(resp, "")
	}

	// Extract content length from header if available
	var contentLen int64 = -1
	if resp.ContentLength > 0 {
		contentLen = resp.ContentLength
	}

	stream := &ringapimodels.VideoStream{
		Body:        resp.Body,
		ContentType: resp.Header.Get("Content-Type"),
		ContentLen:  contentLen,
		Headers:     resp.Header,
	}

	return stream, nil
}
