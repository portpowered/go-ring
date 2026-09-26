package ring

import (
	"context"
	"fmt"

	"github.com/portpowered/go-ring/internal/signaling"
	dependencywebsocket "github.com/portpowered/go-ring/pkg/dependencies/websocket"
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
	return dependencywebsocket.WriteSignaling(ctx, c.conn, m)
}

func (c *SignalingConnection) readLoop() {
	defer close(c.readerDone)
	err := dependencywebsocket.ReadSignaling(c.conn, c.route)
	if err != nil && !c.isClosed() {
		c.fail(err)
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
