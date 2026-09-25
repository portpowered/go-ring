package ring

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/portpowered/go-ring/pkg/generatedapi"
)

var _ generatedapi.HTTPClient = (*Client)(nil)

// CallHTTP sends a schema-defined operation through the configured transport.
// Callers own the response body. Authentication headers are supplied by the
// caller so this also handles unauthenticated OAuth operations.
func (c *Client) CallHTTP(ctx context.Context, op generatedapi.HTTPOperation, input generatedapi.HTTPRequest) (*http.Response, error) {
	route, ok := generatedapi.HTTPRoutes[op]
	if !ok {
		return nil, fmt.Errorf("unknown HTTP operation %q", op)
	}
	path, err := generatedapi.BuildPath(op, input.PathParams)
	if err != nil {
		return nil, err
	}
	base := c.endpoints.APIBaseURL
	if strings.Contains(route.Server, "oauth.ring.com") {
		base = c.endpoints.OAuthBaseURL
	}
	if strings.Contains(route.Server, "rings.solutions") {
		base = c.endpoints.SolutionsBaseURL
	}
	if base == "" {
		return nil, fmt.Errorf("no configured server for %s", op)
	}
	url := strings.TrimRight(base, "/") + path
	if len(input.Query) != 0 {
		url += "?" + input.Query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, route.Method, url, input.Body)
	if err != nil {
		return nil, err
	}
	if input.Headers != nil {
		req.Header = input.Headers.Clone()
	}
	return c.restClient.HTTPClient().Do(req)
}
