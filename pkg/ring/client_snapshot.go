package ring

import (
	"context"
	"time"

	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

var ErrSnapshotNotReady = ringapimodels.NewNotFoundError("fresh snapshot not available", nil)

// GetSnapshot triggers a fresh legacy snapshot, polls its timestamp, then
// fetches the image. This follows pinned Python behavior; it is not the C1
// app-snaps endpoint, whose response was absent from the capture.
func (c *Client) GetSnapshot(ctx context.Context, req GetSnapshotRequest) (*Snapshot, error) {
	ctx = c.accountContext(ctx, req.Auth)

	id, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return nil, err
	}

	if req.MaxAttempts < 0 || req.MaxAttempts > 100 || req.PollInterval < 0 {
		return nil, ringapimodels.NewBadRequestError("invalid snapshot polling bounds", nil)
	}

	attempts := req.MaxAttempts
	if attempts == 0 {
		attempts = 3
	}

	{
		_, err = c.restClient.RefreshSnapshotTimestamp(ctx, id)
		if err != nil {
			return nil, err
		}
	}

	requestedAt := time.Now().UnixMilli()

	for range attempts {
		if req.PollInterval > 0 {
			timer := time.NewTimer(req.PollInterval)
			select {
			case <-ctx.Done():
				timer.Stop()

				return nil, ringapimodels.NewNetworkError("snapshot polling canceled", ctx.Err())
			case <-timer.C:
			}
		}

		timestamp, err := c.restClient.RefreshSnapshotTimestamp(ctx, id)
		if err != nil {
			return nil, err
		}

		if timestamp <= requestedAt {
			continue
		}

		data, contentType, err := c.restClient.GetSnapshotImage(ctx, id)
		if err != nil {
			return nil, err
		}

		if len(data) == 0 {
			return nil, ringapimodels.NewInternalServerError("snapshot image was empty", nil)
		}

		return &Snapshot{Bytes: data, Timestamp: time.UnixMilli(timestamp), ContentType: contentType}, nil
	}

	return nil, ErrSnapshotNotReady
}
