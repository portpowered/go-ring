package ring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/internal/signaling"
)

func (c *SignalingConnection) send(ctx context.Context, m signaling.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-c.done:
		return c.Err()
	default:
	}
	return c.writer.send(ctx, m)
}

func (c *SignalingConnection) writeFrame(ctx context.Context, m signaling.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(signaling.SendTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	netConn := c.conn.NetConn()
	// Gorilla caches write deadlines on Conn and reapplies the cached value
	// inside WriteMessage, so set both the cached deadline and the net.Conn.
	_ = c.conn.SetWriteDeadline(deadline)
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
	_ = c.conn.SetWriteDeadline(time.Time{})
	_ = netConn.SetWriteDeadline(time.Time{})
	if err != nil {
		if cause := ctx.Err(); cause != nil {
			return cause
		}
		// The socket deadline may fire before the context timer is scheduled.
		var timeout net.Error
		if d, ok := ctx.Deadline(); ok && !time.Now().Before(d) && errors.As(err, &timeout) && timeout.Timeout() {
			return context.DeadlineExceeded
		}
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
	channel := c.channels[m.DialogID]
	playback := c.playbacks[m.DialogID]
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
		return
	}
	if playback != nil {
		playback.handle(m)
		return
	}
	if channel != nil {
		select {
		case channel <- m:
		default:
			c.fail(fmt.Errorf("signaling event queue full"))
		}
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
		<-c.writer.finished
		return nil
	}
	c.closed = true
	children := make([]*DeviceSession, 0, len(c.sessions))
	for _, s := range c.sessions {
		children = append(children, s)
	}
	c.mu.Unlock()
	closeCtx, closeCancel := context.WithTimeout(context.Background(), signaling.CloseTimeout)
	defer closeCancel()
	for _, s := range children {
		_ = s.closeWithContext(closeCtx, true)
	}
	c.mu.Lock()
	c.terminal = signaling.ErrClosed
	close(c.done)
	c.mu.Unlock()
	c.cancel()
	_ = c.conn.Close()
	<-c.readerDone
	<-c.writer.finished
	c.client.removeSignaling(c)
	return nil
}
func (c *Client) removeSignaling(s *SignalingConnection) {
	c.mu.Lock()
	delete(c.signalingConnections, s)
	c.mu.Unlock()
}
