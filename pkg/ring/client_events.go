package ring

import (
	"context"

	dependencywebsocket "github.com/portpowered/go-ring/pkg/dependencies/websocket"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// ConnectEvents establishes a WebSocket connection for receiving events
// This is a wrapper that calls the internal ConnectEvents method
func (c *Client) ConnectEvents(ctx context.Context) (*EventConnection, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, ringapimodels.NewTokenError("failed to get token for event connection", err)
	}
	conn, err := dependencywebsocket.OpenEvents(ctx, c.eventWebSocketURL, token, c.hardwareID)
	if err != nil {
		return nil, ringapimodels.NewConnectionError("failed to connect to event stream", err)
	}
	return &EventConnection{transport: conn}, nil
}

func (c *EventConnection) Receive() (*ringapimodels.Event, error) {
	raw, err := c.transport.Receive()
	if err != nil {
		return nil, err
	}
	event := &ringapimodels.Event{Data: raw}
	if kind, ok := raw["kind"].(string); ok {
		event.Kind = ringapimodels.EventKind(kind)
	}
	if deviceID, ok := raw["device_id"].(float64); ok {
		event.DeviceID = int64(deviceID)
	}
	if timestamp, ok := raw["timestamp"].(string); ok {
		event.Timestamp = timestamp
	}
	return event, nil
}

func (c *EventConnection) Close() error { return c.transport.Close() }

// Listen listens for events and calls the callback for each event
func (c *Client) Listen(ctx context.Context, callback ringapimodels.EventCallback) error {
	conn, err := c.ConnectEvents(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	for {
		select {
		case <-ctx.Done():
			return ringapimodels.NewConnectionError("event listener canceled", ctx.Err())
		default:
			event, err := conn.Receive()
			if err != nil {
				if ringapimodels.IsClosedError(err) {
					if ctxErr := ctx.Err(); ctxErr != nil {
						return ringapimodels.NewConnectionError("event listener canceled", ctxErr)
					}
					return nil
				}
				return err
			}

			if callback != nil {
				if err := callback(event); err != nil {
					return err
				}
			}
		}
	}
}
