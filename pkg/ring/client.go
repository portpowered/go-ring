// Package ring provides the main client for interacting with Ring services.
// It orchestrates authentication, API clients (REST), and event connections.
package ring

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/requestauth"
	"github.com/portpowered/go-ring/pkg/dependencies/rest"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// NewClient creates a new Ring client.
func NewClient(opts ...Option) (*Client, error) {
	client := &Client{
		restClient:        rest.NewClient(),
		userAgent:         protocol.DefaultUserAgent,
		region:            RegionUS,
		eventWebSocketURL: protocol.ExperimentalEventWebSocketURL,
	}

	err := client.applyEndpointConfiguration()
	if err != nil {
		return nil, err
	}

	for _, opt := range opts {
		err := opt.apply(client)
		if err != nil {
			return nil, err
		}
	}

	return client, nil
}

type accessTokenClaims struct {
	HardwareID string `json:"hardware_id"`
}

func hardwareIDFromAccessToken(accessToken string) string {
	parts := strings.Split(accessToken, ".")
	if len(parts) != protocol.JWTCompactSegmentCount {
		return ""
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}

	var claims accessTokenClaims
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}

	return claims.HardwareID
}

func (c *Client) ensureSession(ctx context.Context) error {
	auth, ok := requestauth.FromContext(ctx)
	if !ok || auth.HardwareID == "" {
		return nil
	}

	return c.restClient.RegisterSession(ctx)
}

// getToken retrieves the access token.
func (c *Client) getToken(ctx context.Context) (string, error) {
	if auth, ok := requestauth.FromContext(ctx); ok {
		if auth.AccessToken == "" {
			return "", ringapimodels.NewTokenError("no token in request", nil)
		}

		return auth.AccessToken, nil
	}

	return "", ringapimodels.NewTokenError("no token in request", nil)
}

func (c *Client) accountContext(ctx context.Context, auth AuthContext) context.Context {
	hardwareID := auth.HardwareID
	if hardwareID == "" {
		hardwareID = hardwareIDFromAccessToken(auth.AccessToken)
	}

	return requestauth.WithAccount(ctx, requestauth.Account{
		AccessToken: auth.AccessToken,
		HardwareID:  hardwareID,
	})
}

func (c *Client) hardwareIDFor(ctx context.Context) string {
	if auth, ok := requestauth.FromContext(ctx); ok {
		return auth.HardwareID
	}

	return ""
}

// Close is retained for source compatibility. Connection lifetimes belong to
// the objects returned by connection-opening methods; close those objects to
// end their connections. Client currently owns no long-lived connections.
func (c *Client) Close() error {
	return nil
}
