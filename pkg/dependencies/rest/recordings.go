package rest

import (
	"context"

	"github.com/portpowered/go-ring/internal/generatedhttp"
	"github.com/portpowered/go-ring/internal/ringerrors"
	"github.com/portpowered/go-ring/internal/ringmedia"
)

// GetRecordingShareURL follows the pinned Python legacy share/play profile.
// The C1 recording does not establish this route's response.
func (c *Client) GetRecordingShareURL(ctx context.Context, recordingID int64) (string, error) {
	var response generatedhttp.RecordingShare

	req, err := generatedhttp.NewGetLegacyRecordingShareURLRequest(generatedServerBase(c.baseURI), recordingID)
	if err != nil {
		return "", ringerrors.NewNetworkError("failed to build recording share request", err)
	}

	err = c.doGeneratedJSON(ctx, req, &response)
	if err != nil {
		return "", err
	}

	if response.Url == "" {
		return "", ringerrors.NewInternalServerError("recording share response lacks URL", nil)
	}

	return response.Url, nil
}

// GetDeviceHistory retrieves the history of recordings for a device.
func (c *Client) GetDeviceHistory(
	ctx context.Context,
	deviceID int64,
	limit int,
	kind string,
	olderThan *int64,
) (generatedhttp.RecordingArray, error) {
	params := &generatedhttp.GetLegacyDeviceHistoryParams{Limit: nil, Kind: nil, OlderThan: olderThan}

	if limit > 0 {
		params.Limit = &limit
	}

	if kind != "" {
		params.Kind = &kind
	}

	// The API returns an array directly, not wrapped in an object
	var recordings generatedhttp.RecordingArray

	req, err := generatedhttp.NewGetLegacyDeviceHistoryRequest(generatedServerBase(c.baseURI), deviceID, params)
	if err != nil {
		return nil, ringerrors.NewNetworkError("failed to build device history request", err)
	}

	err = c.doGeneratedJSON(ctx, req, &recordings)
	if err != nil {
		return nil, err
	}

	return recordings, nil
}

// GetActiveDings retrieves currently active dings.
func (c *Client) GetActiveDings(ctx context.Context) (generatedhttp.RecordingArray, error) {
	// The API returns an array directly, not wrapped in an object
	var recordings generatedhttp.RecordingArray

	req, err := generatedhttp.NewGetActiveDingsRequest(generatedServerBase(c.baseURI))
	if err != nil {
		return nil, ringerrors.NewNetworkError("failed to build active dings request", err)
	}

	err = c.doGeneratedJSON(ctx, req, &recordings)
	if err != nil {
		return nil, err
	}

	return recordings, nil
}

// GetRecording retrieves a video stream for a recording
// The endpoint returns video/mp4 directly in the response body.
func (c *Client) GetRecording(ctx context.Context, recordingID int64) (*ringmedia.VideoStream, error) {
	// Build the streaming request from the OpenAPI operation. Keep the response
	// body open so callers can stream the media instead of buffering it.
	req, err := generatedhttp.NewStreamRecordingRequest(generatedServerBase(c.baseURI), recordingID)
	if err != nil {
		return nil, ringerrors.NewNetworkError("failed to create request", err)
	}

	req = req.WithContext(ctx)

	// Get token and set authorization header
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, ringerrors.NewTokenError("failed to get recording token", err)
	}

	req.Header.Set(string(generatedhttp.Authorization), "Bearer "+token)

	// Don't set JSON headers for video requests - accept video/mp4
	req.Header.Set(string(generatedhttp.Accept), "video/mp4,*/*")
	req.Header.Set(string(generatedhttp.UserAgent), c.userAgent)

	if hardwareID := c.hardwareIDFor(ctx); hardwareID != "" {
		req.Header.Set(string(generatedhttp.HardwareId), hardwareID)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, ringerrors.NewNetworkError("failed to get recording", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = resp.Body.Close()

		return nil, ringerrors.ClassifyHTTPError(resp, "")
	}

	// Extract content length from header if available
	var contentLen int64 = -1
	if resp.ContentLength > 0 {
		contentLen = resp.ContentLength
	}

	stream := &ringmedia.VideoStream{
		Body:        resp.Body,
		ContentType: resp.Header.Get("Content-Type"),
		ContentLen:  contentLen,
		Headers:     resp.Header,
	}

	return stream, nil
}
