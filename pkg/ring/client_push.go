package ring

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"sync"

	"github.com/portpowered/go-ring/internal/generatedfcm"
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

	err := c.ensureSession(ctx)
	if err != nil {
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

	{
		err = c.ensureSession(ctx)
		if err != nil {
			return err
		}
	}

	return c.restClient.SubscribeDeviceDing(ctx, id)
}

func (c *Client) SubscribeDeviceMotion(ctx context.Context, req DeviceIDRequest) error {
	ctx = c.accountContext(ctx, req.Auth)

	id, err := settingsDeviceID(req.DeviceID)
	if err != nil {
		return err
	}

	{
		err = c.ensureSession(ctx)
		if err != nil {
			return err
		}
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

// FCMDialContext supplies the already-secured connection used for FCM MCS
// frames. The default receiver performs TLS before returning its connection.
type FCMDialContext func(context.Context, string, string) (net.Conn, error)

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

// WithFCMHTTPTransport configures the HTTP transport used by the built-in FCM
// receiver. It has no effect when WithFCMSource supplies a custom receiver.
func WithFCMHTTPTransport(transport http.RoundTripper) Option {
	return withFCMHTTPTransport{transport: transport}
}

type withFCMHTTPTransport struct{ transport http.RoundTripper }

func (option withFCMHTTPTransport) apply(c *Client) error {
	if option.transport == nil {
		return ringapimodels.NewBadRequestError("FCM HTTP transport must not be nil", nil)
	}

	c.fcmHTTPTransport = option.transport

	return nil
}

// WithFCMDialContext configures the context-aware FCM MCS connection function.
// The default receiver establishes TLS; injected functions return the
// connection that carries MCS frames.
func WithFCMDialContext(dialContext FCMDialContext) Option {
	return withFCMDialContext{dialContext: dialContext}
}

type withFCMDialContext struct{ dialContext FCMDialContext }

func (option withFCMDialContext) apply(client *Client) error {
	if option.dialContext == nil {
		return ringapimodels.NewBadRequestError("FCM dial context must not be nil", nil)
	}

	client.fcmDialContext = option.dialContext

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
		source = func(ctx context.Context, saved json.RawMessage) (<-chan FCMEvent, error) {
			return defaultFCMSource(ctx, saved, c.fcmHTTPTransport, c.fcmDialContext)
		}
	}

	stream, err := source(connectionCtx, req.Credentials)
	if err != nil {
		cancel()

		return nil, ringapimodels.NewBadRequestError("invalid saved push credentials", err)
	}

	connection := &PushConnection{
		client: c,
		ctx:    connectionCtx,
		cancel: cancel,
		events: make(chan FCMEvent, fcmEventBufferSize),
		done:   make(chan struct{}),
	}

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
		case PushRegistered:
			continue
		case PushCredentials:
			p.emit(FCMEvent{
				Kind:        PushCredentials,
				Token:       "",
				DeviceID:    "",
				Action:      "",
				Credentials: event.Credentials,
				Data:        nil,
				Err:         nil,
			})

			err := p.register(event.Token, deviceIDs, ding, motion)
			if err != nil {
				p.emit(FCMEvent{
					Kind:        PushRetry,
					Token:       "",
					DeviceID:    "",
					Action:      "",
					Credentials: nil,
					Data:        nil,
					Err:         err,
				})
			} else {
				p.emit(FCMEvent{
					Kind:        PushRegistered,
					Token:       "",
					DeviceID:    "",
					Action:      "",
					Credentials: nil,
					Data:        nil,
					Err:         nil,
				})
			}
		case PushConnected:
			p.emit(FCMEvent{
				Kind:        PushConnected,
				Token:       "",
				DeviceID:    "",
				Action:      "",
				Credentials: nil,
				Data:        nil,
				Err:         nil,
			})
		case PushMessage:
			p.emit(ParseFCMNotification(event.Data))
		case PushRetry:
			p.emit(FCMEvent{
				Kind:        PushRetry,
				Token:       "",
				DeviceID:    "",
				Action:      "",
				Credentials: nil,
				Data:        nil,
				Err:         event.Err,
			})
		case PushClosed:
			p.emit(FCMEvent{
				Kind:        PushClosed,
				Token:       "",
				DeviceID:    "",
				Action:      "",
				Credentials: nil,
				Data:        nil,
				Err:         nil,
			})
		}
	}
}

func defaultFCMSource(
	ctx context.Context,
	saved json.RawMessage,
	transport http.RoundTripper,
	dialContext FCMDialContext,
) (<-chan FCMEvent, error) {
	stream, err := push.StartWithTransports(ctx, saved, transport, push.DialContextFunc(dialContext))
	if err != nil {
		return nil, ringapimodels.NewBadRequestError("invalid saved FCM credentials", err)
	}

	out := make(chan FCMEvent, fcmEventBufferSize)

	go func() {
		defer close(out)

		for event := range stream {
			mapped := FCMEvent{
				Kind:        FCMEventKind(event.Kind),
				Token:       event.Token,
				DeviceID:    "",
				Action:      "",
				Credentials: event.Credentials,
				Data:        event.Data,
				Err:         event.Err,
			}
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
	err := p.client.ensureSession(p.ctx)
	if err != nil {
		return err
	}

	err = p.client.restClient.RegisterPushDevice(p.ctx, token)
	if err != nil {
		return err
	}

	for _, id := range deviceIDs {
		if ding {
			err := p.client.restClient.SubscribeDeviceDing(p.ctx, id)
			if err != nil {
				return err
			}
		}

		if motion {
			err := p.client.restClient.SubscribeDeviceMotion(p.ctx, id)
			if err != nil {
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
	event := FCMEvent{
		Kind:        PushMessage,
		Token:       "",
		DeviceID:    "",
		Action:      "",
		Credentials: nil,
		Data:        data,
		Err:         nil,
	}

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

	var config generatedfcm.RingPushNotificationConfig

	_ = json.Unmarshal(envelope["android_config"], &config)

	if config.Category != nil {
		event.Action = PushAction(*config.Category)
	}

	var payload generatedfcm.RingPushNotificationPayload

	_ = json.Unmarshal(envelope["data"], &payload)

	if payload.Device != nil && payload.Device.Id != nil {
		event.DeviceID = strconv.FormatInt(*payload.Device.Id, 10)
	}

	if event.Action == "" && payload.GcmData != nil && payload.GcmData.Action != nil {
		event.Action = PushAction(*payload.GcmData.Action)
	}

	if event.DeviceID == "" {
		var id int64
		if json.Unmarshal(envelope["doorbot_id"], &id) == nil {
			event.DeviceID = strconv.FormatInt(id, 10)
		}
	}

	return event
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
