package replay

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const defaultWebSocketAssertTimeout = 3 * time.Second
const terminalFrameGrace = 50 * time.Millisecond

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
	// Template enables explicit $uuid:name and $ref:name bindings in JSON text frames.
	Template bool `json:"template,omitempty"`
}

// WSHandshake is the expected upgrade origin, host, path, query, and application headers.
// Empty Origin means the request must have no Origin header. Empty Host means
// the server's loopback host, which is assigned after the test server starts.
type WSHandshake struct {
	Origin  string
	Host    string
	Path    string
	Query   url.Values
	Headers http.Header
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
func NewWebSocketServer(steps []WSStep, timeout time.Duration, handshakes ...WSHandshake) *WebSocketServer {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	w := &WebSocketServer{done: make(chan error, 1)}
	want := WSHandshake{Path: "/", Query: url.Values{}}
	if len(handshakes) > 0 {
		want = handshakes[0]
		if want.Query == nil {
			want.Query = url.Values{}
		}
	}
	up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool {
		return r.Header.Get("Origin") == want.Origin
	}}
	w.Server = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if !matchesHandshake(r, want, w.Server.Listener.Addr().String()) {
			w.finish(webSocketReplayError{message: "websocket replay: upgrade request mismatch"})
			http.Error(rw, "upgrade request mismatch", http.StatusBadRequest)
			return
		}
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
		bindings := make(map[string]string)
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
				matched := mt == want && frameEqual(want, s.Body, b)
				if mt == want && s.Template && want == websocket.TextMessage {
					matched = matchFrameTemplate(s.Body, b, bindings) == nil
				}
				if !matched {
					w.finish(webSocketReplayError{message: fmt.Sprintf("step %d expected %s frame %s, got %s frame %s", i, s.Frame, s.Body, frameName(mt), b)})
					return
				}
			case "send":
				mt := messageType(s.Frame)
				if mt == 0 {
					w.finish(webSocketReplayError{message: fmt.Sprintf("step %d: unsupported frame %q", i, s.Frame)})
					return
				}
				body := []byte(s.Body)
				if s.Template && mt == websocket.TextMessage {
					var e error
					body, e = renderFrameTemplate(s.Body, bindings)
					if e != nil {
						w.finish(webSocketReplayError{message: fmt.Sprintf("step %d template", i), cause: e})
						return
					}
				}
				_ = c.SetWriteDeadline(time.Now().Add(timeout))
				if e := c.WriteMessage(mt, body); e != nil {
					w.finish(webSocketReplayError{message: fmt.Sprintf("step %d write", i), cause: e})
					return
				}
			default:
				w.finish(webSocketReplayError{message: fmt.Sprintf("step %d: invalid kind %q", i, s.Kind)})
				return
			}
		}
		grace := timeout
		if grace > terminalFrameGrace {
			grace = terminalFrameGrace
		}
		if err := checkTerminalFrame(c, grace); err != nil {
			w.finish(err)
			return
		}
		w.finish(nil)
	}))
	return w
}
func matchesHandshake(r *http.Request, want WSHandshake, serverHost string) bool {
	host := want.Host
	if host == "" {
		host = serverHost
	}
	return r.Method == http.MethodGet && r.Host == host && r.Header.Get("Origin") == want.Origin && r.URL.EscapedPath() == want.Path && reflect.DeepEqual(r.URL.Query(), want.Query) && headersMatch(want.Headers, r.Header, HeadersRequired)
}

func checkTerminalFrame(c *websocket.Conn, grace time.Duration) error {
	_ = c.SetReadDeadline(time.Now().Add(grace))
	frame, _, err := c.ReadMessage()
	if err == nil {
		return webSocketReplayError{message: fmt.Sprintf("unexpected frame %s after script completion", frameName(frame))}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return nil
	}
	if errors.Is(err, io.EOF) || websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived, websocket.CloseAbnormalClosure) {
		return nil
	}
	return webSocketReplayError{message: "websocket replay: terminal read", cause: err}
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
