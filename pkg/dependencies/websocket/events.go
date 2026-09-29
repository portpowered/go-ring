package websocket

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/internal/generatedhttp"
	"github.com/portpowered/go-ring/internal/generatedsignaling"
	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/ringerrors"
)

type accountEventRecord struct {
	frame generatedsignaling.AccountEventFrame
	raw   map[string]interface{}
}

// EventConnection owns the transport, read loop, and event queue.
type EventConnection struct {
	conn   *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.RWMutex
	// receiveMu serializes consumers so events cannot overtake a terminal read error.
	receiveMu sync.Mutex
	closed    bool
	// pendingErr retains a consumed terminal error until queued events are drained.
	pendingErr  error
	messageChan chan accountEventRecord
	errChan     chan error
}

// OpenEvents connects the account event stream and starts its read loop.
func OpenEvents(ctx context.Context, wsURL, token, hardwareID string) (*EventConnection, error) {
	return OpenEventsWithDialer(ctx, wsURL, token, hardwareID, nil)
}

// OpenEventsWithDialer connects the account event stream using the supplied dialer.
// A nil dialer uses Gorilla's default dialer with a bounded handshake timeout.
func OpenEventsWithDialer(
	ctx context.Context,
	wsURL, token, hardwareID string,
	dialer Dialer,
) (*EventConnection, error) {
	parsed, _ := url.Parse(wsURL)

	var validationErr error

	if parsed != nil && parsed.Hostname() == "api.ring.com" {
		validationErr = protocol.ValidateWebSocketURL(protocol.AccountEventsChannel, wsURL)
	} else {
		validationErr = protocol.ValidateWebSocketOverride(wsURL)
	}

	if validationErr != nil {
		return nil, ringerrors.NewBadRequestError("invalid account event WebSocket URL", validationErr)
	}

	header := http.Header{}
	header.Set(string(generatedhttp.Authorization), "Bearer "+token)

	if hardwareID != "" {
		header.Set(string(generatedhttp.HardwareId), hardwareID)
	}

	if dialer == nil {
		dialer = &websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	}

	conn, response, err := dialer.DialContext(ctx, wsURL, header)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		return nil, ringerrors.NewNetworkError("event websocket dial failed", err)
	}

	eventCtx, cancel := context.WithCancel(ctx)
	ec := &EventConnection{
		conn:        conn,
		ctx:         eventCtx,
		cancel:      cancel,
		messageChan: make(chan accountEventRecord, 100),
		errChan:     make(chan error, 10),
	}
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

// Receive receives events in wire order before a terminal read error.
// Concurrent callers are serialized to preserve that ordering.
func (ec *EventConnection) Receive() (map[string]interface{}, error) {
	event, err := ec.receiveRecord()
	if err != nil {
		return nil, err
	}

	return event.raw, nil
}

// ReceiveFrame returns the schema-generated event projection and its full raw payload.
func (ec *EventConnection) ReceiveFrame() (generatedsignaling.AccountEventFrame, map[string]interface{}, error) {
	event, err := ec.receiveRecord()
	if err != nil {
		return generatedsignaling.AccountEventFrame{}, nil, err
	}

	return event.frame, event.raw, nil
}

// Close closes the event connection.
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

func (ec *EventConnection) receiveRecord() (accountEventRecord, error) {
	ec.receiveMu.Lock()
	defer ec.receiveMu.Unlock()

	ec.mu.RLock()
	closed := ec.closed
	ec.mu.RUnlock()

	if closed {
		return accountEventRecord{}, ringerrors.NewClosedError("connection is closed")
	}

	if ec.pendingErr != nil {
		select {
		case event, ok := <-ec.messageChan:
			if !ok {
				return accountEventRecord{}, ringerrors.NewClosedError("message channel closed")
			}

			return event, nil
		default:
		}

		pendingErr := ec.pendingErr
		ec.pendingErr = nil

		return accountEventRecord{}, pendingErr
	}

	// Preserve wire order by draining already-decoded events before returning
	// the terminal read error that follows them.
	select {
	case event, ok := <-ec.messageChan:
		if !ok {
			return accountEventRecord{}, ringerrors.NewClosedError("message channel closed")
		}

		return event, nil
	default:
	}

	select {
	case event, ok := <-ec.messageChan:
		if !ok {
			return accountEventRecord{}, ringerrors.NewClosedError("message channel closed")
		}

		return event, nil
	case err, ok := <-ec.errChan:
		if !ok {
			return accountEventRecord{}, ringerrors.NewClosedError("error channel closed")
		}

		return ec.prioritizePendingEvent(err)
	case <-ec.ctx.Done():
		// A peer read failure queues its error before cancelling the context.
		// If both are ready, report the failure rather than a generic close.
		select {
		case err, ok := <-ec.errChan:
			if !ok {
				return accountEventRecord{}, ringerrors.NewClosedError("error channel closed")
			}

			return ec.prioritizePendingEvent(err)
		default:
		}

		select {
		case event, ok := <-ec.messageChan:
			if !ok {
				return accountEventRecord{}, ringerrors.NewClosedError("message channel closed")
			}

			return event, nil
		default:
		}

		return accountEventRecord{}, ringerrors.NewClosedError("context cancelled")
	}
}

func (ec *EventConnection) prioritizePendingEvent(readErr error) (accountEventRecord, error) {
	select {
	case event, ok := <-ec.messageChan:
		if !ok {
			return accountEventRecord{}, ringerrors.NewClosedError("message channel closed")
		}

		ec.pendingErr = readErr

		return event, nil
	default:
		return accountEventRecord{}, readErr
	}
}

// processMessages processes incoming WebSocket messages.
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

			var frame generatedsignaling.AccountEventFrame

			err = json.Unmarshal(message, &frame)
			if err != nil {
				continue
			}

			// Keep unknown fields for callers alongside the generated known fields.
			var eventData map[string]interface{}
			{
				err := json.Unmarshal(message, &eventData)
				if err != nil {
					continue
				}
			}

			select {
			case ec.messageChan <- accountEventRecord{frame: frame, raw: eventData}:
			case <-ec.ctx.Done():
				return
			}
		}
	}
}
