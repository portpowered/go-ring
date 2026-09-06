package unit

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

type pkceMockTransport struct {
	state    string
	requests []*http.Request
	bodies   []string
}

func (m *pkceMockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	m.requests = append(m.requests, req.Clone(req.Context()))
	m.bodies = append(m.bodies, string(body))

	respond := func(status int, body string, headers http.Header) *http.Response {
		if headers == nil {
			headers = make(http.Header)
		}
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: req}
	}

	switch {
	case req.Method == http.MethodGet && req.URL.Path == "/oauth/v2/authorize" && req.URL.Query().Get("response_type") == "code":
		m.state = req.URL.Query().Get("state")
		return respond(http.StatusOK, `<script id="oauth-args">{"csrf-token":"csrf-value"}</script>`, nil), nil
	case req.Method == http.MethodPost && req.URL.Path == "/oauth/v2/signin":
		return respond(http.StatusPreconditionFailed, `{"tsv_state":"email"}`, nil), nil
	case req.Method == http.MethodPost && req.URL.Path == "/oauth/v2/2fa/verify":
		return respond(http.StatusOK, `{}`, nil), nil
	case req.Method == http.MethodGet && req.URL.Path == "/oauth/v2/authorize":
		headers := make(http.Header)
		headers.Set("Location", "https://ring.com/signin/callback?code=auth-code&state="+url.QueryEscape(m.state))
		return respond(http.StatusFound, "", headers), nil
	case req.Method == http.MethodPost && req.URL.Path == "/oauth/token":
		if strings.Contains(string(body), "grant_type=refresh_token") {
			return respond(http.StatusOK, `{"access_token":"pkce-access-refreshed","refresh_token":"pkce-refresh-rotated","expires_in":14400,"token_type":"Bearer"}`, nil), nil
		}
		return respond(http.StatusOK, `{"access_token":"pkce-access","refresh_token":"pkce-refresh","expires_in":14400,"token_type":"Bearer"}`, nil), nil
	default:
		return respond(http.StatusNotFound, `{}`, nil), nil
	}
}

func TestAuthenticate_PKCEWith2FA(t *testing.T) {
	transport := &pkceMockTransport{}
	client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}))
	require.NoError(t, err)
	defer client.Close()
	ctx := newTestContext()

	err = client.Request2FACode(ctx, ring.Request2FACodeRequest{Username: "testuser", Password: "testpass"})
	require.NoError(t, err)
	authResp, err := client.Authenticate(ctx, ring.AuthenticateRequest{Username: "testuser", Password: "testpass", OTPCode: "123456"})
	require.NoError(t, err)
	assert.Equal(t, "pkce-access-refreshed", authResp.AccessToken)
	assert.Equal(t, "pkce-refresh-rotated", authResp.RefreshToken)
	require.Len(t, transport.requests, 6)
	assert.Equal(t, "S256", transport.requests[0].URL.Query().Get("code_challenge_method"))
	assert.Contains(t, transport.bodies[1], "csrf-token=csrf-value")
	assert.Contains(t, transport.bodies[2], "2fa_code=123456")
	assert.Contains(t, transport.bodies[4], "grant_type=authorization_code")
	assert.Contains(t, transport.bodies[5], "grant_type=refresh_token")
}

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
	require.Len(t, requests, 2)
	req := requests[1]
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
	require.Len(t, requests, 2)
	req := requests[1]
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
	require.Len(t, requests, 2)
	req := requests[1]
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
