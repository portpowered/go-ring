package ring

import (
	"context"

	"github.com/portpowered/go-ring/internal/protocol"
	dependencywebsocket "github.com/portpowered/go-ring/pkg/dependencies/websocket"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// ConnectEvents establishes a WebSocket connection for receiving events
// This is a wrapper that calls the internal ConnectEvents method.
func (c *Client) ConnectEvents(ctx context.Context, req ConnectEventsRequest) (*EventConnection, error) {
	ctx = c.accountContext(ctx, req.Auth)

	token, err := c.getToken(ctx)
	if err != nil {
		return nil, ringapimodels.NewTokenError("failed to get token for event connection", err)
	}

	if c.eventWebSocketURL == protocol.ExperimentalEventWebSocketURL {
		err = protocol.ValidateWebSocketURL(protocol.AccountEventsChannel, c.eventWebSocketURL)
	} else {
		err = protocol.ValidateWebSocketOverride(c.eventWebSocketURL)
	}

	if err != nil {
		return nil, ringapimodels.NewBadRequestError("invalid account event WebSocket URL", err)
	}

	conn, err := dependencywebsocket.OpenEventsWithDialer(
		ctx,
		c.eventWebSocketURL,
		token,
		c.hardwareIDFor(ctx),
		c.websocketDialer,
	)
	if err != nil {
		return nil, ringapimodels.NewConnectionError("failed to connect to event stream", err)
	}

	return &EventConnection{transport: conn}, nil
}

func (c *EventConnection) Receive() (*ringapimodels.Event, error) {
	frame, raw, err := c.transport.ReceiveFrame()
	if err != nil {
		if ringapimodels.IsClosedError(err) {
			return nil, ringapimodels.NewClosedError("event connection closed", err)
		}

		return nil, ringapimodels.NewConnectionError("failed to receive event frame", err)
	}

	event := &ringapimodels.Event{
		DeviceID:  int64(frame.DeviceId),
		Kind:      frame.Kind,
		Timestamp: frame.Timestamp,
		Data:      raw,
	}

	return event, nil
}

func (c *EventConnection) Close() error {
	err := c.transport.Close()
	if err != nil {
		return ringapimodels.NewNetworkError("close event connection", err)
	}

	return nil
}

// Listen listens for events, calls the callback for each event, and returns a context error when canceled.
func (c *Client) Listen(ctx context.Context, callback ringapimodels.EventCallback, req ConnectEventsRequest) error {
	conn, err := c.ConnectEvents(ctx, req)
	if err != nil {
		return err
	}

	defer func() { _ = conn.Close() }()

	for {
		select {
		case <-ctx.Done():
			return ringapimodels.NewConnectionError("event listener canceled", ctx.Err())
		default:
			event, err := conn.Receive()
			if err != nil {
				ctxErr := ctx.Err()
				if ctxErr != nil {
					return ringapimodels.NewConnectionError("event listener canceled", ctxErr)
				}

				if ringapimodels.IsClosedError(err) {
					return nil
				}

				return err
			}

			if callback != nil {
				err := callback(event)
				if err != nil {
					return err
				}
			}
		}
	}
}
