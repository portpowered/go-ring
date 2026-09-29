/*
 * Copyright (c) 2019 Zenichi Amano
 *
 * This file is part of go-push-receiver, which is MIT licensed.
 * See http://opensource.org/licenses/MIT
 */

package pushreceiver

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
)

// ClientOption configures a push receiver client.
type ClientOption func(*Client)

// MCSDialContext establishes the connection used for the MCS stream. The
// default implementation performs TLS with the receiver's configured TLS
// settings. Injected functions must return a connection ready for MCS frames.
type MCSDialContext func(context.Context, string, string) (net.Conn, error)

// WithMCSDialContext configures the context-aware MCS connection function.
func WithMCSDialContext(dialContext MCSDialContext) ClientOption {
	return func(client *Client) {
		client.mcsDialContext = dialContext
	}
}

// WithLogger sets the logger.
func WithLogger(logger *slog.Logger) ClientOption {
	return func(client *Client) {
		client.logger = logger
	}
}

// WithCreds sets the FCM credentials.
func WithCreds(creds *FCMCredentials) ClientOption {
	return func(client *Client) {
		client.creds = creds
	}
}

// WithReceivedPersistentID sets the list of received persistent IDs.
func WithReceivedPersistentID(ids []string) ClientOption {
	return func(client *Client) {
		client.receivedPersistentID = ids
	}
}

// WithHTTPClient sets the HTTP client.
func WithHTTPClient(c httpClient) ClientOption {
	return func(client *Client) {
		client.httpClient = c
	}
}

// WithTLSConfig sets the TLS configuration.
func WithTLSConfig(c *tls.Config) ClientOption {
	return func(client *Client) {
		client.tlsConfig = c
	}
}

// WithBackoff sets the retry backoff policy.
func WithBackoff(b *Backoff) ClientOption {
	return func(client *Client) {
		client.backoff = b
	}
}

// WithHeartbeat configures heartbeat behavior.
func WithHeartbeat(options ...HeartbeatOption) ClientOption {
	return func(client *Client) {
		client.heartbeat = newHeartbeat(options...)
	}
}

// WithDialer sets the MCS dialer.
func WithDialer(dialer *net.Dialer) ClientOption {
	return func(client *Client) {
		client.dialer = dialer
	}
}

// WithRetry configures whether to retry when an error occurs.
func WithRetry(retry bool) ClientOption {
	return func(client *Client) {
		client.retryDisabled = !retry
	}
}

// WithEvents sets the event channel.
func WithEvents(events chan Event) ClientOption {
	return func(client *Client) {
		client.Events = events
	}
}
