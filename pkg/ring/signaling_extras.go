package ring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/portpowered/go-ring/internal/signaling"
	"github.com/portpowered/go-ring/pkg/generatedapi"
)

// PushFilter is the captured subscription filter. Identifiers are caller
// supplied so one subscriber can distinguish multiple requested filters.
type PushFilter struct {
	FilterIdentifier string `json:"filter_identifier"`
	Filters          struct {
		DoorbotIDs []int64 `json:"doorbot_ids"`
	} `json:"filters"`
	NotificationScope string `json:"notification_scope"`
	NotificationType  string `json:"notification_type"`
}

type PushEvent struct {
	FilterIdentifiers []string        `json:"filter_identifiers"`
	IngestionTimeMS   int64           `json:"ingestion_time_ms"`
	NotificationScope string          `json:"notification_scope"`
	NotificationType  string          `json:"notification_type"`
	Payload           json.RawMessage `json:"payload"`
	SubscriptionID    string          `json:"subscription_id"`
}

type PushSubscription struct {
	connection *SignalingConnection
	dialog     string
	id         string
	events     chan signaling.Message
	done       chan struct{}
	once       sync.Once
}

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
	c.mu.Unlock()
}
func (c *SignalingConnection) sendTyped(ctx context.Context, method generatedapi.ClientMethod, dialog, riid string, body any) error {
	if riid == "" {
		riid = uuid.NewString()
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return c.send(ctx, signaling.Message{Method: string(method), DialogID: dialog, RIID: riid, Body: raw})
}

// SubscribePush registers captured shoulder-tap style notification filters.
// It keeps the subscription alive until Close or its parent connection ends.
func (c *SignalingConnection) SubscribePush(ctx context.Context, filters []PushFilter) (*PushSubscription, error) {
	if len(filters) == 0 {
		return nil, errors.New("at least one push filter is required")
	}
	dialog, events, err := c.registerChannel()
	if err != nil {
		return nil, err
	}
	if err = c.sendTyped(ctx, generatedapi.ClientPushSubscribe, dialog, "", map[string]any{"requested_notifications": filters}); err != nil {
		c.removeChannel(dialog)
		return nil, err
	}
	for {
		select {
		case <-ctx.Done():
			c.removeChannel(dialog)
			return nil, ctx.Err()
		case <-c.done:
			c.removeChannel(dialog)
			return nil, c.Err()
		case m := <-events:
			if m.Method != string(generatedapi.ServerPushSubscriptionAck) {
				continue
			}
			var ack struct {
				Status         string `json:"status"`
				SubscriptionID string `json:"subscription_id"`
			}
			if json.Unmarshal(m.Body, &ack) != nil || ack.Status != "ok" || ack.SubscriptionID == "" {
				c.removeChannel(dialog)
				return nil, fmt.Errorf("push subscription rejected: %s", m.Body)
			}
			s := &PushSubscription{connection: c, dialog: dialog, id: ack.SubscriptionID, events: events, done: make(chan struct{})}
			go s.heartbeat()
			return s, nil
		}
	}
}
func (s *PushSubscription) heartbeat() {
	s.heartbeatAt(30 * time.Second)
}
func (s *PushSubscription) heartbeatAt(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-s.connection.done:
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(s.connection.ctx, signaling.SendTimeout)
			_ = s.connection.sendTyped(ctx, generatedapi.ClientPushHeartbeat, s.dialog, "", map[string]string{"subscription_id": s.id})
			cancel()
		}
	}
}
func (s *PushSubscription) Receive(ctx context.Context) (PushEvent, error) {
	for {
		select {
		case <-ctx.Done():
			return PushEvent{}, ctx.Err()
		case <-s.done:
			return PushEvent{}, signaling.ErrClosed
		case <-s.connection.done:
			return PushEvent{}, s.connection.Err()
		case m := <-s.events:
			if m.Method != string(generatedapi.ServerPushEvent) {
				continue
			}
			var event PushEvent
			if err := json.Unmarshal(m.Body, &event); err != nil {
				return PushEvent{}, err
			}
			if event.SubscriptionID != s.id {
				return PushEvent{}, fmt.Errorf("push subscription ID mismatch")
			}
			return event, nil
		}
	}
}
func (s *PushSubscription) Close() error {
	var err error
	s.once.Do(func() {
		close(s.done)
		s.connection.removeChannel(s.dialog)
		ctx, cancel := context.WithTimeout(context.Background(), signaling.CloseTimeout)
		defer cancel()
		err = s.connection.sendTyped(ctx, generatedapi.ClientPushUnsubscribe, s.dialog, "", map[string]string{"subscription_id": s.id})
	})
	return err
}

type StartPlaybackRequest struct {
	DeviceID   string
	Offer      SessionDescription
	EntryPoint string
}

// PlaybackSession is a cloud playback negotiation on a signaling connection.
// Live camera controls remain on DeviceSession.
type PlaybackSession struct {
	connection *SignalingConnection
	dialog     string
	riid       string
	id         string
	deviceID   int64
	answer     SessionDescription
	events     chan signaling.Message
	done       chan struct{}
	once       sync.Once
	lastPong   atomic.Int64
}

