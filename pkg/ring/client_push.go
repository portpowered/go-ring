package ring

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"

	"github.com/portpowered/go-ring/pkg/dependencies/push"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

const fcmEventBufferSize = 16

// ConnectPushRequest creates a caller-scoped FCM connection. Credentials come
// from a previous credentials event and are never stored on Client.
type ConnectPushRequest struct {
	Auth        AuthContext
	Credentials json.RawMessage
	DeviceIDs   []string
	Ding        bool
	Motion      bool
}

type RegisterPushDeviceRequest struct {
	Auth  AuthContext
	Token string
}

func (c *Client) RegisterPushDevice(ctx context.Context, req RegisterPushDeviceRequest) error {
	if req.Token == "" {
		return ringapimodels.NewBadRequestError("FCM token is required", nil)
	}
	ctx = c.accountContext(ctx, req.Auth)
	if err := c.ensureSession(ctx); err != nil {
		return err
	}
	return c.restClient.RegisterPushDevice(ctx, req.Token)
}

func (c *Client) SubscribeDeviceDing(ctx context.Context, req DeviceIDRequest) error {
	ctx = c.accountContext(ctx, req.Auth)
	id, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return err
	}
	if err = c.ensureSession(ctx); err != nil {
		return err
	}
	return c.restClient.SubscribeDeviceDing(ctx, id)
}

func (c *Client) SubscribeDeviceMotion(ctx context.Context, req DeviceIDRequest) error {
	ctx = c.accountContext(ctx, req.Auth)
	id, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return err
	}
	if err = c.ensureSession(ctx); err != nil {
		return err
	}
	return c.restClient.SubscribeDeviceMotion(ctx, id)
}

type FCMEventKind string

const (
	PushCredentials FCMEventKind = "credentials"
	PushRegistered  FCMEventKind = "registered"
	PushConnected   FCMEventKind = "connected"
	PushMessage     FCMEventKind = "message"
	PushRetry       FCMEventKind = "retry"
	PushClosed      FCMEventKind = "closed"
)

type PushAction string

const (
	PushActionDing   PushAction = "ding"
	PushActionMotion PushAction = "motion"
)

// PushEvent reports setup progress and incoming notifications. Credentials are
// sensitive and should be saved securely by the caller for reconnection.
type FCMEvent struct {
	Kind        FCMEventKind
	Token       string
	DeviceID    string
	Action      PushAction
	Credentials json.RawMessage
	Data        json.RawMessage
	Err         error
}

// FCMSource supplies receiver events. Most callers use the built-in receiver;
// an alternate source can be injected for controlled replay or other transports.
type FCMSource func(context.Context, json.RawMessage) (<-chan FCMEvent, error)

// WithFCMSource replaces the receiver while preserving Ring registration and
// event lifecycle behavior.
func WithFCMSource(source FCMSource) Option { return withFCMSource{source: source} }

type withFCMSource struct{ source FCMSource }

func (option withFCMSource) apply(c *Client) error {
	if option.source == nil {
		return ringapimodels.NewBadRequestError("FCM source must not be nil", nil)
	}
	c.fcmSource = option.source
	return nil
}

// PushConnection owns one FCM receiver and Ring device subscriptions.
type PushConnection struct {
	client  *Client
	ctx     context.Context
	cancel  context.CancelFunc
	events  chan FCMEvent
	done    chan struct{}
	closeMu sync.Once
}

func (c *Client) ConnectPush(ctx context.Context, req ConnectPushRequest) (*PushConnection, error) {
	if req.Auth.AccessToken == "" {
		return nil, ringapimodels.NewTokenError("push connection requires an access token", nil)
	}
	deviceIDs := make([]int64, 0, len(req.DeviceIDs))
	for _, rawID := range req.DeviceIDs {
		id, err := settingsDeviceID(rawID)
		if err != nil {
			return nil, err
		}
		deviceIDs = append(deviceIDs, id)
	}
	connectionCtx, cancel := context.WithCancel(c.accountContext(ctx, req.Auth))
	source := c.fcmSource
	if source == nil {
		source = defaultFCMSource
	}
	stream, err := source(connectionCtx, req.Credentials)
	if err != nil {
		cancel()
		return nil, ringapimodels.NewBadRequestError("invalid saved push credentials", err)
	}
	connection := &PushConnection{client: c, ctx: connectionCtx, cancel: cancel, events: make(chan FCMEvent, fcmEventBufferSize), done: make(chan struct{})}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		cancel()
		return nil, ringapimodels.NewConnectionError("client is closed", nil)
	}
	c.pushConnections[connection] = struct{}{}
	c.mu.Unlock()
	go connection.run(stream, deviceIDs, req.Ding, req.Motion)
	return connection, nil
}

