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
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// Client is a REST API client for Ring services
type Client struct {
	httpClient   *http.Client
	baseURI      string
	oauthBaseURI string
	accessToken  string
	tokenGetter  func(ctx context.Context) (string, error)
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

// WithAccessToken sets an access token directly
func WithAccessToken(token string) ClientOption {
	return func(c *Client) {
		c.accessToken = token
	}
}

// WithTokenGetter sets a function to retrieve tokens dynamically
func WithTokenGetter(getter func(ctx context.Context) (string, error)) ClientOption {
	return func(c *Client) {
		c.tokenGetter = getter
	}
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
		userAgent:    ringapimodels.DefaultUserAgent,
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

// getToken retrieves the access token, either from direct token or token getter
func (c *Client) getToken(ctx context.Context) (string, error) {
	if c.accessToken != "" {
		return c.accessToken, nil
	}
	if c.tokenGetter != nil {
		return c.tokenGetter(ctx)
	}
	return "", ringapimodels.NewTokenError("no token available", nil)
}

// doRequest performs an HTTP request with retry logic
func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var bodyReader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, ringapimodels.NewBadRequestError("failed to marshal request body", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	url := c.baseURI + path
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, ringapimodels.NewNetworkError("failed to create request", err)
	}

	// Get token and set authorization header
	token, err := c.getToken(ctx)
	if err != nil && c.tokenGetter != nil {
		return nil, ringapimodels.NewTokenError("failed to retrieve access token", err)
	}
	if err == nil && token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)

	if c.hardwareID != "" {
		req.Header.Set("hardware_id", c.hardwareID)
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
					return nil, ringapimodels.NewNetworkError("failed to recreate request body", err)
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
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}
	}

	if err != nil {
		return nil, ringapimodels.NewNetworkError("request failed after retries", err)
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
		return ringapimodels.NewNetworkError("failed to read response body", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ringapimodels.NewHTTPError(resp, string(bodyBytes))
	}

	if result != nil {
		if err := json.Unmarshal(bodyBytes, result); err != nil {
			return ringapimodels.NewBadRequestError("failed to decode response", err)
		}
	}

	return nil
}

// HTTPClient returns the underlying HTTP client
func (c *Client) HTTPClient() *http.Client {
	return c.httpClient
}