func (c *SignalingConnection) StartPlayback(ctx context.Context, req StartPlaybackRequest) (*PlaybackSession, error) {
	id, err := strconv.ParseInt(req.DeviceID, 10, 64)
	if err != nil || id <= 0 {
		return nil, errors.New("device ID must be a positive integer")
	}
	if req.Offer.Type != "offer" || req.Offer.SDP == "" {
		return nil, errors.New("playback requires an SDP offer")
	}
	if _, err = signaling.ParseSDP(req.Offer.SDP); err != nil {
		return nil, fmt.Errorf("invalid SDP offer: %w", err)
	}
	entry := req.EntryPoint
	if entry == "" {
		entry = "timeline"
	}
	dialog, events, err := c.registerChannel()
	if err != nil {
		return nil, err
	}
	if err = c.sendTyped(ctx, generatedapi.ClientPlayback, dialog, "", map[string]any{"doorbot_id": id, "entry_point": entry, "sdp": req.Offer.SDP, "type": "cloud"}); err != nil {
		c.removeChannel(dialog)
		return nil, err
	}
	deadline, cancel := context.WithTimeout(ctx, signaling.NegotiationTimeout)
	defer cancel()
	for {
		select {
		case <-deadline.Done():
			c.removeChannel(dialog)
			return nil, deadline.Err()
		case <-c.done:
			c.removeChannel(dialog)
			return nil, c.Err()
		case m := <-events:
			if m.Method != string(generatedapi.ServerSdp) {
				continue
			}
			var body struct {
				DeviceID    int64  `json:"doorbot_id"`
				SessionID   string `json:"session_id"`
				SDP         string `json:"sdp"`
				Type        string `json:"type"`
				SessionInfo struct {
					PingInterval int `json:"ping_interval"`
				} `json:"session_info"`
			}
			if json.Unmarshal(m.Body, &body) != nil || body.DeviceID != id || body.SessionID == "" || body.Type != "answer" || body.SDP == "" {
				c.removeChannel(dialog)
				return nil, errors.New("invalid playback SDP answer")
			}
			s := &PlaybackSession{connection: c, dialog: dialog, riid: m.RIID, id: body.SessionID, deviceID: id, answer: SessionDescription{Type: "answer", SDP: body.SDP}, events: events, done: make(chan struct{})}
			s.lastPong.Store(time.Now().UnixNano())
			c.mu.Lock()
			c.playbacks[dialog] = s
			c.mu.Unlock()
			interval := time.Duration(body.SessionInfo.PingInterval) * time.Second
			if interval <= 0 || interval > signaling.MaxHeartbeatInterval {
				interval = signaling.DefaultHeartbeatInterval
			}
			go s.keepalive(interval)
			return s, nil
		}
	}
}
func (s *PlaybackSession) Answer() SessionDescription { return s.answer }
func (s *PlaybackSession) handle(m signaling.Message) {
	if m.Method == string(generatedapi.ServerPong) {
		var body struct {
			DeviceID  int64  `json:"doorbot_id"`
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(m.Body, &body) == nil && body.DeviceID == s.deviceID && body.SessionID == s.id {
			s.lastPong.Store(time.Now().UnixNano())
		}
		return
	}
	if m.Method == string(generatedapi.ServerClose) {
		s.terminate()
		return
	}
	select {
	case s.events <- m:
	default:
		s.terminate()
	}
}
func (s *PlaybackSession) terminate() {
	s.once.Do(func() {
		close(s.done)
		s.connection.mu.Lock()
		delete(s.connection.playbacks, s.dialog)
		delete(s.connection.channels, s.dialog)
		s.connection.mu.Unlock()
	})
}
func (s *PlaybackSession) keepalive(interval time.Duration) {
	ping := time.NewTicker(interval)
	defer ping.Stop()
	expiry := time.NewTimer(signaling.MaxSessionAge)
	defer expiry.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-s.connection.done:
			return
		case <-expiry.C:
			_ = s.Close()
			return
		case <-ping.C:
			if time.Since(time.Unix(0, s.lastPong.Load())) > 3*interval {
				s.terminate()
				return
			}
			ctx, cancel := context.WithTimeout(s.connection.ctx, signaling.SendTimeout)
			_ = s.connection.sendTyped(ctx, generatedapi.ClientPing, s.dialog, s.riid, map[string]any{"doorbot_id": s.deviceID, "session_id": s.id})
			cancel()
		}
	}
}
func (s *PlaybackSession) SendICE(ctx context.Context, candidate ICECandidateRequest) error {
	return s.connection.sendTyped(ctx, generatedapi.ClientIce, s.dialog, s.riid, map[string]any{"doorbot_id": s.deviceID, "session_id": s.id, "ice": candidate.Candidate, "mlineindex": candidate.MLineIndex})
}
func (s *PlaybackSession) Receive(ctx context.Context) (SessionEvent, error) {
	select {
	case <-ctx.Done():
		return SessionEvent{}, ctx.Err()
	case <-s.done:
		return SessionEvent{}, signaling.ErrClosed
	case <-s.connection.done:
		return SessionEvent{}, s.connection.Err()
	case m := <-s.events:
		return SessionEvent{Method: m.Method, Body: m.Body}, nil
	}
}
func (s *PlaybackSession) Close() error {
	var err error
	s.once.Do(func() {
		close(s.done)
		s.connection.mu.Lock()
		delete(s.connection.playbacks, s.dialog)
		delete(s.connection.channels, s.dialog)
		s.connection.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), signaling.CloseTimeout)
		defer cancel()
		err = s.connection.sendTyped(ctx, generatedapi.ClientClose, s.dialog, s.riid, map[string]any{"doorbot_id": s.deviceID, "session_id": s.id, "reason": map[string]any{"code": 0, "text": "client_closed"}})
	})
	return err
}
