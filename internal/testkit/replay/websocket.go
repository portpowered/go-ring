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
	// Channel names an AsyncAPI channel for the production inventory gate.
	Channel string          `json:"channel,omitempty"`
	Kind    string          `json:"kind"`
	Frame   string          `json:"frame"`
	Body    json.RawMessage `json:"body"`
	// Template enables explicit $uuid:name and $ref:name bindings in JSON text frames.
	Template bool `json:"template,omitempty"`
}

// WSHandshake is the expected upgrade origin, host, path, query, and application headers.
// Empty Origin means the request must have no Origin header. Empty Host means
// the server's loopback host, which is assigned after the test server starts.
type WSHandshake struct {
	Origin  string      `json:"origin,omitempty"`
	Host    string      `json:"host,omitempty"`
	Path    string      `json:"path,omitempty"`
	Query   url.Values  `json:"query,omitempty"`
	Headers http.Header `json:"headers,omitempty"`
}

// WebSocketServer serves one scripted connection on a loopback httptest server.
type WebSocketServer struct {
	Server   *httptest.Server
	done     chan error
	steps    chan int
	mu       sync.Mutex
	accepted bool
	active   *websocket.Conn
	result   error
	lastStep int
	once     sync.Once
}

// NewWebSocketServer starts a strict scripted WebSocket peer. Each read has a bounded deadline.
func NewWebSocketServer(steps []WSStep, timeout time.Duration, handshakes ...WSHandshake) *WebSocketServer {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}

	server := &WebSocketServer{done: make(chan error, 1), steps: make(chan int, len(steps)), lastStep: -1}
	want := expectedHandshake(handshakes)

	up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool {
		return r.Header.Get("Origin") == want.Origin
	}}
	server.Server = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, request *http.Request) {
		handleWebSocketUpgrade(server, rw, request, up, want, steps, timeout)
	}))

	return server
}

func expectedHandshake(handshakes []WSHandshake) WSHandshake {
	want := WSHandshake{Path: "/", Query: url.Values{}}
	if len(handshakes) == 0 {
		return want
	}

	want = handshakes[0]
	if want.Query == nil {
		want.Query = url.Values{}
	}

	return want
}

func handleWebSocketUpgrade(
	server *WebSocketServer,
	rw http.ResponseWriter,
	request *http.Request,
	upgrader websocket.Upgrader,
	want WSHandshake,
	steps []WSStep,
	timeout time.Duration,
) {
	if !matchesHandshake(request, want, server.Server.Listener.Addr().String()) {
		server.finish(webSocketReplayError{message: "websocket replay: upgrade request mismatch"})
		http.Error(rw, "upgrade request mismatch", http.StatusBadRequest)

		return
	}

	if !claimWebSocketConnection(server) {
		http.Error(rw, "script supports one connection", http.StatusConflict)

		return
	}

	connection, err := upgrader.Upgrade(rw, request, nil)
	if err != nil {
		server.finish(err)

		return
	}

	server.mu.Lock()
	server.active = connection
	server.mu.Unlock()

	defer func() { _ = connection.Close() }()
	defer func() { server.mu.Lock(); server.active = nil; server.mu.Unlock() }()

	err = runWebSocketSteps(server, connection, steps, timeout)
	if err != nil {
		server.finish(err)

		return
	}

	err = checkTerminalFrame(connection, terminalFrameGraceFor(timeout))
	if err != nil {
		server.finish(err)

		return
	}

	server.finish(nil)
}

func claimWebSocketConnection(server *WebSocketServer) bool {
	server.mu.Lock()
	defer server.mu.Unlock()

	if server.accepted {
		return false
	}

	server.accepted = true

	return true
}

func runWebSocketSteps(
	server *WebSocketServer,
	connection *websocket.Conn,
	steps []WSStep,
	timeout time.Duration,
) error {
	bindings := make(map[string]string)

	for stepIndex, step := range steps {
		_ = connection.SetReadDeadline(time.Now().Add(timeout))

		err := runWebSocketStep(connection, stepIndex, step, timeout, bindings)
		if err != nil {
			return err
		}

		server.mu.Lock()
		server.lastStep = stepIndex
		server.mu.Unlock()

		server.steps <- stepIndex
	}

	return nil
}

func runWebSocketStep(
	connection *websocket.Conn,
	stepIndex int,
	step WSStep,
	timeout time.Duration,
	bindings map[string]string,
) error {
	switch step.Kind {
	case "expect":
		return expectWebSocketFrame(connection, stepIndex, step, bindings)
	case "send":
		return sendWebSocketFrame(connection, stepIndex, step, timeout, bindings)
	default:
		return webSocketReplayError{message: fmt.Sprintf("step %d: invalid kind %q", stepIndex, step.Kind)}
	}
}

