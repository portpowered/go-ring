package ring

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/pkg/dependencies/rest"
)

// Region selects a Ring account region. Regional Solutions bootstrap origins
// are not currently verified for EU or FE and must be supplied explicitly.
type Region string

const (
	RegionUS Region = "US"
	RegionEU Region = "EU"
	RegionFE Region = "FE"
)

// Endpoints overrides service origins for a client. Empty fields inherit the
// selected region's verified defaults.
type Endpoints struct {
	OAuthBaseURL     string
	APIBaseURL       string
	SolutionsBaseURL string
	SignalingURL     string
}

func WithRegion(region Region) Option { return withRegion(region) }

type withRegion Region

func (w withRegion) Apply(c *Client) error {
	region := Region(w)
	if region != RegionUS && region != RegionEU && region != RegionFE {
		return fmt.Errorf("unsupported region %q", region)
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

type endpointValidation struct {
	name, raw string
	schemes   []string
	queryOK   bool
}

func validateEndpoints(e Endpoints) error {
	for _, endpoint := range []endpointValidation{
		{"OAuthBaseURL", e.OAuthBaseURL, []string{"http", "https"}, false},
		{"APIBaseURL", e.APIBaseURL, []string{"http", "https"}, false},
		{"SolutionsBaseURL", e.SolutionsBaseURL, []string{"http", "https"}, false},
		{"SignalingURL", e.SignalingURL, []string{"ws", "wss"}, true},
	} {
		name, raw := endpoint.name, endpoint.raw
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		validScheme := false
		for _, scheme := range endpoint.schemes {
			validScheme = validScheme || (u != nil && u.Scheme == scheme)
		}
		if err != nil || u == nil || u.Host == "" || u.User != nil || (!endpoint.queryOK && u.RawQuery != "") || u.Fragment != "" || u.Opaque != "" || !validScheme {
			return fmt.Errorf("invalid %s URL %q", name, raw)
		}
	}
	return nil
}

func (c *Client) applyEndpointConfiguration() error {
	p := protocol.Profile(string(c.region))
	e := Endpoints{p.OAuthBaseURL, p.APIBaseURL, p.SolutionsBaseURL, p.SignalingURL}
	o := c.endpointOverrides
	if o.OAuthBaseURL != "" {
		e.OAuthBaseURL = strings.TrimRight(o.OAuthBaseURL, "/")
	}
	if o.APIBaseURL != "" {
		e.APIBaseURL = strings.TrimRight(o.APIBaseURL, "/")
	}
	if o.SolutionsBaseURL != "" {
		e.SolutionsBaseURL = strings.TrimRight(o.SolutionsBaseURL, "/")
	}
	if o.SignalingURL != "" {
		e.SignalingURL = o.SignalingURL
	}
	if c.signalingWebSocketOverride != "" {
		e.SignalingURL = c.signalingWebSocketOverride
	}
	c.endpoints = e
	c.signalingWebSocketURL = e.SignalingURL
	c.restClient.Apply(rest.WithEndpointBases(e.APIBaseURL, e.OAuthBaseURL))
	return nil
}
