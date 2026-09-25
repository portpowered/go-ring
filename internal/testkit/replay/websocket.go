package replay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/gorilla/websocket"
)

// WSStep either expects a client frame or sends a server frame. Kind is "expect" or "send".
type WSStep struct {
	Kind  string          `json:"kind"`
	Frame string          `json:"frame"`
	Body  json.RawMessage `json:"body"`
}

// WebSocketServer serves one scripted connection on a loopback httptest server.
type WebSocketServer struct {
	Server *httptest.Server
	done   chan error
}

// NewWebSocketServer starts a strict scripted WebSocket peer. Each read has a bounded deadline.
func NewWebSocketServer(steps []WSStep, timeout time.Duration) *WebSocketServer {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	w := &WebSocketServer{done: make(chan error, 1)}
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	w.Server = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		c, e := up.Upgrade(rw, r, nil)
		if e != nil {
			w.done <- e
			return
		}
		defer c.Close()
		for i, s := range steps {
			_ = c.SetReadDeadline(time.Now().Add(timeout))
			if s.Kind == "expect" {
				mt, b, e := c.ReadMessage()
				if e != nil {
					w.done <- fmt.Errorf("step %d read: %w", i, e)
					return
				}
				want := messageType(s.Frame)
				if mt != want || !frameEqual(want, s.Body, b) {
					w.done <- fmt.Errorf("step %d expected %s frame %s, got %s frame %s", i, s.Frame, s.Body, string(frameName(mt)), b)
					return
				}
			} else if s.Kind == "send" {
				mt := messageType(s.Frame)
				if mt == 0 {
					w.done <- fmt.Errorf("step %d: unsupported frame %q", i, s.Frame)
					return
				}
				_ = c.SetWriteDeadline(time.Now().Add(timeout))
				if e := c.WriteMessage(mt, s.Body); e != nil {
					w.done <- fmt.Errorf("step %d write: %w", i, e)
					return
				}
			} else {
				w.done <- fmt.Errorf("step %d: invalid kind %q", i, s.Kind)
				return
			}
		}
		w.done <- nil
	}))
	return w
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
		timeout = 3 * time.Second
	}
	select {
	case e := <-w.done:
		return e
	case <-time.After(timeout):
		return fmt.Errorf("websocket replay: script did not complete before deadline")
	}
}

// SemanticEqual compares JSON structure while retaining exact decimal number values.
func SemanticEqual(a, b []byte) bool { return semanticJSONEqual(a, b) }
