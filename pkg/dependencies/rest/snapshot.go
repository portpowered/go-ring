package rest

import (
	"context"
	"fmt"
	"io"

	"github.com/portpowered/go-ring/internal/generatedhttp"
	"github.com/portpowered/go-ring/internal/ringerrors"
)

const maxSnapshotBytes = 16 << 20

// RefreshSnapshotTimestamp uses the pinned Python legacy profile. The first
// response may have no timestamp; callers trigger once, then poll for freshness.
func (c *Client) RefreshSnapshotTimestamp(ctx context.Context, deviceID int64) (int64, error) {
	var response generatedhttp.SnapshotTimestampResponse

	req, err := generatedhttp.NewRefreshLegacySnapshotTimestampRequest(
		generatedServerBase(c.baseURI), generatedhttp.SnapshotTimestampRequest{DoorbotIds: []int{int(deviceID)}},
	)
	if err != nil {
		return 0, ringerrors.NewNetworkError("failed to build snapshot refresh request", err)
	}

	err = c.doGeneratedJSON(ctx, req, &response)
	if err != nil {
		return 0, err
	}

	if response.Timestamps == nil || len(*response.Timestamps) == 0 {
		return 0, nil
	}

	return (*response.Timestamps)[0].Timestamp, nil
}

// GetSnapshotImage buffers one bounded JPEG response so the caller owns plain
// bytes rather than a live transport body.
func (c *Client) GetSnapshotImage(ctx context.Context, deviceID int64) ([]byte, string, error) {
	req, err := generatedhttp.NewGetLegacySnapshotImageRequest(generatedServerBase(c.baseURI), deviceID)
	if err != nil {
		return nil, "", ringerrors.NewNetworkError("failed to create snapshot request", err)
	}

	req = req.WithContext(ctx)

	token, err := c.getToken(ctx)
	if err != nil {
		return nil, "", ringerrors.NewTokenError("failed to get snapshot token", err)
	}

	req.Header.Set(string(generatedhttp.Authorization), "Bearer "+token)
	req.Header.Set(string(generatedhttp.Accept), "image/jpeg")
	req.Header.Set(string(generatedhttp.UserAgent), c.userAgent)

	if hardwareID := c.hardwareIDFor(ctx); hardwareID != "" {
		req.Header.Set(string(generatedhttp.HardwareId), hardwareID)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", ringerrors.NewNetworkError("failed to get snapshot image", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", ringerrors.ClassifyHTTPError(resp, "")
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSnapshotBytes+1))
	if err != nil {
		return nil, "", ringerrors.NewNetworkError("failed to read snapshot image", err)
	}

	if len(data) > maxSnapshotBytes {
		return nil, "", ringerrors.NewInternalServerError(
			fmt.Sprintf("snapshot exceeds %d bytes", maxSnapshotBytes),
			nil,
		)
	}

	return data, resp.Header.Get("Content-Type"), nil
}
