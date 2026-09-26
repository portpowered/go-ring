package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/internal/ringerrors"
	"github.com/portpowered/go-ring/internal/signaling"
)

// Dialer allows tests and callers to substitute the signaling transport.
type Dialer interface {
	DialContext(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)
}

// DialSignaling opens a bounded signaling socket with an optional custom dialer.
func DialSignaling(ctx context.Context, wsURL string, headers http.Header, dialer Dialer) (*websocket.Conn, error) {
	if dialer == nil {
		dialer = &websocket.Dialer{HandshakeTimeout: signaling.HandshakeTimeout}
	}
	conn, _, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		return nil, err
	}
	if conn == nil {
		return nil, ringerrors.NewConnectionError("signaling dialer returned an empty connection", nil)
	}
	conn.SetReadLimit(signaling.MaxMessageBytes)
	return conn, nil
}

// WriteSignaling sends one JSON frame with a context-sensitive write deadline.
func WriteSignaling(ctx context.Context, conn *websocket.Conn, message signaling.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(signaling.SendTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	netConn := conn.NetConn()
	// Gorilla reapplies its cached deadline inside WriteMessage.
	_ = conn.SetWriteDeadline(deadline)
	_ = netConn.SetWriteDeadline(deadline)
	cancelWriteDone := make(chan struct{})
	stopCancel := context.AfterFunc(ctx, func() { _ = netConn.SetWriteDeadline(time.Now()); close(cancelWriteDone) })
	encoded, err := json.Marshal(message)
	if err == nil {
		err = conn.WriteMessage(websocket.TextMessage, encoded)
	}
	if !stopCancel() {
		<-cancelWriteDone
	}
	_ = conn.SetWriteDeadline(time.Time{})
	_ = netConn.SetWriteDeadline(time.Time{})
	if err != nil {
		if cause := ctx.Err(); cause != nil {
			return cause
		}
		var timeout net.Error
		if d, ok := ctx.Deadline(); ok && !time.Now().Before(d) && errors.As(err, &timeout) && timeout.Timeout() {
			return context.DeadlineExceeded
		}
	}
	return err
}

// ReadSignaling reads text frames until a transport or decoding error occurs.
func ReadSignaling(conn *websocket.Conn, route func(signaling.Message)) error {
	for {
		typ, encoded, err := conn.ReadMessage()
		if err != nil {
			return ringerrors.NewConnectionError("signaling read failed", err)
		}
		if typ != websocket.TextMessage {
			continue
		}
		var message signaling.Message
		if err := json.Unmarshal(encoded, &message); err != nil {
			return ringerrors.NewConnectionError("invalid signaling message", err)
		}
		route(message)
	}
}
