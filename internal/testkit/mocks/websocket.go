package mocks

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// MockWebSocketConn is a mock WebSocket connection for testing
type MockWebSocketConn struct {
	mu            sync.RWMutex
	closed        bool
	writeMessages [][]byte
	readMessages  [][]byte
	readIndex     int
	readErr       error
	writeErr      error
	closeErr      error
	messageChan   chan []byte
	errorChan     chan error
}

// NewMockWebSocketConn creates a new mock WebSocket connection
func NewMockWebSocketConn() *MockWebSocketConn {
	return &MockWebSocketConn{
		writeMessages: make([][]byte, 0),
		readMessages:  make([][]byte, 0),
		messageChan:   make(chan []byte, 100),
		errorChan:     make(chan error, 10),
	}
}

// ReadMessage reads a message from the connection
func (m *MockWebSocketConn) ReadMessage() (messageType int, message []byte, err error) {
	m.mu.RLock()
	closed := m.closed
	readErr := m.readErr
	m.mu.RUnlock()

	if closed {
		return 0, nil, errors.New("connection closed")
	}

	if readErr != nil {
		return 0, nil, readErr
	}

	// Try to read from message channel first
	select {
	case msg := <-m.messageChan:
		return websocket.TextMessage, msg, nil
	case err := <-m.errorChan:
		return 0, nil, err
	case <-time.After(100 * time.Millisecond):
		// Timeout - check if there are pre-queued messages
		m.mu.RLock()
		if m.readIndex < len(m.readMessages) {
			msg := m.readMessages[m.readIndex]
			m.readIndex++
			m.mu.RUnlock()
			return websocket.TextMessage, msg, nil
		}
		m.mu.RUnlock()
		return 0, nil, errors.New("no message available")
	}
}

// WriteMessage writes a message to the connection
func (m *MockWebSocketConn) WriteMessage(messageType int, data []byte) error {
	m.mu.RLock()
	closed := m.closed
	writeErr := m.writeErr
	m.mu.RUnlock()

	if closed {
		return errors.New("connection closed")
	}

	if writeErr != nil {
		return writeErr
	}

	m.mu.Lock()
	m.writeMessages = append(m.writeMessages, data)
	m.mu.Unlock()

	return nil
}

// Close closes the connection
func (m *MockWebSocketConn) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return m.closeErr
	}

	m.closed = true
	close(m.messageChan)
	close(m.errorChan)
	return m.closeErr
}

// SetReadError sets an error to return on ReadMessage
func (m *MockWebSocketConn) SetReadError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readErr = err
}

// SetWriteError sets an error to return on WriteMessage
func (m *MockWebSocketConn) SetWriteError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writeErr = err
}

// SetCloseError sets an error to return on Close
func (m *MockWebSocketConn) SetCloseError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeErr = err
}

// QueueMessage queues a message to be read
func (m *MockWebSocketConn) QueueMessage(message []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readMessages = append(m.readMessages, message)
}

// QueueJSONMessage queues a JSON message to be read
func (m *MockWebSocketConn) QueueJSONMessage(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	m.QueueMessage(data)
	return nil
}

// SendMessage sends a message through the message channel
func (m *MockWebSocketConn) SendMessage(message []byte) {
	select {
	case m.messageChan <- message:
	default:
		// Channel full, queue it instead
		m.QueueMessage(message)
	}
}

// SendError sends an error through the error channel
func (m *MockWebSocketConn) SendError(err error) {
	select {
	case m.errorChan <- err:
	default:
		// Channel full, ignore
	}
}

// GetWrittenMessages returns all messages written to the connection
func (m *MockWebSocketConn) GetWrittenMessages() [][]byte {
	m.mu.RLock()
	defer m.mu.RUnlock()
	messages := make([][]byte, len(m.writeMessages))
	copy(messages, m.writeMessages)
	return messages
}

// ClearWrittenMessages clears the written messages history
func (m *MockWebSocketConn) ClearWrittenMessages() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writeMessages = make([][]byte, 0)
}

// IsClosed returns whether the connection is closed
func (m *MockWebSocketConn) IsClosed() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.closed
}

// MockWebSocketDialer is a mock WebSocket dialer for testing
type MockWebSocketDialer struct {
	mu          sync.RWMutex
	connections map[string]*MockWebSocketConn
	defaultConn *MockWebSocketConn
}

// NewMockWebSocketDialer creates a new mock WebSocket dialer
func NewMockWebSocketDialer() *MockWebSocketDialer {
	return &MockWebSocketDialer{
		connections: make(map[string]*MockWebSocketConn),
	}
}

// DialContext mocks the WebSocket dial operation
func (m *MockWebSocketDialer) DialContext(ctx context.Context, urlStr string, requestHeader map[string][]string) (*MockWebSocketConn, error) {
	m.mu.RLock()
	conn, exists := m.connections[urlStr]
	m.mu.RUnlock()

	if exists {
		return conn, nil
	}

	if m.defaultConn != nil {
		return m.defaultConn, nil
	}

	// Create a new connection
	conn = NewMockWebSocketConn()
	m.mu.Lock()
	m.connections[urlStr] = conn
	m.mu.Unlock()

	return conn, nil
}

// SetConnection sets a connection for a specific URL
func (m *MockWebSocketDialer) SetConnection(urlStr string, conn *MockWebSocketConn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connections[urlStr] = conn
}

// SetDefaultConnection sets a default connection to use
func (m *MockWebSocketDialer) SetDefaultConnection(conn *MockWebSocketConn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.defaultConn = conn
}

// GetConnection returns the connection for a URL
func (m *MockWebSocketDialer) GetConnection(urlStr string) *MockWebSocketConn {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.connections[urlStr]
}
