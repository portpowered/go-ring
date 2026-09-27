package replay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const defaultWebSocketAssertTimeout = 3 * time.Second

type webSocketReplayError struct {
	message string
	cause   error
}

func (e webSocketReplayError) Error() string {
	if e.cause != nil {
		return e.message + ": " + e.cause.Error()
	}
	return e.message
}

func (e webSocketReplayError) Unwrap() error { return e.cause }

// WSStep either expects a client frame or sends a server frame. Kind is "expect" or "send".
type WSStep struct {
	Kind  string          `json:"kind"`
	Frame string          `json:"frame"`
	Body  json.RawMessage `json:"body"`
}

// WebSocketServer serves one scripted connection on a loopback httptest server.
type WebSocketServer struct {
	Server   *httptest.Server
	done     chan error
	mu       sync.Mutex
	accepted bool
	active   *websocket.Conn
	result   error
	once     sync.Once
}

// NewWebSocketServer starts a strict scripted WebSocket peer. Each read has a bounded deadline.
func NewWebSocketServer(steps []WSStep, timeout time.Duration) *WebSocketServer {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	w := &WebSocketServer{done: make(chan error, 1)}
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	w.Server = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		if w.accepted {
			w.mu.Unlock()
			http.Error(rw, "script supports one connection", http.StatusConflict)
			return
		}
		w.accepted = true
		w.mu.Unlock()
		c, e := up.Upgrade(rw, r, nil)
		if e != nil {
			w.finish(e)
			return
		}
		w.mu.Lock()
		w.active = c
		w.mu.Unlock()
		defer func() { _ = c.Close() }()
		defer func() { w.mu.Lock(); w.active = nil; w.mu.Unlock() }()
		for i, s := range steps {
			_ = c.SetReadDeadline(time.Now().Add(timeout))
			switch s.Kind {
			case "expect":
				mt, b, e := c.ReadMessage()
				if e != nil {
					w.finish(webSocketReplayError{message: fmt.Sprintf("step %d read", i), cause: e})
					return
				}
				want := messageType(s.Frame)
				if mt != want || !frameEqual(want, s.Body, b) {
					w.finish(webSocketReplayError{message: fmt.Sprintf("step %d expected %s frame %s, got %s frame %s", i, s.Frame, s.Body, frameName(mt), b)})
					return
				}
			case "send":
				mt := messageType(s.Frame)
				if mt == 0 {
					w.finish(webSocketReplayError{message: fmt.Sprintf("step %d: unsupported frame %q", i, s.Frame)})
					return
				}
				_ = c.SetWriteDeadline(time.Now().Add(timeout))
				if e := c.WriteMessage(mt, s.Body); e != nil {
					w.finish(webSocketReplayError{message: fmt.Sprintf("step %d write", i), cause: e})
					return
				}
			default:
				w.finish(webSocketReplayError{message: fmt.Sprintf("step %d: invalid kind %q", i, s.Kind)})
				return
			}
		}
		w.finish(nil)
	}))
	return w
}
func (w *WebSocketServer) finish(err error) {
	w.once.Do(func() { w.mu.Lock(); w.result = err; w.mu.Unlock(); close(w.done) })
}
func messageType(s string) int {
	switch s {
	case "text":
		return websocket.TextMessage
	case "binary":
		return websocket.BinaryMessage
	default:
		return 0
	}
}
func frameName(t int) string {
	switch t {
	case websocket.TextMessage:
		return "text"
	case websocket.BinaryMessage:
		return "binary"
	}
	return "other"
}
func frameEqual(t int, want, got []byte) bool {
	if t == websocket.TextMessage {
		return semanticJSONEqual(want, got)
	}
	return bytes.Equal(want, got)
}

// URL returns the local websocket URL for a client dialer.
func (w *WebSocketServer) URL() string { return "ws" + w.Server.URL[len("http"):] }

// AssertComplete waits for the script and reports mismatch, disconnect, or timeout.
func (w *WebSocketServer) AssertComplete(timeout time.Duration) error {
	if timeout <= 0 {
		timeout = defaultWebSocketAssertTimeout
	}
	select {
	case <-w.done:
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.result
	case <-time.After(timeout):
		return webSocketReplayError{message: "websocket replay: script did not complete before deadline"}
	}
}

// Close stops the server and closes an active client connection so shutdown remains bounded.
func (w *WebSocketServer) Close() {
	w.mu.Lock()
	c := w.active
	w.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
	w.Server.Close()
}

// SemanticEqual compares JSON structure while retaining exact decimal number values.
func SemanticEqual(a, b []byte) bool { return semanticJSONEqual(a, b) }
