package websocket

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/ringerrors"
	"github.com/portpowered/go-ring/internal/signaling"
)

// Dialer allows tests and callers to substitute a WebSocket transport.
type Dialer interface {
	DialContext(ctx context.Context, wsURL string, headers http.Header) (*websocket.Conn, *http.Response, error)
}

// DialSignaling opens a bounded signaling socket with an optional custom dialer.
func DialSignaling(ctx context.Context, wsURL string, headers http.Header, dialer Dialer) (*websocket.Conn, error) {
	err := validateSignalingDialURL(wsURL)
	if err != nil {
		return nil, ringerrors.NewBadRequestError("invalid signaling WebSocket URL", err)
	}

	if dialer == nil {
		dialer = &websocket.Dialer{HandshakeTimeout: signaling.HandshakeTimeout}
	}

	conn, response, err := dialer.DialContext(ctx, wsURL, headers)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		return nil, ringerrors.NewNetworkError("signaling websocket dial failed", err)
	}

	if conn == nil {
		return nil, ringerrors.NewConnectionError("signaling dialer returned an empty connection", nil)
	}

	conn.SetReadLimit(signaling.MaxMessageBytes)

	return conn, nil
}

func validateSignalingDialURL(wsURL string) error {
	parsed, err := url.Parse(wsURL)
	if err != nil {
		return ringerrors.NewBadRequestError("parse signaling WebSocket URL", err)
	}

	isModeledOrigin := strings.EqualFold(parsed.Scheme, "wss") &&
		strings.EqualFold(parsed.Hostname(), "api.prod.signalling.ring.devices.a2z.com") &&
		(parsed.Port() == "" || parsed.Port() == "443") && parsed.EscapedPath() == "/ws"
	if isModeledOrigin {
		err = protocol.ValidateWebSocketURL(protocol.SignalingChannel, wsURL)
		if err != nil {
			return ringerrors.NewBadRequestError("validate modeled signaling WebSocket URL", err)
		}

		return nil
	}

	err = protocol.ValidateWebSocketOverride(wsURL)
	if err != nil {
		return ringerrors.NewBadRequestError("validate signaling WebSocket override", err)
	}

	return nil
}

// WriteSignaling sends one JSON frame with a context-sensitive write deadline.
func WriteSignaling(ctx context.Context, conn *websocket.Conn, message signaling.Message) error {
	{
		err := ctx.Err()
		if err != nil {
			return wrapSignalingContextError(err)
		}
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

	encoded, writeErr := marshalSignalingFrame(message)

	encodingFailed := writeErr != nil
	if !encodingFailed {
		writeErr = conn.WriteMessage(websocket.TextMessage, encoded)
	}

	if !stopCancel() {
		<-cancelWriteDone
	}

	_ = conn.SetWriteDeadline(time.Time{})
	_ = netConn.SetWriteDeadline(time.Time{})

	if writeErr != nil {
		cause := ctx.Err()
		if cause != nil {
			return wrapSignalingContextError(cause)
		}

		var timeout net.Error
		if d, ok := ctx.Deadline(); ok && !time.Now().Before(d) && errors.As(writeErr, &timeout) && timeout.Timeout() {
			return wrapSignalingContextError(context.DeadlineExceeded)
		}

		if encodingFailed {
			return ringerrors.NewBadRequestError("encode signaling message", writeErr)
		}

		return ringerrors.NewConnectionError("write signaling message", writeErr)
	}

	return nil
}

func wrapSignalingContextError(cause error) error {
	if cause == nil {
		return nil
	}

	return ringerrors.NewConnectionError("signaling write canceled", cause)
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

		message, err := unmarshalSignalingFrame(encoded)
		if err != nil {
			return ringerrors.NewConnectionError("invalid signaling message", err)
		}

		route(message)
	}
}
