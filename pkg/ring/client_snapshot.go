package ring

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

var ErrSnapshotNotReady = errors.New("fresh snapshot not available")

// GetSnapshotRequest controls the bounded Python-legacy freshness poll.
// Zero attempts defaults to three; a zero interval polls without delay.
type GetSnapshotRequest struct {
	DeviceID     string
	MaxAttempts  int
	PollInterval time.Duration
}

// Snapshot contains buffered image bytes and the timestamp returned by the
// legacy polling endpoint. The caller may choose how to persist the bytes.
type Snapshot struct {
	Bytes       []byte
	Timestamp   time.Time
	ContentType string
}

// GetSnapshot triggers a fresh legacy snapshot, polls its timestamp, then
// fetches the image. This follows pinned Python behavior; it is not the C1
// app-snaps endpoint, whose response was absent from the capture.
func (c *Client) GetSnapshot(ctx context.Context, req GetSnapshotRequest) (*Snapshot, error) {
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
	if _, err = c.restClient.RefreshSnapshotTimestamp(ctx, id); err != nil {
		return nil, err
	}
	requestedAt := time.Now().UnixMilli()
	for i := 0; i < attempts; i++ {
		if req.PollInterval > 0 {
			timer := time.NewTimer(req.PollInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
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
			return nil, fmt.Errorf("snapshot image was empty")
		}
		return &Snapshot{Bytes: data, Timestamp: time.UnixMilli(timestamp), ContentType: contentType}, nil
	}
	return nil, ErrSnapshotNotReady
}
