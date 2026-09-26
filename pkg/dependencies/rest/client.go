// Package rest provides a REST API client for interacting with Ring services.
// It supports authentication, device enumeration, control, and state management via HTTP/RESTful APIs.
package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/requestauth"
	"github.com/portpowered/go-ring/internal/ringerrors"
)

// Client is a REST API client for Ring services
type Client struct {
	httpClient   *http.Client
	baseURI      string
	oauthBaseURI string
	userAgent    string
	hardwareID   string
	authMu       sync.Mutex
	pendingPKCE  *pkceState
}

// ClientOption is a function that configures a Client
type ClientOption func(*Client)

// WithHTTPClient sets a custom HTTP client
func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = httpClient
	}
}

// WithBaseURI sets the base URL for the Ring API
func WithBaseURI(baseURL string) ClientOption {
	return func(c *Client) {
		c.baseURI = baseURL
	}
}

// WithEndpointBases configures per-client Ring service origins.
func WithEndpointBases(apiBase, oauthBase string) ClientOption {
	return func(c *Client) { c.baseURI, c.oauthBaseURI = apiBase, oauthBase }
}

// WithUserAgent sets a custom user agent
func WithUserAgent(userAgent string) ClientOption {
	return func(c *Client) {
		c.userAgent = userAgent
	}
}

// WithHardwareID sets the hardware ID for authentication
func WithHardwareID(hardwareID string) ClientOption {
	return func(c *Client) {
		c.hardwareID = hardwareID
	}
}

// NewClient creates a new REST API client
func NewClient(opts ...ClientOption) *Client {
	client := &Client{
		baseURI:      protocol.APIBaseURL,
		oauthBaseURI: protocol.OAuthBaseURL,
		userAgent:    protocol.DefaultUserAgent,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	for _, opt := range opts {
		opt(client)
	}

	return client
}

// Apply applies the options to the client
func (c *Client) Apply(opts ...ClientOption) {
	for _, opt := range opts {
		opt(c)
	}
}

// getToken retrieves credentials scoped to the current operation.
func (c *Client) getToken(ctx context.Context) (string, error) {
	if auth, ok := requestauth.FromContext(ctx); ok {
		if auth.AccessToken == "" {
			return "", ringerrors.NewTokenError("no token in request", nil)
		}
		return auth.AccessToken, nil
	}
	return "", ringerrors.NewTokenError("no token in request", nil)
}

func (c *Client) hardwareIDFor(ctx context.Context) string {
	if auth, ok := requestauth.FromContext(ctx); ok {
		return auth.HardwareID
	}
	return c.hardwareID
}

// doRequest performs an HTTP request with retry logic
func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, ringerrors.NewNetworkError("request canceled", err)
	}
	var bodyReader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, ringerrors.NewBadRequestError("failed to marshal request body", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	url := c.baseURI + path
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, ringerrors.NewNetworkError("failed to create request", err)
	}

	// Get token and set authorization header
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, ringerrors.NewTokenError("failed to retrieve access token", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)

	if hardwareID := c.hardwareIDFor(ctx); hardwareID != "" {
		req.Header.Set("hardware_id", hardwareID)
	}

	// Retry logic
	maxRetries := 1
	if method == http.MethodGet || method == http.MethodHead {
		maxRetries = 3
	}
	var resp *http.Response
	for i := 0; i < maxRetries; i++ {
		attemptReq := req
		if i > 0 {
			attemptReq = req.Clone(ctx)
			if req.GetBody != nil {
				attemptReq.Body, err = req.GetBody()
				if err != nil {
					return nil, ringerrors.NewNetworkError("failed to recreate request body", err)
				}
			}
		}
		resp, err = c.httpClient.Do(attemptReq)
		if err == nil && resp.StatusCode < 500 {
			break
		}

		if i < maxRetries-1 {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			// Exponential backoff
			backoff := time.Duration(i+1) * time.Second
			select {
			case <-ctx.Done():
				return nil, ringerrors.NewNetworkError("request canceled during retry", ctx.Err())
			case <-time.After(backoff):
			}
		}
	}

	if err != nil {
		return nil, ringerrors.NewNetworkError("request failed after retries", err)
	}

	return resp, nil
}

// doJSONRequest performs a request and unmarshals the JSON response
func (c *Client) doJSONRequest(ctx context.Context, method, path string, body interface{}, result interface{}) error {
	resp, err := c.doRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Read the response body before decoding so the transport can be reused.
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return ringerrors.NewNetworkError("failed to read response body", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ringerrors.ClassifyHTTPError(resp, string(bodyBytes))
	}

	if result != nil {
		if err := json.Unmarshal(bodyBytes, result); err != nil {
			return ringerrors.NewInternalServerError("failed to decode response", err)
		}
	}

	return nil
}

// HTTPClient returns the underlying HTTP client
func (c *Client) HTTPClient() *http.Client {
	return c.httpClient
}
