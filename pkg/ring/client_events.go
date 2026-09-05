package ring

import (
	"context"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// ConnectEvents establishes a WebSocket connection for receiving events
// This is a wrapper that calls the internal ConnectEvents method
func (c *Client) ConnectEvents(ctx context.Context) (*EventConnection, error) {
	return connectEventsInternal(c, ctx)
}

// connectEventsInternal is the internal implementation
func connectEventsInternal(c *Client, ctx context.Context) (*EventConnection, error) {
	// Get token for authentication
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, ringapimodels.NewTokenError("failed to get token for event connection", err)
	}

	wsURL := c.eventWebSocketURL

	// Create WebSocket dialer
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	// Add authorization header
	header := make(map[string][]string)
	header["Authorization"] = []string{"Bearer " + token}
	if c.hardwareID != "" {
		header["hardware_id"] = []string{c.hardwareID}
	}

	conn, _, err := dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		return nil, ringapimodels.NewConnectionError("failed to connect to event stream", err)
	}

	eventCtx, cancel := context.WithCancel(ctx)
	ec := &EventConnection{
		conn:        conn,
		ctx:         eventCtx,
		cancel:      cancel,
		messageChan: make(chan *ringapimodels.Event, 100),
		errChan:     make(chan error, 10),
	}

	// Start message processing
	ec.wg.Add(1)
	go ec.processMessages()
	// Cancellation must interrupt ReadMessage even when the peer sends no data.
	ec.wg.Add(1)
	go func() {
		defer ec.wg.Done()
		<-eventCtx.Done()
		conn.Close()
	}()

	return ec, nil
}

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
			return ctx.Err()
		default:
			event, err := conn.Receive()
			if err != nil {
				if ringapimodels.IsClosedError(err) {
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
