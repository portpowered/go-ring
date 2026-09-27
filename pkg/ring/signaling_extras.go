package ring

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/signaling"
	"github.com/portpowered/go-ring/pkg/dependencies/webrtc"
	"github.com/portpowered/go-ring/pkg/generatedsignaling"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

const defaultPlaybackEntryPoint = "timeline"

func (c *SignalingConnection) registerChannel() (string, chan signaling.Message, error) {
	name := uuid.NewString()
	ch := make(chan signaling.Message, signaling.NegotiationQueueCapacity)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return "", nil, c.terminal
	}
	c.channels[name] = ch
	return name, ch, nil
}
func (c *SignalingConnection) removeChannel(name string) {
	c.mu.Lock()
	delete(c.channels, name)
	delete(c.pushes, name)
	c.mu.Unlock()
}
func (c *SignalingConnection) sendTyped(ctx context.Context, method string, dialog, riid string, body any) error {
	if riid == "" {
		riid = uuid.NewString()
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return ringapimodels.NewInternalServerError("failed to encode signaling message", err)
	}
	return c.send(ctx, signaling.Message{Method: method, DialogID: dialog, RIID: riid, Body: raw})
}

// SubscribePush registers captured shoulder-tap style notification filters.
// It keeps the subscription alive until Close or its parent connection ends.
func (c *SignalingConnection) SubscribePush(ctx context.Context, filters []PushFilter) (*PushSubscription, error) {
	if len(filters) == 0 {
		return nil, ringapimodels.NewBadRequestError("at least one push filter is required", nil)
	}
	dialog, events, err := c.registerChannel()
	if err != nil {
		return nil, err
	}
	wireFilters := make([]generatedsignaling.PushFilter, 0, len(filters))
	for _, filter := range filters {
		ids := make([]int, len(filter.Filters.DoorbotIDs))
		for i, id := range filter.Filters.DoorbotIDs {
			ids[i] = int(id)
		}
		wireFilters = append(wireFilters, generatedsignaling.PushFilter{FilterIdentifier: filter.FilterIdentifier, Filters: &generatedsignaling.PushFilters{DoorbotIds: ids}, NotificationScope: filter.NotificationScope, NotificationType: filter.NotificationType})
	}
	if err = c.sendTyped(ctx, protocol.MethodPushSubscribe, dialog, "", generatedsignaling.PushSubscribeBody{RequestedNotifications: wireFilters}); err != nil {
		c.removeChannel(dialog)
		return nil, err
	}
	for {
		select {
		case <-ctx.Done():
			c.removeChannel(dialog)
			return nil, ringapimodels.NewConnectionError("push subscription canceled", ctx.Err())
		case <-c.done:
			c.removeChannel(dialog)
			return nil, c.Err()
		case m := <-events:
			if m.Method != protocol.MethodPushSubscriptionAck {
				continue
			}
			var ack generatedsignaling.PushSubscriptionAckBody
			if json.Unmarshal(m.Body, &ack) != nil || ack.Status != protocol.SubscriptionStatusOK || ack.SubscriptionId == "" {
				c.removeChannel(dialog)
				return nil, ringapimodels.NewConnectionError("push subscription rejected", nil)
			}
			s := &PushSubscription{connection: c, dialog: dialog, id: ack.SubscriptionId, events: events, done: make(chan struct{})}
			c.mu.Lock()
			if c.closed {
				c.mu.Unlock()
				return nil, c.Err()
			}
			if c.pushes == nil {
				c.pushes = make(map[string]*PushSubscription)
			}
			c.pushes[dialog] = s
			c.mu.Unlock()
			go s.heartbeat(c.ctx) //nolint:contextcheck // The subscription belongs to the connection after the request is acknowledged.
			return s, nil
		}
	}
}
func (s *PushSubscription) heartbeat(ctx context.Context) {
	const pushHeartbeatInterval = 30 * time.Second
	s.heartbeatAt(ctx, pushHeartbeatInterval)
}
func (s *PushSubscription) heartbeatAt(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.done:
			return
		case <-s.connection.done:
			return
		case <-t.C:
			sendCtx, cancel := context.WithTimeout(ctx, signaling.SendTimeout)
			_ = s.connection.sendTyped(sendCtx, protocol.MethodPushHeartbeat, s.dialog, "", generatedsignaling.PushSubscriptionBody{SubscriptionId: s.id})
			cancel()
		}
	}
}
func (s *PushSubscription) Receive(ctx context.Context) (PushEvent, error) {
	for {
		select {
		case <-s.done:
			return PushEvent{}, sessionError("push subscription ended", s.terminal)
		default:
		}
		select {
		case <-ctx.Done():
			return PushEvent{}, ringapimodels.NewConnectionError("push receive canceled", ctx.Err())
		case <-s.done:
			return PushEvent{}, sessionError("push subscription ended", s.terminal)
		case <-s.connection.done:
			return PushEvent{}, s.connection.Err()
		case m := <-s.events:
			if m.Method != protocol.MethodPushEvent {
				continue
			}
			var event PushEvent
			if err := json.Unmarshal(m.Body, &event); err != nil {
				return PushEvent{}, ringapimodels.NewConnectionError("invalid push event", err)
			}
			if event.SubscriptionID != s.id {
				return PushEvent{}, ringapimodels.NewConnectionError("push subscription ID mismatch", nil)
			}
			return event, nil
		}
	}
}
func (s *PushSubscription) Close() error {
	var err error
	s.once.Do(func() {
		s.terminal = signaling.ErrClosed
		close(s.done)
		s.connection.removeChannel(s.dialog)
		ctx, cancel := context.WithTimeout(context.Background(), signaling.CloseTimeout)
		defer cancel()
		err = s.connection.sendTyped(ctx, protocol.MethodPushUnsubscribe, s.dialog, "", generatedsignaling.PushSubscriptionBody{SubscriptionId: s.id})
	})
	return err
}

