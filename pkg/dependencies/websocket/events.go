package websocket

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/internal/ringerrors"
)

// EventConnection owns the transport, read loop, and event queue.
type EventConnection struct {
	conn        *websocket.Conn
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	mu          sync.RWMutex
	closed      bool
	messageChan chan map[string]interface{}
	errChan     chan error
}

// OpenEvents connects the account event stream and starts its read loop.
func OpenEvents(ctx context.Context, wsURL, token, hardwareID string) (*EventConnection, error) {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	if hardwareID != "" {
		header.Set("hardware_id", hardwareID)
	}
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		return nil, err
	}
	eventCtx, cancel := context.WithCancel(ctx)
	ec := &EventConnection{conn: conn, ctx: eventCtx, cancel: cancel, messageChan: make(chan map[string]interface{}, 100), errChan: make(chan error, 10)}
	ec.wg.Add(1)
	go ec.processMessages()
	ec.wg.Add(1)
	go func() {
		defer ec.wg.Done()
		<-eventCtx.Done()
		_ = conn.Close()
	}()
	return ec, nil
}

// processMessages processes incoming WebSocket messages
func (ec *EventConnection) processMessages() {
	defer ec.wg.Done()
	defer func() { _ = ec.conn.Close() }()
	defer ec.cancel()

	for {
		select {
		case <-ec.ctx.Done():
			return
		default:
			_, message, err := ec.conn.ReadMessage()
			if err != nil {
				select {
				case ec.errChan <- ringerrors.NewConnectionError("failed to read message", err):
				default:
				}
				return
			}

			// Parse event
			var eventData map[string]interface{}
			if err := json.Unmarshal(message, &eventData); err != nil {
				continue
			}

			select {
			case ec.messageChan <- eventData:
			case <-ec.ctx.Done():
				return
			}
		}
	}
}

// Receive receives an event from the connection
func (ec *EventConnection) Receive() (map[string]interface{}, error) {
	ec.mu.RLock()
	closed := ec.closed
	ec.mu.RUnlock()

	if closed {
		return nil, ringerrors.NewClosedError("connection is closed")
	}
	// Preserve wire order by draining already-decoded events before returning
	// the terminal read error that follows them.
	select {
	case event, ok := <-ec.messageChan:
		if !ok {
			return nil, ringerrors.NewClosedError("message channel closed")
		}
		return event, nil
	default:
	}

	select {
	case event, ok := <-ec.messageChan:
		if !ok {
			return nil, ringerrors.NewClosedError("message channel closed")
		}
		return event, nil
	case err, ok := <-ec.errChan:
		if !ok {
			return nil, ringerrors.NewClosedError("error channel closed")
		}
		return nil, err
	case <-ec.ctx.Done():
		// A peer read failure queues its error before cancelling the context.
		// If both are ready, report the failure rather than a generic close.
		select {
		case err := <-ec.errChan:
			return nil, err
		default:
		}
		return nil, ringerrors.NewClosedError("context cancelled")
	}
}

// Close closes the event connection
func (ec *EventConnection) Close() error {
	ec.mu.Lock()
	if ec.closed {
		ec.mu.Unlock()
		return nil
	}
	ec.closed = true
	ec.mu.Unlock()

	ec.cancel()
	if ec.conn != nil {
		_ = ec.conn.Close()
	}
	ec.wg.Wait()

	close(ec.messageChan)
	close(ec.errChan)

	return nil
}
