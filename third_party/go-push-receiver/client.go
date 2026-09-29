/*
 * Copyright (c) 2019 Zenichi Amano
 *
 * This file is part of go-push-receiver, which is MIT licensed.
 * See http://opensource.org/licenses/MIT
 */

// Package pushreceiver is Push Message Receiver library from FCM.
package pushreceiver

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"
)

const defaultEventBufferSize = 50

// httpClient defines the minimal interface needed for an http.Client to be implemented.
type httpClient interface {
	Do(request *http.Request) (*http.Response, error)
}

// Client is FCM Push receive client.
type Client struct {
	apiKey               string
	projectID            string
	appID                string
	vapidKey             string
	logger               *slog.Logger
	httpClient           httpClient
	tlsConfig            *tls.Config
	creds                *FCMCredentials
	dialer               *net.Dialer
	mcsDialContext       MCSDialContext
	backoff              *Backoff
	heartbeat            *Heartbeat
	receivedPersistentID []string
	retryDisabled        bool
	Events               chan Event
}

// New returns a new FCM push receive client instance.
func New(config *Config, options ...ClientOption) *Client {
	client := new(Client)
	client.apiKey = config.ApiKey
	client.projectID = config.ProjectID
	client.appID = config.AppID
	client.vapidKey = config.VapidKey

	for _, option := range options {
		option(client)
	}

	// set defaults
	client.setDefaultOptions()

	client.logger.Debug(
		"Config",
		"apiKey", client.apiKey,
		"projectID", client.projectID,
		"appID", client.appID,
		"vapidKey", client.vapidKey,
	)

	return client
}

func (c *Client) post(ctx context.Context, request *http.Request) (*http.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	request = request.WithContext(ctx)

	res, err := c.httpClient.Do(request)
	if err != nil {
		return nil, wrapError(err, "send FCM POST request")
	}

	return res, nil
}

// setDefaultOptions set default options.
func (c *Client) setDefaultOptions() {
	// set defaults
	if c.backoff == nil {
		c.backoff = NewBackoff(defaultBackoffBase*time.Second, defaultBackoffMax*time.Second)
	}

	if c.heartbeat == nil {
		c.heartbeat = newHeartbeat(
			WithClientInterval(defaultHeartbeatPeriod * time.Minute),
		)
	}

	if c.tlsConfig == nil {
		c.tlsConfig = &tls.Config{
			MinVersion: tls.VersionTLS13,
		}
	}

	if c.dialer == nil {
		c.dialer = &net.Dialer{
			Timeout:       defaultDialTimeout * time.Second,
			KeepAlive:     defaultKeepAlive * time.Minute,
			FallbackDelay: 30 * time.Millisecond,
		}
	}

	if c.httpClient == nil {
		c.httpClient = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: c.tlsConfig,
			},
		}
	}

	if c.logger == nil {
		c.logger = slog.New(noOpHandler{})
	}

	if c.Events == nil {
		c.Events = make(chan Event, defaultEventBufferSize)
	}

	if len(c.vapidKey) == 0 {
		c.vapidKey = fcmServerKey
	}
}

func closeResponse(res *http.Response) {
	if res == nil || res.Body == nil {
		return
	}

	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
}