func expectWebSocketFrame(
	connection *websocket.Conn,
	stepIndex int,
	step WSStep,
	bindings map[string]string,
) error {
	actualType, messageBytes, err := connection.ReadMessage()
	if err != nil {
		return webSocketReplayError{message: fmt.Sprintf("step %d read", stepIndex), cause: err}
	}

	wantType := messageType(step.Frame)

	matched := actualType == wantType && frameEqual(wantType, step.Body, messageBytes)

	if actualType == wantType && step.Template && wantType == websocket.TextMessage {
		matched = matchFrameTemplate(step.Body, messageBytes, bindings) == nil
	}

	if matched {
		return nil
	}

	return webSocketReplayError{
		message: fmt.Sprintf(
			"step %d expected %s frame %s, got %s frame %s",
			stepIndex,
			step.Frame,
			step.Body,
			frameName(actualType),
			messageBytes,
		),
	}
}

func sendWebSocketFrame(
	connection *websocket.Conn,
	stepIndex int,
	step WSStep,
	timeout time.Duration,
	bindings map[string]string,
) error {
	frameType := messageType(step.Frame)
	if frameType == 0 {
		return webSocketReplayError{message: fmt.Sprintf("step %d: unsupported frame %q", stepIndex, step.Frame)}
	}

	body := []byte(step.Body)

	if step.Template && frameType == websocket.TextMessage {
		var err error

		body, err = renderFrameTemplate(step.Body, bindings)
		if err != nil {
			return webSocketReplayError{message: fmt.Sprintf("step %d template", stepIndex), cause: err}
		}
	}

	_ = connection.SetWriteDeadline(time.Now().Add(timeout))

	err := connection.WriteMessage(frameType, body)
	if err != nil {
		return webSocketReplayError{message: fmt.Sprintf("step %d write", stepIndex), cause: err}
	}

	return nil
}

func terminalFrameGraceFor(timeout time.Duration) time.Duration {
	if timeout > terminalFrameGrace {
		return terminalFrameGrace
	}

	return timeout
}

// WaitStep waits until the strict peer has consumed step index, or reports its
// first mismatch. It lets tests trigger later client actions without sleeping.
func (w *WebSocketServer) WaitStep(index int, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = defaultWebSocketAssertTimeout
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case done := <-w.steps:
			if done >= index {
				return nil
			}
		case <-w.done:
			w.mu.Lock()
			complete := w.lastStep >= index
			w.mu.Unlock()

			if complete {
				return nil
			}

			err := w.AssertComplete(timeout)
			if err != nil {
				return err
			}

			return webSocketReplayError{message: "websocket replay: requested step was not reached"}
		case <-timer.C:
			return webSocketReplayError{message: "websocket replay: step did not complete before deadline"}
		}
	}
}
func matchesHandshake(request *http.Request, want WSHandshake, serverHost string) bool {
	host := want.Host
	if host == "" {
		host = serverHost
	}

	return request.Method == http.MethodGet && request.Host == host && request.Header.Get("Origin") == want.Origin &&
		request.URL.EscapedPath() == want.Path &&
		reflect.DeepEqual(request.URL.Query(), want.Query) &&
		headersMatch(want.Headers, request.Header, HeadersRequired)
}

func checkTerminalFrame(c *websocket.Conn, grace time.Duration) error {
	_ = c.SetReadDeadline(time.Now().Add(grace))

	frame, _, err := c.ReadMessage()
	if err == nil {
		return webSocketReplayError{
			message: fmt.Sprintf("unexpected frame %s after script completion", frameName(frame)),
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return nil
	}

	if errors.Is(err, io.EOF) ||
		websocket.IsCloseError(
			err,
			websocket.CloseNormalClosure,
			websocket.CloseGoingAway,
			websocket.CloseNoStatusReceived,
			websocket.CloseAbnormalClosure,
		) {
		return nil
	}

	return webSocketReplayError{message: "websocket replay: terminal read", cause: err}
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

func (w *WebSocketServer) finish(err error) {
	w.once.Do(func() { w.mu.Lock(); w.result = err; w.mu.Unlock(); close(w.done) })
}

// SemanticEqual compares JSON structure while retaining exact decimal number values.
func SemanticEqual(a, b []byte) bool { return semanticJSONEqual(a, b) }
