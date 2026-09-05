package unit

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

func TestAuthenticate_Success(t *testing.T) {
	client, mockTransport := newTestClient()
	defer client.Close()

	ctx := newTestContext()

	// Authenticate should use the oauth endpoint
	authResp, err := client.Authenticate(ctx, ring.AuthenticateRequest{
		Username: "testuser",
		Password: "testpass",
		OTPCode:  "",
	})
	require.NoError(t, err)
	require.NotNil(t, authResp)

	// Verify response
	assert.Equal(t, "dummyBearerToken", authResp.AccessToken)
	assert.Equal(t, "bearer", authResp.TokenType)
	assert.Equal(t, 3600, authResp.ExpiresIn)
	assert.Equal(t, "123123456789", authResp.RefreshToken)

	// Verify request was made correctly
	requests := mockTransport.GetRequests()
	require.Len(t, requests, 1)
	req := requests[0]
	assert.Equal(t, "POST", req.Method)
	assert.Contains(t, req.URL, "/oauth/token")
	assert.Contains(t, req.BodyString, "grant_type=password")
	assert.Contains(t, req.BodyString, "username=testuser")
	assert.Contains(t, req.BodyString, "password=testpass")
}

func TestAuthenticate_WithOTP(t *testing.T) {
	client, mockTransport := newTestClient()
	defer client.Close()

	ctx := newTestContext()

	// Authenticate with OTP code
	authResp, err := client.Authenticate(ctx, ring.AuthenticateRequest{
		Username: "testuser",
		Password: "testpass",
		OTPCode:  "123456",
	})
	require.NoError(t, err)
	require.NotNil(t, authResp)

	// Verify request included OTP headers
	requests := mockTransport.GetRequests()
	require.Len(t, requests, 1)
	req := requests[0]
	assert.Equal(t, "true", req.Headers.Get("2fa-support"))
	assert.Equal(t, "123456", req.Headers.Get("2fa-code"))
}

func TestAuthenticate_Requires2FA(t *testing.T) {
	client, mockTransport := newTestClient()
	defer client.Close()

	// Set up a 412 response for 2FA requirement
	mockTransport.SetResponseWithBody("POST", "/oauth/token", http.StatusPreconditionFailed, map[string]string{
		"error": "2FA required",
	})

	ctx := newTestContext()
	authResp, err := client.Authenticate(ctx, ring.AuthenticateRequest{
		Username: "testuser",
		Password: "testpass",
		OTPCode:  "",
	})

	// Should return a Requires2FAError
	assert.Nil(t, authResp)
	assert.Error(t, err)
	assert.True(t, ringapimodels.IsRequires2FAError(err))
}

func TestAuthenticate_InvalidCredentials(t *testing.T) {
	client, mockTransport := newTestClient()
	defer client.Close()

	// Set up a 401 response for invalid credentials
	mockTransport.SetResponseWithBody("POST", "/oauth/token", http.StatusUnauthorized, map[string]string{
		"error": "invalid_credentials",
	})

	ctx := newTestContext()
	authResp, err := client.Authenticate(ctx, ring.AuthenticateRequest{
		Username: "baduser",
		Password: "badpass",
		OTPCode:  "",
	})

	assert.Nil(t, authResp)
	assert.Error(t, err)
	assert.True(t, ringapimodels.IsAuthenticationError(err))
}

func TestRequest2FACode(t *testing.T) {
	client, mockTransport := newTestClient()
	defer client.Close()

	// Set up a 412 response for 2FA requirement
	mockTransport.SetResponseWithBody("POST", "/oauth/token", http.StatusPreconditionFailed, map[string]string{
		"error": "2FA required",
	})

	ctx := newTestContext()
	err := client.Request2FACode(ctx, ring.Request2FACodeRequest{
		Username: "testuser",
		Password: "testpass",
	})

	// Should succeed (2FA requirement is expected)
	assert.NoError(t, err)

	// Verify request was made
	requests := mockTransport.GetRequests()
	require.Len(t, requests, 1)
	req := requests[0]
	assert.Equal(t, "POST", req.Method)
	assert.Contains(t, req.URL, "/oauth/token")
}

func TestRefreshToken_Success(t *testing.T) {
	client, mockTransport := newTestClient()
	defer client.Close()

	ctx := newTestContext()
	authResp, err := client.RefreshToken(ctx, ring.RefreshTokenRequest{
		RefreshToken: "123123456789",
	})

	require.NoError(t, err)
	require.NotNil(t, authResp)

	// Verify response
	assert.Equal(t, "dummyBearerToken", authResp.AccessToken)
	assert.Equal(t, "bearer", authResp.TokenType)
	assert.Equal(t, 3600, authResp.ExpiresIn)

	// Verify request was made correctly
	requests := mockTransport.GetRequests()
	require.Len(t, requests, 1)
	req := requests[0]
	assert.Equal(t, "POST", req.Method)
	assert.Contains(t, req.URL, "/oauth/token")
	assert.Contains(t, req.BodyString, "grant_type=refresh_token")
	assert.Contains(t, req.BodyString, "refresh_token=123123456789")
}

func TestRefreshToken_InvalidToken(t *testing.T) {
	client, mockTransport := newTestClient()
	defer client.Close()

	// Set up a 401 response for invalid refresh token
	mockTransport.SetResponseWithBody("POST", "/oauth/token", http.StatusUnauthorized, map[string]string{
		"error": "invalid_refresh_token",
	})

	ctx := newTestContext()
	authResp, err := client.RefreshToken(ctx, ring.RefreshTokenRequest{
		RefreshToken: "invalid_token",
	})

	assert.Nil(t, authResp)
	assert.Error(t, err)
	assert.True(t, ringapimodels.IsAuthenticationError(err))
}

func TestNewClientWithToken(t *testing.T) {
	token := "test_token_12345"
	client, err := ring.NewClientWithToken(token)
	require.NoError(t, err)
	defer client.Close()

	// Client should be created with the token
	assert.NotNil(t, client)
}

func TestClientOptions(t *testing.T) {
	client, mockTransport := newTestClient()
	defer client.Close()

	// Test applying options
	err := client.Apply(
		ring.WithAccessToken("new_token"),
		ring.WithHardwareID("test_hardware_id"),
		ring.WithUserAgent("test_agent"),
	)
	assert.NoError(t, err)

	// Verify client was configured (indirectly by making a request)
	ctx := newTestContext()
	_, _ = client.ListDevices(ctx)

	requests := mockTransport.GetRequests()
	if len(requests) > 0 {
		req := requests[0]
		assert.Equal(t, "test_agent", req.Headers.Get("User-Agent"))
		assert.Equal(t, "test_hardware_id", req.Headers.Get("hardware_id"))
	}
}
