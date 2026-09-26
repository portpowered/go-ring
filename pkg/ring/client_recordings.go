package ring

import (
	"context"
	"strconv"

	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// GetDeviceHistory retrieves the history of recordings for a device
func (c *Client) GetDeviceHistory(ctx context.Context, req GetDeviceHistoryRequest) (*ringapimodels.RecordingHistoryResponse, error) {
	ctx = c.accountContext(ctx, req.Auth)
	deviceIDInt, err := strconv.ParseInt(req.DeviceID, 10, 64)
	if err != nil {
		return nil, ringapimodels.NewBadRequestError("invalid device ID format", err)
	}
	if req.OlderThan != nil && *req.OlderThan < 0 {
		return nil, ringapimodels.NewBadRequestError("older-than cursor must be nonnegative", nil)
	}

	rawResponse, err := c.restClient.GetDeviceHistory(ctx, deviceIDInt, req.Limit, string(req.Kind), req.OlderThan)
	if err != nil {
		return nil, err
	}

	response := &ringapimodels.RecordingHistoryResponse{}
	for _, raw := range rawResponse {
		recording := ringapimodels.Recording{
			ID:        int64(raw.Id),
			Kind:      raw.Kind,
			Answered:  raw.Answered,
			CreatedAt: raw.CreatedAt,
			DeviceID:  int64(raw.Doorbot.Id),
		}
		response.Recordings = append(response.Recordings, recording)
	}

	return response, nil
}

// GetActiveDings retrieves currently active dings
func (c *Client) GetActiveDings(ctx context.Context, req GetActiveDingsRequest) (*ringapimodels.RecordingHistoryResponse, error) {
	ctx = c.accountContext(ctx, req.Auth)
	rawResponse, err := c.restClient.GetActiveDings(ctx)
	if err != nil {
		return nil, err
	}

	response := &ringapimodels.RecordingHistoryResponse{}
	for _, raw := range rawResponse {
		recording := ringapimodels.Recording{
			ID:        int64(raw.Id),
			Kind:      raw.Kind,
			Answered:  raw.Answered,
			CreatedAt: raw.CreatedAt,
			DeviceID:  int64(raw.Doorbot.Id),
		}
		response.Recordings = append(response.Recordings, recording)
	}

	return response, nil
}

// GetRecording retrieves a video stream for a recording
func (c *Client) GetRecording(ctx context.Context, req GetRecordingRequest) (*ringapimodels.VideoStream, error) {
	ctx = c.accountContext(ctx, req.Auth)
	return c.restClient.GetRecording(ctx, req.RecordingID)
}

// GetRecordingShareURL returns the legacy playback share URL for a recording.
// Access permissions and subscription checks are resolved by the server.
func (c *Client) GetRecordingShareURL(ctx context.Context, req GetRecordingShareURLRequest) (string, error) {
	ctx = c.accountContext(ctx, req.Auth)
	if req.RecordingID <= 0 {
		return "", ringapimodels.NewBadRequestError("invalid recording ID", nil)
	}
	return c.restClient.GetRecordingShareURL(ctx, req.RecordingID)
}

// GetLastRecordingID retrieves the ID of the most recent recording for a device
func (c *Client) GetLastRecordingID(ctx context.Context, req GetLastRecordingIDRequest) (int64, error) {
	ctx = c.accountContext(ctx, req.Auth)
	history, err := c.GetDeviceHistory(ctx, GetDeviceHistoryRequest{
		Auth:     req.Auth,
		DeviceID: req.DeviceID,
		Limit:    1,
		Kind:     "",
	})
	if err != nil {
		return 0, err
	}

	if len(history.Recordings) == 0 {
		return 0, ringapimodels.NewNotFoundError("no recordings found", nil)
	}

	return history.Recordings[0].ID, nil
}
