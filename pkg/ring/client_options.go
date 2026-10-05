package ring

import (
	"fmt"
	"net/http"

	"github.com/portpowered/go-ring/pkg/dependencies/rest"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

func WithRegion(region Region) Option { return withRegion(region) }

type withRegion Region

func (w withRegion) apply(client *Client) error {
	region := Region(w)
	if region != RegionUS && region != RegionEU && region != RegionFE {
		return ringapimodels.NewBadRequestError(fmt.Sprintf("unsupported region %q", region), nil)
	}

	client.region = region

	return client.applyEndpointConfiguration()
}

// WithEndpoints overrides endpoint origins for this client. Overrides take
// precedence over the selected region regardless of option order.
func WithEndpoints(endpoints Endpoints) Option { return withEndpoints(endpoints) }

type withEndpoints Endpoints

func (w withEndpoints) apply(client *Client) error {
	err := validateEndpoints(Endpoints(w))
	if err != nil {
		return err
	}

	client.endpointOverrides = Endpoints(w)

	return client.applyEndpointConfiguration()
}

// WithHTTPClient sets a custom HTTP client for REST API calls.
func WithHTTPClient(httpClient *http.Client) Option {
	return withHTTPClient{httpClient: httpClient}
}

type withHTTPClient struct {
	httpClient *http.Client
}

func (w withHTTPClient) apply(client *Client) error {
	if w.httpClient == nil {
		return ringapimodels.NewBadRequestError("HTTP client must not be nil", nil)
	}

	if w.httpClient.Jar != nil {
		return ringapimodels.NewBadRequestError("shared HTTP client must not have a cookie jar", nil)
	}

	// Keep the caller's transport, but snapshot the mutable client fields so a
	// Jar assigned after construction cannot become shared account state.
	httpClient := *w.httpClient
	client.restClient.Apply(rest.WithHTTPClient(&httpClient))

	return nil
}

// WithUserAgent sets a custom user agent.
func WithUserAgent(userAgent string) Option {
	return withUserAgent(userAgent)
}

type withUserAgent string

func (w withUserAgent) apply(c *Client) error {
	ua := string(w)
	c.userAgent = ua
	c.restClient.Apply(
		rest.WithUserAgent(ua),
	)

	return nil
}

// WithWebSocketDialer configures the event and signaling WebSocket transport.
func WithWebSocketDialer(d WebSocketDialer) Option { return withWebSocketDialer{dialer: d} }

type withWebSocketDialer struct{ dialer WebSocketDialer }

func (o withWebSocketDialer) apply(c *Client) error {
	if o.dialer == nil {
		return ringapimodels.NewBadRequestError("WebSocket dialer must not be nil", nil)
	}

	c.websocketDialer = o.dialer

	return nil
}

// WithSignalingWebSocketURL explicitly overrides the configured signaling URL. It
// takes precedence over WithEndpoints and the selected region, regardless of
// option order.
func WithSignalingWebSocketURL(url string) Option {
	return withSignalingWebSocketURL(url)
}

type withSignalingWebSocketURL string

func (w withSignalingWebSocketURL) apply(client *Client) error {
	err := validateEndpoints(Endpoints{
		OAuthBaseURL:     "",
		APIBaseURL:       "",
		SolutionsBaseURL: "",
		SignalingURL:     string(w),
	})
	if err != nil {
		return err
	}

	client.signalingWebSocketURL = string(w)
	client.signalingWebSocketOverride = string(w)

	return nil
}

// WithEventWebSocketURL configures the event endpoint, including local test servers.
// The default vendor endpoint is experimental; callers must verify vendor support.
func WithEventWebSocketURL(url string) Option {
	return withEventWebSocketURL(url)
}

type withEventWebSocketURL string

var _ Option = withEventWebSocketURL("")

func (w withEventWebSocketURL) apply(client *Client) error {
	err := validateEventWebSocketURL(string(w))
	if err != nil {
		return err
	}

	client.eventWebSocketURL = string(w)

	return nil
}
