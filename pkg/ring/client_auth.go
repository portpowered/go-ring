package ring

import (
	"context"

	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// Authenticate performs an isolated exchange and never binds its token to Client.
// Use NewLoginSession when a 2FA challenge must be completed in the same flow.
func (c *Client) Authenticate(ctx context.Context, req AuthenticateRequest) (*ringapimodels.AuthResponse, error) {
	session, err := c.NewLoginSession(LoginSessionRequest{Username: req.Username, Password: req.Password, HardwareID: req.HardwareID})
	if err != nil {
		return nil, err
	}
	defer session.Close()
	return session.Authenticate(ctx, CompleteLoginRequest{OTPCode: req.OTPCode})
}

// Request2FACode starts an isolated exchange. NewLoginSession retains its
// challenge when the caller needs to follow it with Authenticate.
func (c *Client) Request2FACode(ctx context.Context, req Request2FACodeRequest) error {
	session, err := c.NewLoginSession(LoginSessionRequest(req))
	if err != nil {
		return err
	}
	defer session.Close()
	return session.Request2FACode(ctx)
}

// RefreshToken refreshes an access token using a refresh token
func (c *Client) RefreshToken(ctx context.Context, req RefreshTokenRequest) (*ringapimodels.AuthResponse, error) {
	if req.RefreshToken == "" {
		return nil, ringapimodels.NewBadRequestError("refresh token is required", nil)
	}
	tokenResp, err := c.restClient.RefreshAccessTokenFor(ctx, req.RefreshToken, req.HardwareID)
	if err != nil {
		return nil, err
	}

	return &ringapimodels.AuthResponse{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenString(tokenResp.RefreshToken),
		ExpiresIn:    tokenInt(tokenResp.ExpiresIn),
		TokenType:    tokenResp.TokenType,
	}, nil
}

func tokenString(value *string) string {
	if value != nil {
		return *value
	}
	return ""
}

func tokenInt(value *int) int {
	if value != nil {
		return *value
	}
	return 0
}
