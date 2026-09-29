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
		if endpoint.raw == "" {
			continue
		}

		err := validateEndpointURL(endpoint)
		if err != nil {
			return err
		}
	}

	return nil
}

func validateEventWebSocketURL(raw string) error {
	return validateEndpointURL(endpointValidation{
		name: "EventWebSocketURL", raw: raw, schemes: []string{"ws", "wss"}, queryOK: true,
	})
}

func validateEndpointURL(endpoint endpointValidation) error {
	parsedURL, err := url.Parse(endpoint.raw)
	validScheme := false

	for _, scheme := range endpoint.schemes {
		validScheme = validScheme || (parsedURL != nil && parsedURL.Scheme == scheme)
	}

	if err != nil ||
		parsedURL == nil ||
		parsedURL.Host == "" ||
		parsedURL.User != nil ||
		(!endpoint.queryOK && parsedURL.RawQuery != "") ||
		parsedURL.Fragment != "" ||
		parsedURL.Opaque != "" ||
		!validScheme {
		return ringapimodels.NewBadRequestError(
			fmt.Sprintf("invalid %s URL %q", endpoint.name, endpoint.raw),
			err,
		)
	}

	return nil
}

func (c *Client) applyEndpointConfiguration() error {
	p := protocol.Profile(string(c.region))
	endpoints := Endpoints{p.OAuthBaseURL, p.APIBaseURL, p.SolutionsBaseURL, p.SignalingURL}

	overrides := c.endpointOverrides

	if overrides.OAuthBaseURL != "" {
		endpoints.OAuthBaseURL = strings.TrimRight(overrides.OAuthBaseURL, "/")
	}

	if overrides.APIBaseURL != "" {
		endpoints.APIBaseURL = strings.TrimRight(overrides.APIBaseURL, "/")
	}

	if overrides.SolutionsBaseURL != "" {
		endpoints.SolutionsBaseURL = strings.TrimRight(overrides.SolutionsBaseURL, "/")
	}

	if overrides.SignalingURL != "" {
		endpoints.SignalingURL = overrides.SignalingURL
	}

	if c.signalingWebSocketOverride != "" {
		endpoints.SignalingURL = c.signalingWebSocketOverride
	}

	c.endpoints = endpoints
	c.signalingWebSocketURL = endpoints.SignalingURL
	c.restClient.Apply(rest.WithEndpointBases(endpoints.APIBaseURL, endpoints.OAuthBaseURL))

	return nil
}
