package ring

import (
	"context"

	"github.com/google/uuid"
	"github.com/portpowered/go-ring/pkg/dependencies/rest"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// Authenticate performs full authentication flow with username/password
// If 2FA is required, it will return a Requires2FAError
func (c *Client) Authenticate(ctx context.Context, req AuthenticateRequest) (*ringapimodels.AuthResponse, error) {
	// Generate hardware ID if not set
	hardwareID := c.hardwareID
	if hardwareID == "" {
		hardwareID = uuid.New().String()
		c.hardwareID = hardwareID
		c.restClient.Apply(rest.WithHardwareID(hardwareID))
	}

	// Perform authentication
	tokenResp, err := c.restClient.Authenticate(ctx, req.Username, req.Password, hardwareID, req.OTPCode)
	if err != nil {
		if ringapimodels.IsRequires2FAError(err) {
			return nil, err
		}
		return nil, err
	}

	// Store tokens
	c.accessToken = tokenResp.AccessToken
	c.refreshToken = tokenResp.RefreshToken
	c.restClient.Apply(rest.WithAccessToken(tokenResp.AccessToken))

	return &ringapimodels.AuthResponse{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresIn:    tokenResp.ExpiresIn,
		TokenType:    tokenResp.TokenType,
	}, nil
}

// Request2FACode requests a 2FA code by attempting authentication
func (c *Client) Request2FACode(ctx context.Context, req Request2FACodeRequest) error {
	hardwareID := c.hardwareID
	if hardwareID == "" {
		hardwareID = uuid.New().String()
		c.hardwareID = hardwareID
		c.restClient.Apply(rest.WithHardwareID(hardwareID))
	}

	return c.restClient.Request2FACode(ctx, req.Username, req.Password, hardwareID)
}

// RefreshToken refreshes an access token using a refresh token
func (c *Client) RefreshToken(ctx context.Context, req RefreshTokenRequest) (*ringapimodels.AuthResponse, error) {
	tokenResp, err := c.restClient.RefreshAccessToken(ctx, req.RefreshToken)
	if err != nil {
		return nil, err
	}

	// Update stored tokens
	c.accessToken = tokenResp.AccessToken
	if tokenResp.RefreshToken != "" {
		c.refreshToken = tokenResp.RefreshToken
	}
	c.restClient.Apply(rest.WithAccessToken(tokenResp.AccessToken))

	return &ringapimodels.AuthResponse{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresIn:    tokenResp.ExpiresIn,
		TokenType:    tokenResp.TokenType,
	}, nil
}
