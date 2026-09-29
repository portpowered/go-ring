package rest

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"

	"github.com/portpowered/go-ring/internal/generatedhttp"
	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/ringerrors"
)

// TokenResponse is generated from the OAuthToken schema in api/openapi.yaml.
type TokenResponse = generatedhttp.OAuthToken

const (
	csrfTokenKey              = "csrftoken"
	formURLEncodedContentType = "application/x-www-form-urlencoded"
)

type pkceState struct {
	verifier    string
	state       string
	csrfToken   string
	redirectURI string
	client      *http.Client
}

var csrfPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)name=["']csrf-token["'][^>]*value=["']([^"']+)["']`),
	regexp.MustCompile(`(?i)value=["']([^"']+)["'][^>]*name=["']csrf-token["']`),
	regexp.MustCompile(`(?i)name=["'](?:csrfToken|csrf_token|_csrf)["'][^>]*value=["']([^"']+)["']`),
	regexp.MustCompile(`(?i)name=["'](?:csrf-token|csrfToken)["'][^>]*content=["']([^"']+)["']`),
	regexp.MustCompile(`(?i)["']csrf[-_]?[Tt]oken["']\s*[=:]\s*["']([^"']+)["']`),
}

// Authenticate performs Ring's OAuth 2.0 authorization-code flow with PKCE.
// The pending browser session is retained between the initial 2FA request and
// the subsequent call containing the verification code.
func (c *Client) Authenticate(
	ctx context.Context,
	username, password, hardwareID, otpCode string,
) (*TokenResponse, error) {
	c.authMu.Lock()
	defer c.authMu.Unlock()

	fallback, err := c.prepareAuthentication(ctx, username, password, hardwareID, otpCode)
	if err != nil {
		return nil, err
	}

	if fallback {
		return c.authenticateLegacy(ctx, username, password, hardwareID, otpCode)
	}

	code, err := c.authorizationCode(ctx)
	if err != nil {
		return nil, err
	}

	response, err := c.exchangeAuthorizationCode(ctx, code, hardwareID)
	if err != nil {
		return nil, err
	}

	c.pendingPKCE = nil

	// Ring's session APIs may reject the access token returned directly by the
	// authorization-code exchange. Rotating it once produces the normal API
	// access token and also ensures callers persist the current refresh token.
	if response.RefreshToken != nil && *response.RefreshToken != "" {
		return c.refreshAccessToken(ctx, *response.RefreshToken, hardwareID)
	}

	return response, nil
}

func (c *Client) prepareAuthentication(
	ctx context.Context, username, password, hardwareID, otpCode string,
) (bool, error) {
	if c.pendingPKCE != nil {
		if otpCode == "" {
			return false, ringerrors.NewRequires2FAError("2FA code required")
		}

		return false, c.verify2FA(ctx, otpCode)
	}

	err := c.startPKCE(ctx, username, password, hardwareID, otpCode)
	if err == nil {
		return false, nil
	}

	var authErr *ringerrors.AuthenticationError
	if errors.As(err, &authErr) && authErr.Status == http.StatusNotFound {
		return true, nil
	}

	return false, err
}

func (c *Client) startPKCE(ctx context.Context, username, password, hardwareID, otpCode string) error {
	err := c.initiatePKCE(ctx, hardwareID)
	if err != nil {
		return err
	}

	requires2FA, err := c.submitCredentials(ctx, username, password)
	if err != nil {
		return err
	}

	if requires2FA && otpCode == "" {
		return ringerrors.NewRequires2FAError("2FA code required")
	}

	if requires2FA {
		return c.verify2FA(ctx, otpCode)
	}

	return nil
}

// authenticateLegacy preserves compatibility with older Ring deployments and
// with callers that provide an HTTP test double for the historical endpoint.
func (c *Client) authenticateLegacy(
	ctx context.Context,
	username, password, hardwareID, otpCode string,
) (*TokenResponse, error) {
	data := url.Values{
		"grant_type": {"password"}, "username": {username}, "password": {password},
		"client_id": {protocol.RingClientID}, "scope": {protocol.RingScope},
	}

	req, err := generatedhttp.NewExchangeOrRefreshOAuthTokenRequestWithBody(
		generatedServerBase(c.oauthBaseURI), formURLEncodedContentType, strings.NewReader(data.Encode()),
	)
	if err != nil {
		return nil, ringerrors.NewNetworkError("failed to create legacy auth request", err)
	}

	req = req.WithContext(ctx)

	req.Header.Set(string(generatedhttp.ContentType), formURLEncodedContentType)
	req.Header.Set(string(generatedhttp.UserAgent), c.userAgent)

	if hardwareID != "" {
		req.Header.Set(string(generatedhttp.HardwareId), hardwareID)
	}

	if otpCode != "" {
		req.Header.Set(string(generatedhttp.N2faSupport), "true")
		req.Header.Set(string(generatedhttp.N2faCode), otpCode)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, ringerrors.NewNetworkError("legacy auth request failed", err)
	}

	defer func() { _ = resp.Body.Close() }()

	return decodeTokenResponse(resp)
}

// Request2FACode starts the PKCE login flow and causes Ring to deliver a code.
func (c *Client) Request2FACode(ctx context.Context, username, password, hardwareID string) error {
	_, err := c.Authenticate(ctx, username, password, hardwareID, "")
	if ringerrors.IsRequires2FAError(err) {
		return nil
	}

	return err
}

func (c *Client) initiatePKCE(ctx context.Context, hardwareID string) error {
	const (
		verifierBytesCount = 32
		stateBytesCount    = 16
	)

	verifierBytes := make([]byte, verifierBytesCount)
	stateBytes := make([]byte, stateBytesCount)

	{
		_, err := rand.Read(verifierBytes)
		if err != nil {
			return ringerrors.NewInternalServerError("failed to generate PKCE verifier", err)
		}
	}

	{
		_, err := rand.Read(stateBytes)
		if err != nil {
			return ringerrors.NewInternalServerError("failed to generate OAuth state", err)
		}
	}

	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	challengeBytes := sha256.Sum256([]byte(verifier))
	state := hex.EncodeToString(stateBytes)
	redirectURI := protocol.OAuthCallbackURL

	jar, err := cookiejar.New(nil)
	if err != nil {
		return ringerrors.NewInternalServerError("failed to create OAuth cookie jar", err)
	}

	authClientCopy := *c.httpClient
	authClientCopy.Jar = jar
	authClientCopy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	authClient := &authClientCopy

	params := oauthAuthorizationValues(redirectURI, state, hardwareID, challengeBytes)
	currentURL := strings.TrimSuffix(c.oauthBaseURI, "/") + protocol.OAuthAuthorizePath + "?" + params.Encode()

	var html string

	for range 5 {
		resp, err := c.sendOAuthAuthorize(ctx, authClient, currentURL)
		if err != nil {
			return err
		}

		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			location := resp.Header.Get("Location")

			_ = resp.Body.Close()

			if location == "" {
				return ringerrors.NewAuthenticationError("OAuth redirect missing location", resp.StatusCode)
			}

			next, err := resolveOAuthURL(currentURL, location)
			if err != nil {
				return ringerrors.NewAuthenticationError("invalid OAuth redirect", resp.StatusCode)
			}

			currentURL = next

			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if readErr != nil {
			return ringerrors.NewNetworkError("failed to read OAuth sign-in page", readErr)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return ringerrors.NewAuthenticationError("failed to load OAuth sign-in page", resp.StatusCode)
		}

		html = string(body)

		break
	}

	csrfToken := extractCSRF(html, jar, c.oauthBaseURI)
	if csrfToken == "" {
		return ringerrors.NewAuthenticationError(
			"unable to extract CSRF token from Ring OAuth page",
			http.StatusUnauthorized,
		)
	}

	c.pendingPKCE = &pkceState{
		verifier:    verifier,
		state:       state,
		csrfToken:   csrfToken,
		redirectURI: redirectURI,
		client:      authClient,
	}

	return nil
}

func oauthAuthorizationValues(redirectURI, state, hardwareID string, challengeBytes [32]byte) url.Values {
	return url.Values{
		"redirect_uri":          {redirectURI},
		"client_id":             {protocol.RingClientID},
		"response_type":         {"code"},
		"prompt":                {"login"},
		"state":                 {state},
		"scope":                 {protocol.RingScope},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challengeBytes[:])},
		"code_challenge_method": {"S256"},
		"device_model":          {deviceModel},
		"app_version":           {"3.102.0"},
		"dark_mode":             {"false"},
		"device_brand":          {"golang"},
		"device_os_version":     {"go"},
		"app_brand":             {"ring"},
		"hardware_id":           {hardwareID},
	}
}

