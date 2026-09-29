/*
 * Copyright (c) 2019 Zenichi Amano
 *
 * This file is part of go-push-receiver, which is MIT licensed.
 * See http://opensource.org/licenses/MIT
 */

package pushreceiver

import (
	"context"
	"log/slog"
	"time"
)

const deadmanTimeoutMultiplier = 4

// Heartbeat sends a signal to keep the connection alive.
type Heartbeat struct {
	clientInterval time.Duration
	serverInterval time.Duration
	deadmanTimeout time.Duration
	adaptive       bool
}

// HeartbeatOption configures heartbeat behavior.
type HeartbeatOption func(*Heartbeat)

// WithClientInterval sets the client heartbeat interval.
func WithClientInterval(interval time.Duration) HeartbeatOption {
	return func(heartbeat *Heartbeat) {
		heartbeat.clientInterval = interval
	}
}

// WithServerInterval sets the server heartbeat interval.
func WithServerInterval(interval time.Duration) HeartbeatOption {
	return func(heartbeat *Heartbeat) {
		// minimum 1 minute
		if interval > 1*time.Minute {
			heartbeat.serverInterval = interval
		} else {
			heartbeat.serverInterval = 1 * time.Minute
		}
	}
}

// WithDeadmanTimeout sets the heartbeat deadman timeout.
func WithDeadmanTimeout(timeout time.Duration) HeartbeatOption {
	return func(heartbeat *Heartbeat) {
		heartbeat.deadmanTimeout = timeout
	}
}

// WithAdaptive enables or disables adaptive heartbeat behavior.
func WithAdaptive(enabled bool) HeartbeatOption {
	return func(heartbeat *Heartbeat) {
		heartbeat.adaptive = enabled
	}
}

func newHeartbeat(options ...HeartbeatOption) *Heartbeat {
	h := new(Heartbeat)
	for _, option := range options {
		option(h)
	}

	return h
}

func (h *Heartbeat) start(
	ctx context.Context,
	logger *slog.Logger,
	heartbeatAck chan bool,
	sendHeartbeat func() error,
	onDisconnect func(),
) {
	if h.deadmanTimeout <= 0 {
		if h.clientInterval < h.serverInterval {
			h.deadmanTimeout = durationDeadmanTimeout(h.serverInterval)
		} else {
			h.deadmanTimeout = durationDeadmanTimeout(h.clientInterval)
		}
	}

	var (
		pingDeadman  *time.Timer
		pingDeadmanC <-chan time.Time
	)

	if h.deadmanTimeout > 0 {
		pingDeadman = time.NewTimer(h.deadmanTimeout)
		pingDeadmanC = pingDeadman.C
	}

	defer func() {
		logger.DebugContext(ctx, "heartbeat stopped")

		if pingDeadman != nil {
			pingDeadman.Stop()
		}
	}()

	var (
		pingTicker  *time.Ticker
		pingTickerC <-chan time.Time
	)

	if h.clientInterval > 0 {
		pingTicker = time.NewTicker(h.clientInterval)
		pingTickerC = pingTicker.C
	}

	defer func() {
		if pingTicker != nil {
			pingTicker.Stop()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeatAck:
			if pingDeadman != nil {
				pingDeadman.Reset(h.deadmanTimeout)
			}
		case <-pingDeadmanC:
			// force disconnect
			logger.InfoContext(ctx, "force disconnect by heartbeat")
			onDisconnect()

			return
		case <-pingTickerC:
			// send heartbeat to FCM
			err := sendHeartbeat()
			if err != nil {
				return
			}
		}
	}
}

func durationDeadmanTimeout(interval time.Duration) time.Duration {
	return interval * deadmanTimeoutMultiplier
}
