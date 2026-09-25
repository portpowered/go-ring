package rest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/pkg/generatedhttp"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

const maxSnapshotBytes = 16 << 20

// RefreshSnapshotTimestamp uses the pinned Python legacy profile. The first
// response may have no timestamp; callers trigger once, then poll for freshness.
func (c *Client) RefreshSnapshotTimestamp(ctx context.Context, deviceID int64) (int64, error) {
	var response generatedhttp.SnapshotTimestampResponse
	err := c.doJSONRequest(ctx, http.MethodPost, protocol.LegacySnapshotTimestampPath, generatedhttp.SnapshotTimestampRequest{DoorbotIds: []int{int(deviceID)}}, &response)
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
	path := strings.Replace(protocol.LegacySnapshotImagePath, "{id}", strconv.FormatInt(deviceID, 10), 1)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURI+path, nil)
	if err != nil {
		return nil, "", ringapimodels.NewNetworkError("failed to create snapshot request", err)
	}
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, "", ringapimodels.NewTokenError("failed to get snapshot token", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "image/jpeg")
	req.Header.Set("User-Agent", c.userAgent)
	if c.hardwareID != "" {
		req.Header.Set("hardware_id", c.hardwareID)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", ringapimodels.NewNetworkError("failed to get snapshot image", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", ringapimodels.NewHTTPError(resp, "")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSnapshotBytes+1))
	if err != nil {
		return nil, "", ringapimodels.NewNetworkError("failed to read snapshot image", err)
	}
	if len(data) > maxSnapshotBytes {
		return nil, "", ringapimodels.NewBadRequestError(fmt.Sprintf("snapshot exceeds %d bytes", maxSnapshotBytes), nil)
	}
	return data, resp.Header.Get("Content-Type"), nil
}
