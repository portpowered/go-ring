package ring

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/portpowered/go-ring/internal/generatedhttp"
	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/signaling"
	dependencywebsocket "github.com/portpowered/go-ring/pkg/dependencies/websocket"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// OpenSignaling obtains the currently supported legacy signaling ticket and opens its websocket.
// The captured C1 GET /api/v1/clap/tickets profile is intentionally not substituted for this POST route.
func (c *Client) OpenSignaling(ctx context.Context, req OpenSignalingRequest) (*SignalingConnection, error) {
	ctx = c.accountContext(ctx, req.Auth)

	err := ctx.Err()
	if err != nil {
		return nil, ringapimodels.NewConnectionError("signaling open canceled", err)
	}

	ticket, err := c.requestSignalingTicket(ctx)
	if err != nil {
		return nil, err
	}

	clientID := uuid.NewString()
	wsURL := strings.Replace(c.signalingWebSocketURL, "{client_id}", clientID, 1)

	wsURL = strings.Replace(wsURL, "{token}", url.QueryEscape(ticket), 1)

	if c.signalingWebSocketURL == protocol.SignalingURL {
		err = protocol.ValidateWebSocketURL(protocol.SignalingChannel, wsURL)
	} else {
		err = protocol.ValidateWebSocketOverride(wsURL)
	}

	if err != nil {
		return nil, ringapimodels.NewBadRequestError("invalid signaling WebSocket URL", err)
	}

	wsHeaders := http.Header{}
	wsHeaders.Set(string(generatedhttp.UserAgent), c.userAgent)

	if hardwareID := c.hardwareIDFor(ctx); hardwareID != "" {
		wsHeaders.Set(string(generatedhttp.HardwareId), hardwareID)
	}

	conn, err := dependencywebsocket.DialSignaling(ctx, wsURL, wsHeaders, c.websocketDialer)
	if err != nil {
		// Dialer errors may contain the ticket-bearing URL.
		return nil, ringapimodels.NewConnectionError("failed to connect to signaling websocket", nil)
	}

	connCtx, cancel := context.WithCancel(ctx)
	signalingConnection := &SignalingConnection{
		conn:       conn,
		ctx:        connCtx,
		cancel:     cancel,
		done:       make(chan struct{}),
		readerDone: make(chan struct{}),
		pending:    make(map[string]chan signaling.Message),
		sessions:   make(map[string]*DeviceSession),
		channels:   make(map[string]chan signaling.Message),
		playbacks:  make(map[string]*PlaybackSession),
		pushes:     make(map[string]*PushSubscription),
	}
	signalingConnection.writer = dependencywebsocket.NewSignalingWriter(
		signalingConnection.done,
		signalingConnection.writeFrame,
		func(err error) {
			if ringapimodels.IsConnectionError(err) {
				signalingConnection.fail(err)

				return
			}

			signalingConnection.fail(ringapimodels.NewConnectionError("signaling write failed", err))
		},
	)

	go signalingConnection.readLoop()
	go signalingConnection.writer.Run()
	go func() {
		select {
		case <-ctx.Done():
			signalingConnection.fail(ctx.Err())
		case <-signalingConnection.done:
		}
	}()

	return signalingConnection, nil
}

func (c *Client) requestSignalingTicket(ctx context.Context) (string, error) {
	err := c.ensureSession(ctx)
	if err != nil {
		return "", ringapimodels.NewConnectionError("failed to register Ring session", err)
	}

	token, err := c.getToken(ctx)
	if err != nil {
		return "", ringapimodels.NewTokenError("failed to get token for signaling", err)
	}

	if c.endpoints.SolutionsBaseURL == "" {
		return "", ringapimodels.NewConnectionError(
			"Solutions bootstrap URL is unverified for selected region; configure WithEndpoints",
			nil,
		)
	}

	headers := http.Header{}
	headers.Set(string(generatedhttp.Authorization), "Bearer "+token)
	headers.Set(string(generatedhttp.UserAgent), c.userAgent)
	headers.Set(string(generatedhttp.ContentType), "application/json")

	if hardwareID := c.hardwareIDFor(ctx); hardwareID != "" {
		headers.Set(string(generatedhttp.HardwareId), hardwareID)
	}

	wire, err := generatedhttp.NewClient(
		c.endpoints.SolutionsBaseURL,
		generatedhttp.WithHTTPClient(c.restClient.HTTPClient()),
	)
	if err != nil {
		return "", ringapimodels.NewNetworkError("failed to configure signaling ticket client", err)
	}

	response, err := wire.RequestLegacySignalingTicket(ctx, func(_ context.Context, request *http.Request) error {
		request.Header = headers.Clone()

		return nil
	})
	if err != nil {
		return "", ringapimodels.NewNetworkError("failed to request signaling ticket", err)
	}

	responseBytes, readErr := io.ReadAll(io.LimitReader(response.Body, signaling.MaxTicketResponseBytes+1))
	_ = response.Body.Close()

	if readErr != nil {
		return "", ringapimodels.NewNetworkError("failed to read signaling ticket response", readErr)
	}

	if len(responseBytes) > signaling.MaxTicketResponseBytes {
		return "", ringapimodels.NewInternalServerError("signaling ticket response exceeds size limit", nil)
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", ringapimodels.ClassifyHTTPError(response, string(responseBytes))
	}

	var ticket generatedhttp.LegacySignalingTicket

	err = json.Unmarshal(responseBytes, &ticket)
	if err != nil {
		return "", ringapimodels.NewInternalServerError("invalid signaling ticket response", err)
	}

	if ticket.Ticket == "" {
		return "", ringapimodels.NewConnectionError("empty signaling ticket", nil)
	}

	return ticket.Ticket, nil
}
