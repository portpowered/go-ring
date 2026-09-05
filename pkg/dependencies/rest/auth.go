package rest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// TokenResponse represents the response from OAuth token endpoint
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
}

// Authenticate performs OAuth password grant authentication
func (c *Client) Authenticate(ctx context.Context, username, password, hardwareID string, otpCode string) (*TokenResponse, error) {
	data := url.Values{}
	data.Set("grant_type", "password")
	data.Set("username", username)
	data.Set("password", password)
	data.Set("client_id", ringapimodels.RingClientID)
	data.Set("scope", ringapimodels.RingScope)

	req, err := http.NewRequestWithContext(ctx, "POST", ringapimodels.RingOAuthURI, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, ringapimodels.NewNetworkError("failed to create auth request", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.userAgent)
	if hardwareID != "" {
		req.Header.Set("hardware_id", hardwareID)
	}

	// Add 2FA headers if OTP code is provided
	if otpCode != "" {
		req.Header.Set("2fa-support", "true")
		req.Header.Set("2fa-code", otpCode)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, ringapimodels.NewNetworkError("failed to make auth request", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, ringapimodels.NewNetworkError("failed to read auth response", err)
	}

	// 412 Precondition Failed means 2FA is required
	if resp.StatusCode == http.StatusPreconditionFailed {
		return nil, ringapimodels.NewRequires2FAError("2FA code required")
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, ringapimodels.NewRateLimitError("rate limit exceeded")
	}

	// 401 Unauthorized means invalid credentials
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ringapimodels.NewAuthenticationError(string(body), resp.StatusCode)
	}

	// Check for other errors
	if resp.StatusCode != http.StatusOK {
		return nil, ringapimodels.NewBadRequestError(string(body), errors.New(string(body)))
	}

	// Parse response
	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, ringapimodels.NewInternalServerError("failed to parse token response", err)
	}

	return &tokenResp, nil
}

// Request2FACode requests a 2FA code by attempting authentication
// This will return a Requires2FAError if 2FA is required, which indicates success
func (c *Client) Request2FACode(ctx context.Context, username, password, hardwareID string) error {
	_, err := c.Authenticate(ctx, username, password, hardwareID, "")
	if err != nil {
		if ringapimodels.IsRequires2FAError(err) {
			// This is expected - 2FA is required
			return nil
		}
		// Check if it's a 412 status
		if authErr, ok := err.(*ringapimodels.AuthenticationError); ok && authErr.Status == http.StatusPreconditionFailed {
			return nil
		}
		return err
	}
	// If we get here, authentication succeeded without 2FA
	return nil
}

// RefreshAccessToken refreshes an access token using a refresh token
func (c *Client) RefreshAccessToken(ctx context.Context, refreshToken string) (*TokenResponse, error) {
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", refreshToken)
	data.Set("client_id", ringapimodels.RingClientID)
	data.Set("scope", ringapimodels.RingScope)

	req, err := http.NewRequestWithContext(ctx, "POST", ringapimodels.RingOAuthURI, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, ringapimodels.NewNetworkError("failed to create refresh request", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, ringapimodels.NewNetworkError("failed to make refresh request", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, ringapimodels.NewNetworkError("failed to read refresh response", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, ringapimodels.NewAuthenticationError(string(body), resp.StatusCode)
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, ringapimodels.NewBadRequestError("failed to parse refresh response", err)
	}

	return &tokenResp, nil
}
