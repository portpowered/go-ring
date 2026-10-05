// Package rest provides a REST API client for interacting with Ring services.
// It supports authentication, device enumeration, control, and state management via HTTP/RESTful APIs.
package rest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/portpowered/go-ring/internal/generatedhttp"
	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/requestauth"
	"github.com/portpowered/go-ring/internal/ringerrors"
)

// Client is a REST API client for Ring services.
type Client struct {
	httpClient       *http.Client
	configurationErr error
	baseURI          string
	oauthBaseURI     string
	userAgent        string
	hardwareID       string
	authMu           sync.Mutex
	pendingPKCE      *pkceState
}

// HTTPClientConfigurationError reports an unsafe or unusable HTTP client
// supplied to a reusable REST client.
type HTTPClientConfigurationError struct {
	Reason string
}

func (e *HTTPClientConfigurationError) Error() string {
	if e == nil || e.Reason == "" {
		return "HTTP client configuration error"
	}

	return "HTTP client configuration error: " + e.Reason
}

type configurationErrorTransport struct {
	err error
}

func (t configurationErrorTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return nil, t.err
}

// ClientOption is a function that configures a Client.
type ClientOption func(*Client)

// WithHTTPClient sets a custom HTTP client snapshot. A configured cookie jar
// is reported by Client.ConfigurationError and blocks requests.
func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(client *Client) {
		if httpClient == nil {
			configurationErr := &HTTPClientConfigurationError{Reason: "HTTP client must not be nil"}
			client.httpClient = &http.Client{Transport: configurationErrorTransport{err: configurationErr}}
			client.configurationErr = configurationErr

			return
		}

		httpClientSnapshot := *httpClient
		if httpClientSnapshot.Jar != nil {
			configurationErr := &HTTPClientConfigurationError{
				Reason: "a reusable REST client cannot share an HTTP cookie jar across account requests",
			}
			httpClientSnapshot.Transport = configurationErrorTransport{err: configurationErr}
			client.httpClient = &httpClientSnapshot
			client.configurationErr = configurationErr

			return
		}

		client.httpClient = &httpClientSnapshot
		client.configurationErr = nil
	}
}

// WithBaseURI sets the base URL for the Ring API.
func WithBaseURI(baseURL string) ClientOption {
	return func(c *Client) {
		c.baseURI = baseURL
	}
}

// WithEndpointBases configures per-client Ring service origins.
func WithEndpointBases(apiBase, oauthBase string) ClientOption {
	return func(c *Client) { c.baseURI, c.oauthBaseURI = apiBase, oauthBase }
}

// WithUserAgent sets a custom user agent.
func WithUserAgent(userAgent string) ClientOption {
	return func(c *Client) {
		c.userAgent = userAgent
	}
}

// WithHardwareID sets the hardware ID for authentication.
func WithHardwareID(hardwareID string) ClientOption {
	return func(c *Client) {
		c.hardwareID = hardwareID
	}
}

// NewClient creates a new REST API client.
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

// Apply applies the options to the client.
func (c *Client) Apply(opts ...ClientOption) {
	for _, opt := range opts {
		opt(c)
	}
}

// ConfigurationError returns an inspectable configuration error, if an
// option supplied an HTTP client that cannot be used safely.
func (c *Client) ConfigurationError() error {
	return c.configurationErr
}

// HTTPClient returns a shallow copy of the configured HTTP client. Its
// Transport remains shared so connection pooling and custom transports work.
func (c *Client) HTTPClient() *http.Client {
	httpClient := *c.httpClient

	return &httpClient
}

// generatedServerBase preserves a caller-supplied base path when generated
// operation paths are resolved relative to that base URL.
func generatedServerBase(base string) string {
	parsed, err := url.Parse(base)
	if err != nil {
		return base
	}

	if parsed.Path != "" && parsed.Path != "/" && parsed.Path[len(parsed.Path)-1] != '/' {
		parsed.Path += "/"
		parsed.RawPath = ""
	}

	return parsed.String()
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

func (c *Client) sendAuthorizedRequest(ctx context.Context, req *http.Request) (*http.Response, error) {
	err := ctx.Err()
	if err != nil {
		return nil, ringerrors.NewNetworkError("request canceled", err)
	}

	req = req.WithContext(ctx)

	// Get token and set authorization header
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, ringerrors.NewTokenError("failed to retrieve access token", err)
	}

	req.Header.Set(string(generatedhttp.Authorization), "Bearer "+token)

	if req.Header.Get("Content-Type") == "" {
		req.Header.Set(string(generatedhttp.ContentType), "application/json")
	}

	if req.Header.Get("Accept") == "" {
		req.Header.Set(string(generatedhttp.Accept), "application/json")
	}

	req.Header.Set(string(generatedhttp.UserAgent), c.userAgent)

	if hardwareID := c.hardwareIDFor(ctx); hardwareID != "" {
		req.Header.Set(string(generatedhttp.HardwareId), hardwareID)
	}

	return c.sendWithRetry(ctx, req)
}

func (c *Client) sendWithRetry(ctx context.Context, req *http.Request) (*http.Response, error) {
	// Retry logic
	maxRetries := 1
	if req.Method == http.MethodGet || req.Method == http.MethodHead {
		maxRetries = 3
	}

	var (
		resp *http.Response
		err  error
	)

	for retryIndex := range maxRetries {
		attemptReq := req
		if retryIndex > 0 {
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

		if retryIndex < maxRetries-1 {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			// Exponential backoff
			backoff := time.Duration(retryIndex+1) * time.Second
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

// doGeneratedJSON sends a request built by a generated OpenAPI operation.
func (c *Client) doGeneratedJSON(ctx context.Context, req *http.Request, result interface{}) error {
	resp, err := c.sendAuthorizedRequest(ctx, req)
	if err != nil {
		return err
	}

	defer func() { _ = resp.Body.Close() }()

	return decodeJSONResponse(resp, result)
}

func decodeJSONResponse(resp *http.Response, result interface{}) error {
	// Read the response body before decoding so the transport can be reused.
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return ringerrors.NewNetworkError("failed to read response body", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ringerrors.ClassifyHTTPError(resp, string(bodyBytes))
	}

	if result != nil {
		err := json.Unmarshal(bodyBytes, result)
		if err != nil {
			return ringerrors.NewInternalServerError("failed to decode response", err)
		}
	}

	return nil
}
