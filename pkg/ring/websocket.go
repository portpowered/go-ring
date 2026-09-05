package ring

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// EventConnection represents a WebSocket connection for receiving events
type EventConnection struct {
	conn        *websocket.Conn
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	mu          sync.RWMutex
	closed      bool
	messageChan chan *ringapimodels.Event
	errChan     chan error
}

// Note: ConnectEvents is implemented in client_events.go to avoid circular dependencies

// processMessages processes incoming WebSocket messages
func (ec *EventConnection) processMessages() {
	defer ec.wg.Done()
	defer ec.conn.Close()
	defer ec.cancel()

	for {
		select {
		case <-ec.ctx.Done():
			return
		default:
			_, message, err := ec.conn.ReadMessage()
			if err != nil {
				select {
				case ec.errChan <- ringapimodels.NewConnectionError("failed to read message", err):
				default:
				}
				return
			}

			// Parse event
			var eventData map[string]interface{}
			if err := json.Unmarshal(message, &eventData); err != nil {
				continue
			}

			event := parseEvent(eventData)
			if event != nil {
				select {
				case ec.messageChan <- event:
				case <-ec.ctx.Done():
					return
				}
			}
		}
	}
}

// parseEvent parses a raw event message into an Event
func parseEvent(data map[string]interface{}) *ringapimodels.Event {
	event := &ringapimodels.Event{
		Data: data,
	}

	if kind, ok := data["kind"].(string); ok {
		event.Kind = ringapimodels.EventKind(kind)
	}

	if deviceID, ok := data["device_id"].(float64); ok {
		event.DeviceID = int64(deviceID)
	}

	if timestamp, ok := data["timestamp"].(string); ok {
		event.Timestamp = timestamp
	}

	return event
}

// Receive receives an event from the connection
func (ec *EventConnection) Receive() (*ringapimodels.Event, error) {
	ec.mu.RLock()
	closed := ec.closed
	ec.mu.RUnlock()

	if closed {
		return nil, ringapimodels.NewClosedError("connection is closed")
	}

	select {
	case event, ok := <-ec.messageChan:
		if !ok {
			return nil, ringapimodels.NewClosedError("message channel closed")
		}
		return event, nil
	case err, ok := <-ec.errChan:
		if !ok {
			return nil, ringapimodels.NewClosedError("error channel closed")
		}
		return nil, err
	case <-ec.ctx.Done():
		return nil, ringapimodels.NewClosedError("context cancelled")
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
		ec.conn.Close()
	}
	ec.wg.Wait()

	close(ec.messageChan)
	close(ec.errChan)

	return nil
}
