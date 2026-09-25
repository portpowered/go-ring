package ring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/signaling"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// WebSocketDialer is the small portion of Gorilla's dialer used by a signaling connection.
type WebSocketDialer interface {
	DialContext(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)
}
type WithSignalingDialerOption struct{ Dialer WebSocketDialer }

func WithWebSocketDialer(d WebSocketDialer) Option { return WithSignalingDialerOption{Dialer: d} }
func (o WithSignalingDialerOption) Apply(c *Client) error {
	if o.Dialer == nil {
		return errors.New("WebSocket dialer must not be nil")
	}
	c.signalingDialer = o.Dialer
	return nil
}

type OpenSignalingRequest struct{}

// SignalingConnection owns one authenticated signaling socket and its child device sessions.
type SignalingConnection struct {
	client     *Client
	conn       *websocket.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	writeGate  chan struct{}
	closed     bool
	terminal   error
	done       chan struct{}
	readerDone chan struct{}
	pending    map[string]chan signaling.Message
	sessions   map[string]*DeviceSession
}

// OpenSignaling obtains the currently supported legacy signaling ticket and opens its websocket.
// The captured C1 GET /api/v1/clap/tickets profile is intentionally not substituted for this POST route.
func (c *Client) OpenSignaling(ctx context.Context, _ OpenSignalingRequest) (*SignalingConnection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ringapimodels.NewClosedError("client is closed")
	}
	c.mu.Unlock()
	if err := c.ensureSession(ctx); err != nil {
		return nil, ringapimodels.NewConnectionError("failed to register Ring session", err)
	}
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, ringapimodels.NewTokenError("failed to get token for signaling", err)
	}
	if c.endpoints.SolutionsBaseURL == "" {
		return nil, ringapimodels.NewConnectionError("Solutions bootstrap URL is unverified for selected region; configure WithEndpoints", nil)
	}
	u := c.endpoints.SolutionsBaseURL + protocol.TicketPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return nil, ringapimodels.NewNetworkError("failed to create signaling ticket request", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Content-Type", "application/json")
	if c.hardwareID != "" {
		req.Header.Set("hardware_id", c.hardwareID)
	}
	resp, err := c.restClient.HTTPClient().Do(req)
	if err != nil {
		return nil, ringapimodels.NewNetworkError("failed to request signaling ticket", err)
	}
	var ticket struct {
		Ticket string `json:"ticket"`
	}
	b, readErr := io.ReadAll(io.LimitReader(resp.Body, signaling.MaxTicketResponseBytes+1))
	resp.Body.Close()
	if readErr != nil {
		return nil, ringapimodels.NewNetworkError("failed to read signaling ticket response", readErr)
	}
	if len(b) > signaling.MaxTicketResponseBytes {
		return nil, ringapimodels.NewBadRequestError("signaling ticket response exceeds size limit", nil)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, ringapimodels.NewHTTPError(resp, "signaling ticket request failed")
	}
	if err = json.Unmarshal(b, &ticket); err != nil {
		return nil, ringapimodels.NewBadRequestError("failed to decode signaling ticket response", err)
	}
	if ticket.Ticket == "" {
		return nil, ringapimodels.NewConnectionError("empty signaling ticket", nil)
	}
	clientID := uuid.NewString()
	wsURL := strings.Replace(c.rtcWebSocketURL, "{client_id}", clientID, 1)
	wsURL = strings.Replace(wsURL, "{token}", url.QueryEscape(ticket.Ticket), 1)
	dialer := c.signalingDialer
	if dialer == nil {
		d := websocket.Dialer{HandshakeTimeout: signaling.HandshakeTimeout}
		dialer = &d
	}
	h := http.Header{}
	h.Set("User-Agent", c.userAgent)
	if c.hardwareID != "" {
		h.Set("hardware_id", c.hardwareID)
	}
	conn, _, err := dialer.DialContext(ctx, wsURL, h)
	if err != nil {
		return nil, ringapimodels.NewConnectionError("failed to connect to signaling websocket", nil)
	}
	if conn == nil {
		return nil, ringapimodels.NewConnectionError("signaling dialer returned an empty connection", nil)
	}
	connCtx, cancel := context.WithCancel(ctx)
	conn.SetReadLimit(signaling.MaxMessageBytes)
	s := &SignalingConnection{client: c, conn: conn, ctx: connCtx, cancel: cancel, done: make(chan struct{}), readerDone: make(chan struct{}), pending: make(map[string]chan signaling.Message), sessions: make(map[string]*DeviceSession), writeGate: make(chan struct{}, 1)}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		cancel()
		conn.Close()
		return nil, ringapimodels.NewClosedError("client is closed")
	}
	if c.signalingConnections == nil {
		c.signalingConnections = make(map[*SignalingConnection]struct{})
	}
	c.signalingConnections[s] = struct{}{}
	c.mu.Unlock()
	go s.readLoop()
	go func() {
		select {
		case <-ctx.Done():
			s.fail(ctx.Err())
		case <-s.done:
		}
	}()
	return s, nil
}

