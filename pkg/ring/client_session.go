package ring

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/portpowered/go-ring/internal/signaling"
	dependencywebsocket "github.com/portpowered/go-ring/pkg/dependencies/websocket"
	"github.com/portpowered/go-ring/pkg/generatedhttp"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// OpenSignaling obtains the currently supported legacy signaling ticket and opens its websocket.
// The captured C1 GET /api/v1/clap/tickets profile is intentionally not substituted for this POST route.
func (c *Client) OpenSignaling(ctx context.Context, _ OpenSignalingRequest) (*SignalingConnection, error) {
	if err := ctx.Err(); err != nil {
		return nil, ringapimodels.NewConnectionError("signaling open canceled", err)
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ringapimodels.NewClosedError("client is closed")
	}
	c.mu.Unlock()
	if err := c.ensureSession(ctx); err != nil {
		return nil, ringapimodels.NewConnectionError("failed to register Ring session", err)
	}
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, ringapimodels.NewTokenError("failed to get token for signaling", err)
	}
	if c.endpoints.SolutionsBaseURL == "" {
		return nil, ringapimodels.NewConnectionError("Solutions bootstrap URL is unverified for selected region; configure WithEndpoints", nil)
	}
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	h.Set("User-Agent", c.userAgent)
	h.Set("Content-Type", "application/json")
	if c.hardwareID != "" {
		h.Set("hardware_id", c.hardwareID)
	}
	wire, err := generatedhttp.NewClient(c.endpoints.SolutionsBaseURL, generatedhttp.WithHTTPClient(c.restClient.HTTPClient()))
	if err != nil {
		return nil, ringapimodels.NewNetworkError("failed to configure signaling ticket client", err)
	}
	resp, err := wire.RequestLegacySignalingTicket(ctx, func(_ context.Context, req *http.Request) error { req.Header = h.Clone(); return nil })
	if err != nil {
		return nil, ringapimodels.NewNetworkError("failed to request signaling ticket", err)
	}
	var ticket generatedhttp.LegacySignalingTicket
	b, readErr := io.ReadAll(io.LimitReader(resp.Body, signaling.MaxTicketResponseBytes+1))
	resp.Body.Close()
	if readErr != nil {
		return nil, ringapimodels.NewNetworkError("failed to read signaling ticket response", readErr)
	}
	if len(b) > signaling.MaxTicketResponseBytes {
		return nil, ringapimodels.NewInternalServerError("signaling ticket response exceeds size limit", nil)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, ringapimodels.ClassifyHTTPError(resp, string(b))
	}
	if err = json.Unmarshal(b, &ticket); err != nil {
		return nil, ringapimodels.NewInternalServerError("invalid signaling ticket response", err)
	}
	if ticket.Ticket == "" {
		return nil, ringapimodels.NewConnectionError("empty signaling ticket", nil)
	}
	clientID := uuid.NewString()
	wsURL := strings.Replace(c.signalingWebSocketURL, "{client_id}", clientID, 1)
	wsURL = strings.Replace(wsURL, "{token}", url.QueryEscape(ticket.Ticket), 1)
	wsHeaders := http.Header{}
	wsHeaders.Set("User-Agent", c.userAgent)
	if c.hardwareID != "" {
		wsHeaders.Set("hardware_id", c.hardwareID)
	}
	conn, err := dependencywebsocket.DialSignaling(ctx, wsURL, wsHeaders, c.signalingDialer)
	if err != nil {
		// Dialer errors may contain the ticket-bearing URL.
		return nil, ringapimodels.NewConnectionError("failed to connect to signaling websocket", nil)
	}
	connCtx, cancel := context.WithCancel(ctx)
	s := &SignalingConnection{client: c, conn: conn, ctx: connCtx, cancel: cancel, done: make(chan struct{}), readerDone: make(chan struct{}), pending: make(map[string]chan signaling.Message), sessions: make(map[string]*DeviceSession), channels: make(map[string]chan signaling.Message), playbacks: make(map[string]*PlaybackSession)}
	s.writer = dependencywebsocket.NewSignalingWriter(s.done, s.writeFrame, func(error) { s.fail(ringapimodels.NewConnectionError("signaling write failed", nil)) })
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		cancel()
		conn.Close()
		return nil, ringapimodels.NewClosedError("client is closed")
	}
	if c.signalingConnections == nil {
		c.signalingConnections = make(map[*SignalingConnection]struct{})
	}
	c.signalingConnections[s] = struct{}{}
	c.mu.Unlock()
	go s.readLoop()
	go s.writer.Run()
	go func() {
		select {
		case <-ctx.Done():
			s.fail(ctx.Err())
		case <-s.done:
		}
	}()
	return s, nil
}