func (c *Client) submitCredentials(ctx context.Context, username, password string) (bool, error) {
	pending := c.pendingPKCE
	form := url.Values{"username": {username}, "password": {password}, "csrf-token": {pending.csrfToken}}

	resp, body, err := c.authFormRequest(ctx, pending.client, "/oauth/v2/signin", form)
	if err != nil {
		return false, err
	}

	var payload generatedhttp.SignInState

	_ = json.Unmarshal(body, &payload)
	location := resp.Header.Get("Location")

	requires2FA := resp.StatusCode == http.StatusPreconditionFailed ||
		(payload.TsvState != nil && *payload.TsvState != "") ||
		payload.NextTimeInSecs != nil ||
		strings.Contains(location, "/2fa")
	if requires2FA {
		return true, nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return false, ringerrors.NewAuthenticationError("Ring sign-in rejected credentials", resp.StatusCode)
	}

	return false, nil
}

func (c *Client) verify2FA(ctx context.Context, code string) error {
	pending := c.pendingPKCE
	form := url.Values{"2fa_code": {code}, "csrf-token": {pending.csrfToken}, "remember_me": {"false"}}

	resp, _, err := c.authFormRequest(ctx, pending.client, "/oauth/v2/2fa/verify", form)
	if err != nil {
		return err
	}

	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized {
		return ringerrors.NewAuthenticationError("verification code is invalid or expired", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return ringerrors.NewAuthenticationError("2FA verification failed", resp.StatusCode)
	}

	return nil
}

type authFormResponse struct {
	StatusCode int
	Header     http.Header
}

func (c *Client) authFormRequest(
	ctx context.Context,
	client *http.Client,
	path string,
	form url.Values,
) (*authFormResponse, []byte, error) {
	var (
		req *http.Request
		err error
	)

	switch path {
	case "/oauth/v2/signin":
		req, err = generatedhttp.NewSubmitOAuthCredentialsRequestWithBody(
			generatedServerBase(c.oauthBaseURI), formURLEncodedContentType, strings.NewReader(form.Encode()),
		)
	case "/oauth/v2/2fa/verify":
		req, err = generatedhttp.NewVerifyOAuthTwoFactorCodeRequestWithBody(
			generatedServerBase(c.oauthBaseURI), formURLEncodedContentType, strings.NewReader(form.Encode()),
		)
	default:
		return nil, nil, ringerrors.NewBadRequestError("unsupported OAuth form route", nil)
	}

	if err != nil {
		return nil, nil, ringerrors.NewNetworkError("failed to create OAuth request", err)
	}

	req = req.WithContext(ctx)

	req.Header.Set(string(generatedhttp.ContentType), formURLEncodedContentType)
	req.Header.Set(string(generatedhttp.UserAgent), c.userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, ringerrors.NewNetworkError("OAuth request failed", err)
	}

	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if readErr != nil {
		return nil, nil, ringerrors.NewNetworkError("failed to read OAuth response", readErr)
	}

	return &authFormResponse{StatusCode: resp.StatusCode, Header: resp.Header.Clone()}, body, nil
}

func (c *Client) authorizationCode(ctx context.Context) (string, error) {
	pending := c.pendingPKCE

	currentURL := strings.TrimSuffix(c.oauthBaseURI, "/") + protocol.OAuthAuthorizePath
	for range 5 {
		resp, err := c.sendOAuthAuthorize(ctx, pending.client, currentURL)
		if err != nil {
			return "", err
		}

		location := resp.Header.Get("Location")

		_ = resp.Body.Close()

		if resp.StatusCode < 300 || resp.StatusCode >= 400 || location == "" {
			return "", ringerrors.NewAuthenticationError("OAuth authorize endpoint did not redirect", resp.StatusCode)
		}

		next, err := resolveOAuthURL(currentURL, location)
		if err != nil {
			return "", ringerrors.NewAuthenticationError("invalid OAuth authorization redirect", resp.StatusCode)
		}

		redirect, err := url.Parse(next)
		if err == nil && redirect.Query().Get("code") != "" {
			if redirect.Query().Get("state") != pending.state {
				return "", ringerrors.NewAuthenticationError("OAuth state mismatch", http.StatusUnauthorized)
			}

			return redirect.Query().Get("code"), nil
		}

		currentURL = next
	}

	return "", ringerrors.NewAuthenticationError(
		"OAuth authorization code redirect limit exceeded",
		http.StatusUnauthorized,
	)
}

func (c *Client) exchangeAuthorizationCode(ctx context.Context, code, hardwareID string) (*TokenResponse, error) {
	pending := c.pendingPKCE
	form := url.Values{
		"code": {code}, "grant_type": {"authorization_code"}, "redirect_uri": {pending.redirectURI},
		"code_verifier": {pending.verifier}, "client_id": {protocol.RingClientID},
	}

	req, err := generatedhttp.NewExchangeOrRefreshOAuthTokenRequestWithBody(
		generatedServerBase(c.oauthBaseURI), formURLEncodedContentType, strings.NewReader(form.Encode()),
	)
	if err != nil {
		return nil, ringerrors.NewNetworkError("failed to create OAuth token request", err)
	}

	req = req.WithContext(ctx)

	req.Header.Set(string(generatedhttp.ContentType), formURLEncodedContentType)
	req.Header.Set(string(generatedhttp.Accept), "application/json")
	req.Header.Set(string(generatedhttp.UserAgent), c.userAgent)
	req.Header.Set(string(generatedhttp.HardwareId), hardwareID)

	resp, err := pending.client.Do(req)
	if err != nil {
		return nil, ringerrors.NewNetworkError("failed to exchange OAuth authorization code", err)
	}

	defer func() { _ = resp.Body.Close() }()

	return decodeTokenResponse(resp)
}

// RefreshAccessToken refreshes an access token using a refresh token.
func (c *Client) RefreshAccessToken(ctx context.Context, refreshToken string) (*TokenResponse, error) {
	return c.refreshAccessToken(ctx, refreshToken, c.hardwareID)
}

// RefreshAccessTokenFor uses a caller-supplied device identity without
// changing authorization state on a shared REST client.
func (c *Client) RefreshAccessTokenFor(ctx context.Context, refreshToken, hardwareID string) (*TokenResponse, error) {
	return c.refreshAccessToken(ctx, refreshToken, hardwareID)
}

func (c *Client) refreshAccessToken(ctx context.Context, refreshToken, hardwareID string) (*TokenResponse, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {protocol.RingClientID},
		"scope":         {protocol.RingScope},
	}

	req, err := generatedhttp.NewExchangeOrRefreshOAuthTokenRequestWithBody(
		generatedServerBase(c.oauthBaseURI), formURLEncodedContentType, strings.NewReader(data.Encode()),
	)
	if err != nil {
		return nil, ringerrors.NewNetworkError("failed to create refresh request", err)
	}

	req = req.WithContext(ctx)

	req.Header.Set(string(generatedhttp.ContentType), formURLEncodedContentType)
	req.Header.Set(string(generatedhttp.Accept), "application/json")
	req.Header.Set(string(generatedhttp.UserAgent), c.userAgent)

	if hardwareID != "" {
		req.Header.Set(string(generatedhttp.HardwareId), hardwareID)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, ringerrors.NewNetworkError("failed to refresh access token", err)
	}

	defer func() { _ = resp.Body.Close() }()

	return decodeTokenResponse(resp)
}

