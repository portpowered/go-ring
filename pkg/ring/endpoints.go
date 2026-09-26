package ring

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/pkg/dependencies/rest"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

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
			return ringapimodels.NewBadRequestError(fmt.Sprintf("invalid %s URL %q", name, raw), err)
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
