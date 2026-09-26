package ring

import (
	"context"
	"fmt"
	"net/http"

	"github.com/portpowered/go-ring/pkg/dependencies/rest"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

func WithRegion(region Region) Option { return withRegion(region) }

type withRegion Region

func (w withRegion) Apply(c *Client) error {
	region := Region(w)
	if region != RegionUS && region != RegionEU && region != RegionFE {
		return ringapimodels.NewBadRequestError(fmt.Sprintf("unsupported region %q", region), nil)
	}
	c.region = region
	return c.applyEndpointConfiguration()
}

// WithEndpoints overrides endpoint origins for this client. Overrides take
// precedence over the selected region regardless of option order.
func WithEndpoints(endpoints Endpoints) Option { return withEndpoints(endpoints) }

type withEndpoints Endpoints

func (w withEndpoints) Apply(c *Client) error {
	if err := validateEndpoints(Endpoints(w)); err != nil {
		return err
	}
	c.endpointOverrides = Endpoints(w)
	return c.applyEndpointConfiguration()
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
	if w.httpClient == nil {
		return ringapimodels.NewBadRequestError("HTTP client must not be nil", nil)
	}
	if w.httpClient.Jar != nil {
		return ringapimodels.NewBadRequestError("shared HTTP client must not have a cookie jar", nil)
	}
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

func WithWebSocketDialer(d WebSocketDialer) Option { return WithSignalingDialerOption{Dialer: d} }
func (o WithSignalingDialerOption) Apply(c *Client) error {
	if o.Dialer == nil {
		return ringapimodels.NewBadRequestError("WebSocket dialer must not be nil", nil)
	}
	c.signalingDialer = o.Dialer
	return nil
}

// WithSignalingWebSocketURL explicitly overrides the configured signaling URL. It
// takes precedence over WithEndpoints and the selected region, regardless of
// option order.
func WithSignalingWebSocketURL(url string) Option {
	return withSignalingWebSocketURL(url)
}

type withSignalingWebSocketURL string

func (w withSignalingWebSocketURL) Apply(c *Client) error {
	if err := validateEndpoints(Endpoints{SignalingURL: string(w)}); err != nil {
		return err
	}
	c.signalingWebSocketURL = string(w)
	c.signalingWebSocketOverride = string(w)
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
