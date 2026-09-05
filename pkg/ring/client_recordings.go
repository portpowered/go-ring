package ring

import (
	"context"
	"strconv"

	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// GetDeviceHistory retrieves the history of recordings for a device
func (c *Client) GetDeviceHistory(ctx context.Context, req GetDeviceHistoryRequest) (*ringapimodels.RecordingHistoryResponse, error) {
	deviceIDInt, err := strconv.ParseInt(req.DeviceID, 10, 64)
	if err != nil {
		return nil, ringapimodels.NewBadRequestError("invalid device ID format", err)
	}

	rawResponse, err := c.restClient.GetDeviceHistory(ctx, deviceIDInt, req.Limit, req.Kind)
	if err != nil {
		return nil, err
	}

	response := &ringapimodels.RecordingHistoryResponse{}
	for _, raw := range rawResponse.Recordings {
		recording := &ringapimodels.Recording{
			ID:        raw.ID,
			Kind:      raw.Kind,
			Answered:  raw.Answered,
			CreatedAt: raw.CreatedAt,
			DeviceID:  raw.DeviceID,
		}
		response.Recordings = append(response.Recordings, recording)
	}

	return response, nil
}

// GetActiveDings retrieves currently active dings
func (c *Client) GetActiveDings(ctx context.Context) (*ringapimodels.RecordingHistoryResponse, error) {
	rawResponse, err := c.restClient.GetActiveDings(ctx)
	if err != nil {
		return nil, err
	}

	response := &ringapimodels.RecordingHistoryResponse{}
	for _, raw := range rawResponse.Recordings {
		recording := &ringapimodels.Recording{
			ID:        raw.ID,
			Kind:      raw.Kind,
			Answered:  raw.Answered,
			CreatedAt: raw.CreatedAt,
			DeviceID:  raw.DeviceID,
		}
		response.Recordings = append(response.Recordings, recording)
	}

	return response, nil
}

// GetRecording retrieves a video stream for a recording
func (c *Client) GetRecording(ctx context.Context, req GetRecordingRequest) (*ringapimodels.VideoStream, error) {
	return c.restClient.GetRecording(ctx, req.RecordingID)
}

// GetLastRecordingID retrieves the ID of the most recent recording for a device
func (c *Client) GetLastRecordingID(ctx context.Context, req GetLastRecordingIDRequest) (int64, error) {
	history, err := c.GetDeviceHistory(ctx, GetDeviceHistoryRequest{
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