func (c *SignalingConnection) send(ctx context.Context, m signaling.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-c.done:
		return c.Err()
	default:
	}
	select {
	case c.writeGate <- struct{}{}:
		defer func() { <-c.writeGate }()
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(signaling.SendTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	netConn := c.conn.NetConn()
	_ = netConn.SetWriteDeadline(deadline)
	cancelWriteDone := make(chan struct{})
	stopCancel := context.AfterFunc(ctx, func() { _ = netConn.SetWriteDeadline(time.Now()); close(cancelWriteDone) })
	b, err := json.Marshal(m)
	if err == nil {
		err = c.conn.WriteMessage(websocket.TextMessage, b)
	}
	if !stopCancel() {
		<-cancelWriteDone
	}
	_ = netConn.SetWriteDeadline(time.Time{})
	if err != nil {
		c.fail(fmt.Errorf("signaling write failed"))
	}
	return err
}

func (c *SignalingConnection) readLoop() {
	defer close(c.readerDone)
	for {
		typ, b, err := c.conn.ReadMessage()
		if err != nil {
			if !c.isClosed() {
				c.fail(fmt.Errorf("signaling read failed"))
			}
			return
		}
		if typ != websocket.TextMessage {
			continue
		}
		var m signaling.Message
		if err = json.Unmarshal(b, &m); err != nil {
			c.fail(fmt.Errorf("invalid signaling message"))
			return
		}
		c.route(m)
	}
}
func (c *SignalingConnection) route(m signaling.Message) {
	c.mu.Lock()
	pending := c.pending[m.DialogID]
	session := c.sessions[m.DialogID]
	c.mu.Unlock()
	if pending != nil {
		select {
		case pending <- m:
		default:
			c.fail(fmt.Errorf("signaling negotiation queue full"))
		}
		return
	}
	if session != nil {
		session.handle(m)
	}
}
func (c *SignalingConnection) isClosed() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.closed }
func (c *SignalingConnection) fail(err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.terminal = err
	close(c.done)
	sessions := make([]*DeviceSession, 0, len(c.sessions))
	for _, s := range c.sessions {
		sessions = append(sessions, s)
	}
	c.mu.Unlock()
	c.cancel()
	for _, s := range sessions {
		s.terminate(err)
	}
	_ = c.conn.Close()
	c.client.removeSignaling(c)
}
func (c *SignalingConnection) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminal != nil {
		return c.terminal
	}
	return signaling.ErrClosed
}
func (c *SignalingConnection) removeSession(dialog string) {
	c.mu.Lock()
	delete(c.sessions, dialog)
	delete(c.pending, dialog)
	c.mu.Unlock()
}
func (c *SignalingConnection) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		<-c.readerDone
		return nil
	}
	c.closed = true
	children := make([]*DeviceSession, 0, len(c.sessions))
	for _, s := range c.sessions {
		children = append(children, s)
	}
	c.mu.Unlock()
	for _, s := range children {
		_ = s.close(true)
	}
	c.mu.Lock()
	c.terminal = signaling.ErrClosed
	close(c.done)
	c.mu.Unlock()
	c.cancel()
	_ = c.conn.Close()
	<-c.readerDone
	c.client.removeSignaling(c)
	return nil
}
func (c *Client) removeSignaling(s *SignalingConnection) {
	c.mu.Lock()
	delete(c.signalingConnections, s)
	c.mu.Unlock()
}
