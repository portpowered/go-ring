package ring

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/portpowered/go-ring/internal/generatedsignaling"
	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/signaling"
	"github.com/portpowered/go-ring/pkg/dependencies/webrtc"
	dependencywebsocket "github.com/portpowered/go-ring/pkg/dependencies/websocket"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

type deviceSessionNegotiation struct {
	started        time.Time
	id             int64
	maxAge         time.Duration
	cancel         context.CancelFunc
	dialog         string
	events         chan signaling.Message
	signalID       string
	riid           string
	startedSuccess bool
	deadlineError  func() error
}

func (c *SignalingConnection) prepareDeviceSession(
	ctx context.Context,
	req StartDeviceSessionRequest,
	started time.Time,
) (*deviceSessionNegotiation, context.Context, error) {
	id, maxAge, err := validateSessionRequest(req)
	if err != nil {
		return nil, nil, err
	}

	negotiationBudget := min(maxAge, signaling.NegotiationTimeout)
	negotiationCtx, cancel := context.WithDeadline(ctx, started.Add(negotiationBudget))
	deadlineError := func() error {
		if !time.Now().Before(started.Add(maxAge)) {
			return signaling.ErrExpired
		}

		return negotiationCtx.Err()
	}

	negotiation := &deviceSessionNegotiation{
		started:        started,
		id:             id,
		maxAge:         maxAge,
		cancel:         cancel,
		dialog:         uuid.NewString(),
		events:         make(chan signaling.Message, signaling.NegotiationQueueCapacity),
		signalID:       "",
		riid:           "",
		startedSuccess: false,
		deadlineError:  deadlineError,
	}

	c.mu.Lock()

	if c.closed {
		c.mu.Unlock()
		cancel()

		return nil, nil, c.Err()
	}

	c.pending[negotiation.dialog] = negotiation.events
	c.mu.Unlock()

	body := generatedsignaling.LiveViewBody{
		DoorbotId: int(negotiation.id),
		StreamOptions: &generatedsignaling.LiveStreamOptions{
			AudioEnabled:         req.AudioEnabled,
			VideoEnabled:         req.VideoEnabled,
			AdditionalProperties: nil,
		},
		Sdp:                  req.Offer.SDP,
		ReservedType:         protocol.SDPTypeOffer,
		AdditionalProperties: nil,
	}

	err = c.send(negotiationCtx, signaling.Message{
		Method:   protocol.MethodLiveView,
		DialogID: negotiation.dialog,
		RIID:     "",
		Body:     mustJSON(body),
	})
	if err != nil {
		negotiation.cleanup(c)
		cancel()

		return nil, nil, sessionError("failed to send live-view offer", err)
	}

	return negotiation, negotiationCtx, nil
}

func (negotiation *deviceSessionNegotiation) cleanup(connection *SignalingConnection) {
	connection.mu.Lock()
	delete(connection.pending, negotiation.dialog)
	connection.mu.Unlock()
}

func (c *SignalingConnection) finishDeviceSession(
	ctx, negotiationCtx context.Context,
	req StartDeviceSessionRequest,
	negotiation *deviceSessionNegotiation,
) (*DeviceSession, error) {
	//nolint:contextcheck // Failure cleanup sends a bounded close frame after negotiation cancellation.
	defer func() {
		if !negotiation.startedSuccess && negotiation.signalID != "" {
			negotiation.close(c)
		}
	}()

	created, err := c.createNegotiatedDeviceSession(ctx, negotiationCtx, req, negotiation)
	if err != nil {
		negotiation.cleanup(c)

		return nil, err
	}

	err = c.startAndRegisterDeviceSession(ctx, negotiationCtx, req, negotiation, created)
	if err != nil {
		created.terminate(err)
		negotiation.cleanup(c)

		return nil, err
	}

	negotiation.startedSuccess = true

	return created, nil
}

func (negotiation *deviceSessionNegotiation) close(connection *SignalingConnection) {
	ctx, cancel := context.WithTimeout(context.Background(), signaling.CloseTimeout)
	defer cancel()

	_ = connection.send(
		ctx,
		signaling.Message{
			Method:   protocol.MethodClose,
			DialogID: negotiation.dialog,
			RIID:     negotiation.riid,
			Body: mustJSON(generatedsignaling.SessionBody{
				DoorbotId:            int(negotiation.id),
				SessionId:            negotiation.signalID,
				AdditionalProperties: nil,
			}),
		},
	)
}

