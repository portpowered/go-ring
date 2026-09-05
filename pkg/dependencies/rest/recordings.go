package rest

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/portpowered/go-ring/pkg/dependencymodels"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// GetDeviceHistory retrieves the history of recordings for a device
func (c *Client) GetDeviceHistory(ctx context.Context, deviceID int64, limit int, kind string) (*dependencymodels.RingRecordingHistoryResponse, error) {
	// Build endpoint with device ID in the path: /clients_api/doorbots/{device_id}/history
	endpoint := "/clients_api/doorbots/" + strconv.FormatInt(deviceID, 10) + "/history"
	params := []string{}

	if limit > 0 {
		params = append(params, "limit="+strconv.Itoa(limit))
	}
	if kind != "" {
		params = append(params, "kind="+kind)
	}

	if len(params) > 0 {
		endpoint += "?" + strings.Join(params, "&")
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
