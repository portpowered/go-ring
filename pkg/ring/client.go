// Package ring provides the main client for interacting with Ring services.
// It orchestrates authentication, API clients (REST), and event connections.
package ring

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/portpowered/go-ring/pkg/dependencies/rest"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// Client is the main client for interacting with Ring services
type Client struct {
	restClient   *rest.Client
	accessToken  string
	refreshToken string
	username     string
	password     string
	hardwareID   string
	userAgent    string

	tokenGetter       func(ctx context.Context) (string, error)
	rtcWebSocketURL   string
	eventWebSocketURL string
	mu                sync.RWMutex
	sessionMu         sync.Mutex
	sessionRegistered bool
	closed            bool
}

// Option is a function that configures a Client
type Option interface {
	Apply(c *Client) error
}

// Apply options to the client.
func (c *Client) Apply(opts ...Option) error {
	for _, opt := range opts {
		if err := opt.Apply(c); err != nil {
			return err
		}
	}
	return nil
}

// NewClient creates a new Ring client
func NewClient(opts ...Option) (*Client, error) {
	client := &Client{
		restClient:        rest.NewClient(),
		userAgent:         ringapimodels.DefaultUserAgent,
		rtcWebSocketURL:   ringapimodels.RingRTCStreamingWebSocketEndpoint,
		eventWebSocketURL: "wss://api.ring.com/clients_api/ws",
	}

	for _, opt := range opts {
		if err := opt.Apply(client); err != nil {
			return nil, &ringapimodels.ConnectionError{
				Message: "failed to apply option",
				Err:     err,
			}
		}
	}

	return client, nil
}

// WithAccessToken creates a client with an access token
func WithAccessToken(token string) Option {
	return withAccessToken(token)
}

type withAccessToken string

func (w withAccessToken) Apply(c *Client) error {
	token := string(w)
	c.accessToken = token
	c.restClient.Apply(
		rest.WithAccessToken(token),
	)
	return nil
}

// WithRefreshToken creates a client with a refresh token
func WithRefreshToken(refreshToken string) Option {
	return withRefreshToken(refreshToken)
}

type withRefreshToken string

func (w withRefreshToken) Apply(c *Client) error {
	c.refreshToken = string(w)
	return nil
}

// WithUsername sets the username for authentication
func WithUsername(username string) Option {
	return withUsername(username)
}

type withUsername string

func (w withUsername) Apply(c *Client) error {
	c.username = string(w)
	return nil
}

// WithPassword sets the password for authentication
func WithPassword(password string) Option {
	return withPassword(password)
}

type withPassword string

func (w withPassword) Apply(c *Client) error {
	c.password = string(w)
	return nil
}

// WithHardwareID sets the hardware ID for authentication
func WithHardwareID(hardwareID string) Option {
	return withHardwareID(hardwareID)
}

type withHardwareID string

func (w withHardwareID) Apply(c *Client) error {
	id := string(w)
	c.hardwareID = id
	c.restClient.Apply(
		rest.WithHardwareID(id),
	)
	return nil
}

// WithHTTPClient sets a custom HTTP client for REST API calls.
func WithHTTPClient(httpClient *http.Client) Option {
	return withHTTPClient{httpClient: httpClient}
}

type withHTTPClient struct {
	httpClient *http.Client
}

func (w withHTTPClient) Apply(c *Client) error {
	c.restClient.Apply(rest.WithHTTPClient(w.httpClient))
	return nil
}

// WithUserAgent sets a custom user agent
func WithUserAgent(userAgent string) Option {
	return withUserAgent(userAgent)
}

type withUserAgent string

func (w withUserAgent) Apply(c *Client) error {
	ua := string(w)
	c.userAgent = ua
	c.restClient.Apply(
		rest.WithUserAgent(ua),
	)
	return nil
}

// WithTokenGetter sets a function to retrieve tokens dynamically
func WithTokenGetter(getter func(ctx context.Context) (string, error)) Option {
	return withTokenGetter{getter: getter}
}

type withTokenGetter struct {
	getter func(ctx context.Context) (string, error)
}

func (w withTokenGetter) Apply(c *Client) error {
	c.tokenGetter = w.getter
	c.restClient.Apply(
		rest.WithTokenGetter(w.getter),
	)
	return nil
}

// WithRTCWebSocketURL sets a custom websocket URL for RTC streams (useful for testing)
func WithRTCWebSocketURL(url string) Option {
	return withRTCWebSocketURL(url)
}

type withRTCWebSocketURL string

func (w withRTCWebSocketURL) Apply(c *Client) error {
	c.rtcWebSocketURL = string(w)
	return nil
}

// WithEventWebSocketURL configures the event endpoint, including local test servers.
// The default vendor endpoint is experimental; callers must verify vendor support.
func WithEventWebSocketURL(url string) Option {
	return withEventWebSocketURL(url)
}

type withEventWebSocketURL string

var _ Option = withEventWebSocketURL("")

func (w withEventWebSocketURL) Apply(c *Client) error {
	c.eventWebSocketURL = string(w)
	return nil
}

// NewClientWithToken creates a client with an access token (convenience function)
func NewClientWithToken(accessToken string, opts ...Option) (*Client, error) {
	baseOptions := []Option{WithAccessToken(accessToken)}
	if hardwareID := hardwareIDFromAccessToken(accessToken); hardwareID != "" {
		baseOptions = append(baseOptions, WithHardwareID(hardwareID))
	}
	opts = append(baseOptions, opts...)
	return NewClient(opts...)
}

func hardwareIDFromAccessToken(accessToken string) string {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		HardwareID string `json:"hardware_id"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	return claims.HardwareID
}

func (c *Client) ensureSession(ctx context.Context) error {
	if c.hardwareID == "" {
		return nil
	}
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	if c.sessionRegistered {
		return nil
	}
	if err := c.restClient.RegisterSession(ctx); err != nil {
		return err
	}
	c.sessionRegistered = true
	return nil
}

// getToken retrieves the access token
func (c *Client) getToken(ctx context.Context) (string, error) {
	if c.accessToken != "" {
		return c.accessToken, nil
	}
	if c.tokenGetter != nil {
		return c.tokenGetter(ctx)
	}
	return "", ringapimodels.NewTokenError("no token available", nil)
}

// Close closes the client and all connections
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true
	return nil
}