func decodeTokenResponse(resp *http.Response) (*TokenResponse, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, ringerrors.NewNetworkError("failed to read token response", err)
	}

	if resp.StatusCode == http.StatusPreconditionFailed {
		return nil, ringerrors.NewRequires2FAError("2FA code required")
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, ringerrors.NewRateLimitError("rate limit exceeded")
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ringerrors.NewAuthenticationError("token request rejected", resp.StatusCode)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, ringerrors.ClassifyHTTPError(resp, string(body))
	}

	var tokenResponse TokenResponse
	{
		err := json.Unmarshal(body, &tokenResponse)
		if err != nil {
			return nil, ringerrors.NewInternalServerError("failed to parse token response", err)
		}
	}

	if tokenResponse.AccessToken == "" {
		return nil, ringerrors.NewInternalServerError("token response missing access token", nil)
	}

	return &tokenResponse, nil
}

func resolveOAuthURL(base, location string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", ringerrors.NewInternalServerError("invalid OAuth base URL", err)
	}

	next, err := url.Parse(location)
	if err != nil {
		return "", ringerrors.NewBadRequestError("invalid OAuth redirect URL", err)
	}

	return baseURL.ResolveReference(next).String(), nil
}

func extractCSRF(html string, jar http.CookieJar, oauthBase string) string {
	for _, rawURL := range []string{oauthBase, oauthBase + protocol.OAuthSigninPath} {
		parsed, _ := url.Parse(rawURL)
		for _, cookie := range jar.Cookies(parsed) {
			switch strings.ToLower(cookie.Name) {
			case "csrf-token", csrfTokenKey, "csrf_token", "_csrf", "xsrf-token":
				if cookie.Value != "" {
					return cookie.Value
				}
			}
		}
	}

	for _, pattern := range csrfPatterns {
		if match := pattern.FindStringSubmatch(html); len(match) == 2 {
			return match[1]
		}
	}

	for _, scriptID := range []string{"oauth-args", "__NEXT_DATA__"} {
		pattern := regexp.MustCompile(
			`(?s)<script[^>]*id=["']` + regexp.QuoteMeta(scriptID) + `["'][^>]*>(.*?)</script>`,
		)
		if match := pattern.FindStringSubmatch(html); len(match) == 2 {
			var value any
			if json.Unmarshal([]byte(match[1]), &value) == nil {
				if token := findCSRF(value, 0); token != "" {
					return token
				}
			}
		}
	}

	return ""
}

func findCSRF(value any, depth int) string {
	if depth > maxCSRFSearchDepth {
		return ""
	}

	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.NewReplacer("-", "", "_", "").Replace(strings.ToLower(key))
			if normalized == csrfTokenKey || normalized == "csrf" {
				if token, ok := child.(string); ok {
					return token
				}
			}

			if token := findCSRF(child, depth+1); token != "" {
				return token
			}
		}
	case []any:
		for _, child := range typed {
			if token := findCSRF(child, depth+1); token != "" {
				return token
			}
		}
	}

	return ""
}