func (c *SignalingConnection) createNegotiatedDeviceSession(
	ctx, negotiationCtx context.Context,
	req StartDeviceSessionRequest,
	negotiation *deviceSessionNegotiation,
) (*DeviceSession, error) {
	negotiated, err := dependencywebsocket.AwaitLiveAnswer(
		negotiationCtx,
		c.done,
		c.Err,
		negotiation.events,
		negotiation.id,
		negotiation.deadlineError,
	)
	negotiation.signalID, negotiation.riid = negotiated.SignalID, negotiated.RIID

	if err != nil {
		return nil, sessionError("live-view negotiation failed", err)
	}

	answerSDP, controlID, heartbeat := negotiated.AnswerSDP, negotiated.ControlID, negotiated.Heartbeat
	if controlID == "" || controlID == negotiation.signalID {
		return nil, ringapimodels.NewConnectionError("answer is missing an independent PTZ session identity", nil)
	}

	answer, err := webrtc.NormalizeAnswer(req.Offer.SDP, answerSDP)
	if err != nil {
		return nil, ringapimodels.NewConnectionError("invalid SDP answer", err)
	}

	_, err = webrtc.ParseSDP(answer)
	if err != nil {
		return nil, ringapimodels.NewConnectionError("invalid SDP answer", err)
	}

	iceMode := req.ICEMode
	if iceMode == "" {
		iceMode = ICETrickle
	}

	created := &DeviceSession{
		connection: c,
		dialogID:   negotiation.dialog,
		answer:     SessionDescription{Type: SDPTypeAnswer, SDP: answer},
		offerSDP:   req.Offer.SDP,
		started:    negotiation.started,
		movement:   map[PTZAxis]string{},
		ready:      make(chan struct{}),
		done:       make(chan struct{}),
		deviceID:   negotiation.id,
		signalID:   negotiation.signalID,
		riid:       negotiation.riid,
		iceMode:    iceMode,
	}

	remaining := negotiation.maxAge - time.Since(negotiation.started)
	if remaining <= 0 {
		return nil, ringapimodels.NewConnectionError("session expired during negotiation", signaling.ErrExpired)
	}

	created.core, err = signaling.NewSession(
		ctx,
		signaling.SessionConfig{
			DeviceID:  negotiation.id,
			DialogID:  negotiation.dialog,
			SignalID:  negotiation.signalID,
			ControlID: controlID,
			Heartbeat: heartbeat,
			MaxAge:    remaining,
			Clock:     nil,
			Send: func(ctx context.Context, message signaling.Message) error {
				if message.RIID == "" {
					message.RIID = negotiation.riid
				}

				return c.send(ctx, message)
			},
		},
	)
	if err != nil {
		return nil, sessionError("failed to create device session", err)
	}

	go created.watch(ctx)

	for _, message := range negotiated.EarlyICE {
		err := created.core.Handle(message)
		if err != nil {
			created.terminate(err)

			return nil, sessionError("early ICE message rejected", err)
		}
	}

	return created, nil
}

func (c *SignalingConnection) startAndRegisterDeviceSession(
	ctx, negotiationCtx context.Context,
	req StartDeviceSessionRequest,
	negotiation *deviceSessionNegotiation,
	created *DeviceSession,
) error {
	err := c.activateDeviceSession(negotiationCtx, created, negotiation.id, negotiation.signalID, req)
	if err != nil {
		return err
	}

	err = c.waitForCameraStarted(
		ctx,
		negotiationCtx,
		negotiation.events,
		created,
		negotiation.deadlineError,
	)
	if err != nil {
		return sessionError("device session did not become ready", err)
	}

	return c.registerActivatedDeviceSession(negotiation, created)
}

// registerActivatedDeviceSession replays the bounded pending tail before
// exposing direct routing. Handle performs only local validation/queueing here;
// signaling writes and termination cleanup run outside this routing lock.
func (c *SignalingConnection) registerActivatedDeviceSession(
	negotiation *deviceSessionNegotiation,
	created *DeviceSession,
) error {
	c.mu.Lock()

	if c.closed {
		c.mu.Unlock()

		return c.Err()
	}

	for {
		select {
		case message := <-negotiation.events:
			created.handle(message)

			if created.State() != SessionActive {
				c.mu.Unlock()

				return created.terminalError()
			}
		default:
			delete(c.pending, negotiation.dialog)
			c.sessions[negotiation.dialog] = created
			c.mu.Unlock()

			return nil
		}
	}
}