// Events closes when the connection ends. Receive errors arrive as retry events.
func (p *PushConnection) Events() <-chan FCMEvent { return p.events }

func (p *PushConnection) Close() error {
	p.closeMu.Do(p.cancel)
	<-p.done
	return nil
}

func (p *PushConnection) run(stream <-chan FCMEvent, deviceIDs []int64, ding, motion bool) {
	defer close(p.done)
	defer close(p.events)
	defer func() {
		p.client.mu.Lock()
		delete(p.client.pushConnections, p)
		p.client.mu.Unlock()
	}()
	for {
		var event FCMEvent
		select {
		case <-p.ctx.Done():
			return
		case received, ok := <-stream:
			if !ok {
				return
			}
			event = received
		}
		switch event.Kind {
		case PushCredentials:
			p.emit(FCMEvent{Kind: PushCredentials, Credentials: event.Credentials})
			if err := p.register(event.Token, deviceIDs, ding, motion); err != nil {
				p.emit(FCMEvent{Kind: PushRetry, Err: err})
			} else {
				p.emit(FCMEvent{Kind: PushRegistered})
			}
		case PushConnected:
			p.emit(FCMEvent{Kind: PushConnected})
		case PushMessage:
			p.emit(ParseFCMNotification(event.Data))
		case PushRetry:
			p.emit(FCMEvent{Kind: PushRetry, Err: event.Err})
		case PushClosed:
			p.emit(FCMEvent{Kind: PushClosed})
		}
	}
}

func defaultFCMSource(ctx context.Context, saved json.RawMessage) (<-chan FCMEvent, error) {
	stream, err := push.Start(ctx, saved)
	if err != nil {
		return nil, err
	}
	out := make(chan FCMEvent, fcmEventBufferSize)
	go func() {
		defer close(out)
		for event := range stream {
			mapped := FCMEvent{Kind: FCMEventKind(event.Kind), Token: event.Token, Credentials: event.Credentials, Data: event.Data, Err: event.Err}
			select {
			case out <- mapped:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (p *PushConnection) register(token string, deviceIDs []int64, ding, motion bool) error {
	if err := p.client.ensureSession(p.ctx); err != nil {
		return err
	}
	if err := p.client.restClient.RegisterPushDevice(p.ctx, token); err != nil {
		return err
	}
	for _, id := range deviceIDs {
		if ding {
			if err := p.client.restClient.SubscribeDeviceDing(p.ctx, id); err != nil {
				return err
			}
		}
		if motion {
			if err := p.client.restClient.SubscribeDeviceMotion(p.ctx, id); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *PushConnection) emit(event FCMEvent) {
	select {
	case p.events <- event:
	case <-p.ctx.Done():
	}
}

// ParseFCMNotification extracts common Ring event fields while retaining the
// original payload for newer event categories.
func ParseFCMNotification(data json.RawMessage) FCMEvent {
	event := FCMEvent{Kind: PushMessage, Data: data}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(data, &envelope) != nil {
		return event
	}
	if nested := expandedObject(envelope["data"]); nested["android_config"] != nil || nested["data"] != nil {
		envelope = nested
	}
	for key, value := range envelope {
		var encoded string
		if json.Unmarshal(value, &encoded) == nil && json.Valid([]byte(encoded)) {
			envelope[key] = json.RawMessage(encoded)
		}
	}
	var config struct {
		Category PushAction `json:"category"`
	}
	_ = json.Unmarshal(envelope["android_config"], &config)
	event.Action = config.Category
	var payload fcmPayload
	_ = json.Unmarshal(envelope["data"], &payload)
	if payload.Device.ID != "" {
		event.DeviceID = payload.Device.ID.String()
	}
	if event.Action == "" {
		event.Action = payload.GCMData.Action
	}
	if event.DeviceID == "" {
		var id int64
		if json.Unmarshal(envelope["doorbot_id"], &id) == nil {
			event.DeviceID = strconv.FormatInt(id, 10)
		}
	}
	return event
}

type fcmPayload struct {
	Device  fcmDevice  `json:"device"`
	GCMData fcmGCMData `json:"gcmData"`
}

type fcmDevice struct {
	ID json.Number `json:"id"`
}

type fcmGCMData struct {
	Action PushAction `json:"action"`
}

func expandedObject(raw json.RawMessage) map[string]json.RawMessage {
	var encoded string
	if json.Unmarshal(raw, &encoded) == nil && json.Valid([]byte(encoded)) {
		raw = json.RawMessage(encoded)
	}
	var object map[string]json.RawMessage
	_ = json.Unmarshal(raw, &object)
	return object
}
