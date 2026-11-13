package ring

import (
	"context"

	"github.com/portpowered/go-ring/internal/signaling"
	dependencywebsocket "github.com/portpowered/go-ring/pkg/dependencies/websocket"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

func (c *SignalingConnection) send(ctx context.Context, message signaling.Message) error {
	err := ctx.Err()
	if err != nil {
		return sessionError("signaling send canceled", err)
	}

	select {
	case <-c.done:
		return c.Err()
	default:
	}

	return sessionError("signaling send failed", c.writer.Send(ctx, message))
}

func (c *SignalingConnection) writeFrame(ctx context.Context, m signaling.Message) error {
	return dependencywebsocket.WriteSignaling(ctx, c.conn, m)
}

func (c *SignalingConnection) readLoop() {
	defer close(c.readerDone)

	err := dependencywebsocket.ReadSignaling(c.conn, c.route)
	if err != nil && !c.isClosed() {
		c.fail(sessionError("signaling read failed", err))
	}
}
func (c *SignalingConnection) route(message signaling.Message) {
	c.mu.Lock()
	pending := c.pending[message.DialogID]
	session := c.sessions[message.DialogID]
	channel := c.channels[message.DialogID]
	playback := c.playbacks[message.DialogID]
	push := c.pushes[message.DialogID]

	if pending != nil {
		// Enqueue under the routing lock so the activation handoff cannot drain
		// the old channel before a reader holding its previous route publishes.
		queued := false

		select {
		case pending <- message:
			queued = true
		default:
		}

		c.mu.Unlock()

		if !queued {
			c.fail(ringapimodels.NewConnectionError("signaling negotiation queue full", nil))
		}

		return
	}

	c.mu.Unlock()

	if session != nil {
		session.handle(message)

		return
	}

	if playback != nil {
		playback.handle(message)

		return
	}

	if channel != nil {
		select {
		case channel <- message:
		default:
			if push != nil {
				push.terminate(ringapimodels.NewConnectionError("push event queue full", signaling.ErrBackpressure))
			} else {
				// An unowned channel is still negotiating. Its waiter cannot
				// recover from a dropped protocol message.
				c.fail(ringapimodels.NewConnectionError("signaling negotiation queue full", signaling.ErrBackpressure))
			}
		}
	}
}
func (c *SignalingConnection) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.closed
}
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

	playbacks := make([]*PlaybackSession, 0, len(c.playbacks))
	for _, s := range c.playbacks {
		playbacks = append(playbacks, s)
	}

	pushes := make([]*PushSubscription, 0, len(c.pushes))
	for _, s := range c.pushes {
		pushes = append(pushes, s)
	}

	c.mu.Unlock()
	c.cancel()

	for _, s := range sessions {
		s.terminate(err)
	}

	for _, s := range playbacks {
		s.terminate(err)
	}

	for _, s := range pushes {
		s.terminate(err)
	}

	_ = c.conn.Close()
}
func (c *SignalingConnection) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.terminal != nil {
		return sessionError("signaling connection ended", c.terminal)
	}

	return ringapimodels.NewClosedError("signaling connection is closed", signaling.ErrClosed)
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
		<-c.writer.Finished()

		return nil
	}

	c.closed = true

	children := make([]*DeviceSession, 0, len(c.sessions))

	for _, s := range c.sessions {
		children = append(children, s)
	}

	playbacks := make([]*PlaybackSession, 0, len(c.playbacks))
	for _, s := range c.playbacks {
		playbacks = append(playbacks, s)
	}

	pushes := make([]*PushSubscription, 0, len(c.pushes))
	for _, s := range c.pushes {
		pushes = append(pushes, s)
	}

	c.mu.Unlock()

	closeCtx, closeCancel := context.WithTimeout(context.Background(), signaling.CloseTimeout)
	defer closeCancel()

	for _, s := range children {
		s.closeWithContext(closeCtx, true)
	}

	c.mu.Lock()
	c.terminal = signaling.ErrClosed
	close(c.done)
	c.mu.Unlock()

	for _, s := range playbacks {
		s.terminate(signaling.ErrClosed)
	}

	for _, s := range pushes {
		s.terminate(signaling.ErrClosed)
	}

	c.cancel()
	_ = c.conn.Close()
	<-c.readerDone
	<-c.writer.Finished()

	return nil
}