func (s *PushSubscription) terminate(err error) {
	s.once.Do(func() {
		s.terminal = err
		close(s.done)
		s.connection.removeChannel(s.dialog)
	})
}

func (c *SignalingConnection) StartPlayback(ctx context.Context, req StartPlaybackRequest) (*PlaybackSession, error) {
	id, err := strconv.ParseInt(req.DeviceID, 10, 64)
	if err != nil || id <= 0 {
		return nil, ringapimodels.NewBadRequestError("device ID must be a positive integer", err)
	}
	if req.Offer.Type != SDPTypeOffer || req.Offer.SDP == "" {
		return nil, ringapimodels.NewBadRequestError("playback requires an SDP offer", nil)
	}
	if _, err = webrtc.ParseSDP(req.Offer.SDP); err != nil {
		return nil, ringapimodels.NewBadRequestError("invalid SDP offer", err)
	}
	entry := req.EntryPoint
	if entry == "" {
		entry = defaultPlaybackEntryPoint
	}
	dialog, events, err := c.registerChannel()
	if err != nil {
		return nil, err
	}
	if err = c.sendTyped(ctx, protocol.MethodPlayback, dialog, "", generatedsignaling.PlaybackOfferBody{DoorbotId: int(id), EntryPoint: entry, Sdp: req.Offer.SDP, ReservedType: "cloud"}); err != nil {
		c.removeChannel(dialog)
		return nil, err
	}
	deadline, cancel := context.WithTimeout(ctx, signaling.NegotiationTimeout)
	defer cancel()
	for {
		select {
		case <-deadline.Done():
			c.removeChannel(dialog)
			return nil, ringapimodels.NewConnectionError("playback negotiation timed out or was canceled", deadline.Err())
		case <-c.done:
			c.removeChannel(dialog)
			return nil, c.Err()
		case m := <-events:
			if m.Method != protocol.MethodSDP {
				continue
			}
			var body generatedsignaling.PlaybackAnswerBody
			if json.Unmarshal(m.Body, &body) != nil || int64(body.DoorbotId) != id || body.SessionId == "" || body.ReservedType != protocol.SDPTypeAnswer || body.Sdp == "" {
				c.removeChannel(dialog)
				return nil, ringapimodels.NewConnectionError("invalid playback SDP answer", nil)
			}
			s := &PlaybackSession{connection: c, dialog: dialog, riid: m.RIID, id: body.SessionId, deviceID: id, answer: SessionDescription{Type: SDPTypeAnswer, SDP: body.Sdp}, events: events, done: make(chan struct{})}
			s.lastPong.Store(time.Now().UnixNano())
			c.mu.Lock()
			if c.closed {
				c.mu.Unlock()
				return nil, c.Err()
			}
			c.playbacks[dialog] = s
			c.mu.Unlock()
			interval := signaling.DefaultHeartbeatInterval
			if body.SessionInfo != nil {
				interval = time.Duration(body.SessionInfo.PingInterval) * time.Second
			}
			if interval <= 0 || interval > signaling.MaxHeartbeatInterval {
				interval = signaling.DefaultHeartbeatInterval
			}
			go s.keepalive(c.ctx, interval) //nolint:contextcheck // Playback lifetime is owned by the signaling connection.
			return s, nil
		}
	}
}
func (s *PlaybackSession) Answer() SessionDescription { return s.answer }
func (s *PlaybackSession) handle(m signaling.Message) {
	if m.Method == protocol.MethodPong {
		var body generatedsignaling.SessionBody
		if json.Unmarshal(m.Body, &body) == nil && int64(body.DoorbotId) == s.deviceID && body.SessionId == s.id {
			s.lastPong.Store(time.Now().UnixNano())
		}
		return
	}
	if m.Method == protocol.MethodClose {
		s.terminate(signaling.ErrClosed)
		return
	}
	select {
	case s.events <- m:
	default:
		s.terminate(signaling.ErrBackpressure)
	}
}
func (s *PlaybackSession) terminate(err error) {
	s.once.Do(func() {
		s.terminal = err
		close(s.done)
		s.connection.mu.Lock()
		delete(s.connection.playbacks, s.dialog)
		delete(s.connection.channels, s.dialog)
		s.connection.mu.Unlock()
	})
}
func (s *PlaybackSession) keepalive(ctx context.Context, interval time.Duration) {
	s.keepaliveFor(ctx, interval, signaling.MaxSessionAge)
}
func (s *PlaybackSession) keepaliveFor(ctx context.Context, interval, lifetime time.Duration) {
	ping := time.NewTicker(interval)
	defer ping.Stop()
	expiry := time.NewTimer(lifetime)
	defer expiry.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.done:
			return
		case <-s.connection.done:
			return
		case <-expiry.C:
			closeCtx, cancel := context.WithTimeout(ctx, signaling.CloseTimeout)
			_ = s.closeWithContext(closeCtx)
			cancel()
			return
		case <-ping.C:
			if time.Since(time.Unix(0, s.lastPong.Load())) > 3*interval {
				s.terminate(signaling.ErrHeartbeat)
				return
			}
			sendCtx, cancel := context.WithTimeout(ctx, signaling.SendTimeout)
			_ = s.connection.sendTyped(sendCtx, protocol.MethodPing, s.dialog, s.riid, generatedsignaling.SessionBody{DoorbotId: int(s.deviceID), SessionId: s.id})
			cancel()
		}
	}
}
func (s *PlaybackSession) SendICE(ctx context.Context, candidate ICECandidateRequest) error {
	if candidate.Candidate == "" || candidate.MLineIndex < 0 {
		return ringapimodels.NewBadRequestError("invalid playback ICE candidate", nil)
	}
	return s.connection.sendTyped(ctx, protocol.MethodICE, s.dialog, s.riid, generatedsignaling.IceCandidateBody{DoorbotId: int(s.deviceID), SessionId: s.id, Ice: candidate.Candidate, Mlineindex: candidate.MLineIndex})
}
func (s *PlaybackSession) Receive(ctx context.Context) (SessionEvent, error) {
	select {
	case <-ctx.Done():
		return SessionEvent{}, ringapimodels.NewConnectionError("playback receive canceled", ctx.Err())
	case <-s.done:
		return SessionEvent{}, sessionError("playback session ended", s.terminal)
	case <-s.connection.done:
		return SessionEvent{}, s.connection.Err()
	case m := <-s.events:
		return SessionEvent{Method: m.Method, Body: m.Body}, nil
	}
}
func (s *PlaybackSession) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), signaling.CloseTimeout)
	defer cancel()
	return s.closeWithContext(ctx)
}

func (s *PlaybackSession) closeWithContext(ctx context.Context) error {
	var err error
	s.once.Do(func() {
		s.terminal = signaling.ErrClosed
		close(s.done)
		s.connection.mu.Lock()
		delete(s.connection.playbacks, s.dialog)
		delete(s.connection.channels, s.dialog)
		s.connection.mu.Unlock()
		err = s.connection.sendTyped(ctx, protocol.MethodClose, s.dialog, s.riid, generatedsignaling.PlaybackCloseBody{DoorbotId: int(s.deviceID), SessionId: s.id, Reason: &generatedsignaling.PlaybackCloseReason{Code: 0, Text: "client_closed"}})
	})
	return err
}
