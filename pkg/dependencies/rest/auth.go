package rest

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"

	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// TokenResponse represents the response from Ring's OAuth token endpoint.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
}

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
func (c *Client) Authenticate(ctx context.Context, username, password, hardwareID, otpCode string) (*TokenResponse, error) {
	c.authMu.Lock()
	defer c.authMu.Unlock()

	if c.pendingPKCE == nil {
		if err := c.initiatePKCE(ctx, hardwareID); err != nil {
			if authErr, ok := err.(*ringapimodels.AuthenticationError); ok && authErr.Status == http.StatusNotFound {
				return c.authenticateLegacy(ctx, username, password, hardwareID, otpCode)
			}
			return nil, err
		}
		requires2FA, err := c.submitCredentials(ctx, username, password)
		if err != nil {
			return nil, err
		}
		if requires2FA && otpCode == "" {
			return nil, ringapimodels.NewRequires2FAError("2FA code required")
		}
		if requires2FA {
			if err := c.verify2FA(ctx, otpCode); err != nil {
				return nil, err
			}
		}
	} else {
		if otpCode == "" {
			return nil, ringapimodels.NewRequires2FAError("2FA code required")
		}
		if err := c.verify2FA(ctx, otpCode); err != nil {
			return nil, err
		}
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
	if response.RefreshToken != "" {
		return c.refreshAccessToken(ctx, response.RefreshToken, hardwareID)
	}
	return response, nil
}

// authenticateLegacy preserves compatibility with older Ring deployments and
// with callers that provide an HTTP test double for the historical endpoint.
func (c *Client) authenticateLegacy(ctx context.Context, username, password, hardwareID, otpCode string) (*TokenResponse, error) {
	data := url.Values{
		"grant_type": {"password"}, "username": {username}, "password": {password},
		"client_id": {ringapimodels.RingClientID}, "scope": {ringapimodels.RingScope},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ringapimodels.RingOAuthURI, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, ringapimodels.NewNetworkError("failed to create legacy auth request", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.userAgent)
	if hardwareID != "" {
		req.Header.Set("hardware_id", hardwareID)
	}
	if otpCode != "" {
		req.Header.Set("2fa-support", "true")
		req.Header.Set("2fa-code", otpCode)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, ringapimodels.NewNetworkError("legacy auth request failed", err)
	}
	defer resp.Body.Close()
	return decodeTokenResponse(resp)
}

// Request2FACode starts the PKCE login flow and causes Ring to deliver a code.
func (c *Client) Request2FACode(ctx context.Context, username, password, hardwareID string) error {
	_, err := c.Authenticate(ctx, username, password, hardwareID, "")
	if ringapimodels.IsRequires2FAError(err) {
		return nil
	}
	return err
}

func (c *Client) initiatePKCE(ctx context.Context, hardwareID string) error {
	verifierBytes := make([]byte, 32)
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(verifierBytes); err != nil {
		return ringapimodels.NewInternalServerError("failed to generate PKCE verifier", err)
	}
	if _, err := rand.Read(stateBytes); err != nil {
		return ringapimodels.NewInternalServerError("failed to generate OAuth state", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	challengeBytes := sha256.Sum256([]byte(verifier))
	state := hex.EncodeToString(stateBytes)
	redirectURI := "https://ring.com/signin/callback"

	jar, err := cookiejar.New(nil)
	if err != nil {
		return ringapimodels.NewInternalServerError("failed to create OAuth cookie jar", err)
	}
	authClient := &http.Client{
		Transport: c.httpClient.Transport,
		Timeout:   c.httpClient.Timeout,
		Jar:       jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	params := url.Values{
		"redirect_uri":          {redirectURI},
		"client_id":             {ringapimodels.RingClientID},
		"response_type":         {"code"},
		"prompt":                {"login"},
		"state":                 {state},
		"scope":                 {ringapimodels.RingScope},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challengeBytes[:])},
		"code_challenge_method": {"S256"},
		"device_model":          {"go-ring"},
		"app_version":           {"3.102.0"},
		"dark_mode":             {"false"},
		"device_brand":          {"golang"},
		"device_os_version":     {"go"},
		"app_brand":             {"ring"},
		"hardware_id":           {hardwareID},
	}
	currentURL := ringapimodels.RingOAuthBaseURI + "/oauth/v2/authorize?" + params.Encode()
	var html string
	for range 5 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, currentURL, nil)
		if err != nil {
			return ringapimodels.NewNetworkError("failed to create OAuth authorization request", err)
		}
		req.Header.Set("User-Agent", c.userAgent)
		resp, err := authClient.Do(req)
		if err != nil {
			return ringapimodels.NewNetworkError("failed to initiate OAuth flow", err)
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			location := resp.Header.Get("Location")
			resp.Body.Close()
			if location == "" {
				return ringapimodels.NewAuthenticationError("OAuth redirect missing location", resp.StatusCode)
			}
			next, err := resolveOAuthURL(currentURL, location)
			if err != nil {
				return ringapimodels.NewAuthenticationError("invalid OAuth redirect", resp.StatusCode)
			}
			currentURL = next
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return ringapimodels.NewNetworkError("failed to read OAuth sign-in page", readErr)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return ringapimodels.NewAuthenticationError("failed to load OAuth sign-in page", resp.StatusCode)
		}
		html = string(body)
		break
	}
	csrfToken := extractCSRF(html, jar)
	if csrfToken == "" {
		return ringapimodels.NewAuthenticationError("unable to extract CSRF token from Ring OAuth page", http.StatusUnauthorized)
	}
	c.pendingPKCE = &pkceState{verifier: verifier, state: state, csrfToken: csrfToken, redirectURI: redirectURI, client: authClient}
	return nil
}

func (c *Client) submitCredentials(ctx context.Context, username, password string) (bool, error) {
	pending := c.pendingPKCE
	form := url.Values{"username": {username}, "password": {password}, "csrf-token": {pending.csrfToken}}
	resp, body, err := c.authFormRequest(ctx, pending.client, "/oauth/v2/signin", form)
	if err != nil {
		return false, err
	}
	var payload struct {
		TSVState       string `json:"tsv_state"`
		NextTimeInSecs *int   `json:"next_time_in_secs"`
	}
	_ = json.Unmarshal(body, &payload)
	location := resp.Header.Get("Location")
	requires2FA := resp.StatusCode == http.StatusPreconditionFailed || payload.TSVState != "" || payload.NextTimeInSecs != nil || strings.Contains(location, "/2fa")
	if requires2FA {
		return true, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return false, ringapimodels.NewAuthenticationError("Ring sign-in rejected credentials", resp.StatusCode)
	}
	return false, nil
}

func (c *Client) verify2FA(ctx context.Context, code string) error {
	pending := c.pendingPKCE
	form := url.Values{"2fa_code": {code}, "csrf-token": {pending.csrfToken}, "remember_me": {"false"}}
	resp, body, err := c.authFormRequest(ctx, pending.client, "/oauth/v2/2fa/verify", form)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized {
		return ringapimodels.NewAuthenticationError("verification code is invalid or expired", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return ringapimodels.NewAuthenticationError(fmt.Sprintf("2FA verification failed: %s", strings.TrimSpace(string(body))), resp.StatusCode)
	}
	return nil
}

func (c *Client) authFormRequest(ctx context.Context, client *http.Client, path string, form url.Values) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ringapimodels.RingOAuthBaseURI+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, nil, ringapimodels.NewNetworkError("failed to create OAuth request", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, ringapimodels.NewNetworkError("OAuth request failed", err)
	}
	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		return nil, nil, ringapimodels.NewNetworkError("failed to read OAuth response", readErr)
	}
	return resp, body, nil
}

func (c *Client) authorizationCode(ctx context.Context) (string, error) {
	pending := c.pendingPKCE
	currentURL := ringapimodels.RingOAuthBaseURI + "/oauth/v2/authorize"
	for range 5 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, currentURL, nil)
		if err != nil {
			return "", ringapimodels.NewNetworkError("failed to create OAuth code request", err)
		}
		req.Header.Set("User-Agent", c.userAgent)
		resp, err := pending.client.Do(req)
		if err != nil {
			return "", ringapimodels.NewNetworkError("failed to obtain OAuth authorization code", err)
		}
		location := resp.Header.Get("Location")
		resp.Body.Close()
		if resp.StatusCode < 300 || resp.StatusCode >= 400 || location == "" {
			return "", ringapimodels.NewAuthenticationError("OAuth authorize endpoint did not redirect", resp.StatusCode)
		}
		next, err := resolveOAuthURL(currentURL, location)
		if err != nil {
			return "", ringapimodels.NewAuthenticationError("invalid OAuth authorization redirect", resp.StatusCode)
		}
		redirect, err := url.Parse(next)
		if err == nil && redirect.Query().Get("code") != "" {
			if redirect.Query().Get("state") != pending.state {
				return "", ringapimodels.NewAuthenticationError("OAuth state mismatch", http.StatusUnauthorized)
			}
			return redirect.Query().Get("code"), nil
		}
		currentURL = next
	}
	return "", ringapimodels.NewAuthenticationError("OAuth authorization code redirect limit exceeded", http.StatusUnauthorized)
}

func (c *Client) exchangeAuthorizationCode(ctx context.Context, code, hardwareID string) (*TokenResponse, error) {
	pending := c.pendingPKCE
	form := url.Values{
		"code": {code}, "grant_type": {"authorization_code"}, "redirect_uri": {pending.redirectURI},
		"code_verifier": {pending.verifier}, "client_id": {ringapimodels.RingClientID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ringapimodels.RingOAuthURI, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, ringapimodels.NewNetworkError("failed to create OAuth token request", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("hardware_id", hardwareID)
	resp, err := pending.client.Do(req)
	if err != nil {
		return nil, ringapimodels.NewNetworkError("failed to exchange OAuth authorization code", err)
	}
	defer resp.Body.Close()
	return decodeTokenResponse(resp)
}

// RefreshAccessToken refreshes an access token using a refresh token.
func (c *Client) RefreshAccessToken(ctx context.Context, refreshToken string) (*TokenResponse, error) {
	return c.refreshAccessToken(ctx, refreshToken, c.hardwareID)
}

func (c *Client) refreshAccessToken(ctx context.Context, refreshToken, hardwareID string) (*TokenResponse, error) {
	data := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}, "client_id": {ringapimodels.RingClientID}, "scope": {ringapimodels.RingScope}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ringapimodels.RingOAuthURI, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, ringapimodels.NewNetworkError("failed to create refresh request", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if hardwareID != "" {
		req.Header.Set("hardware_id", hardwareID)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, ringapimodels.NewNetworkError("failed to refresh access token", err)
	}
	defer resp.Body.Close()
	return decodeTokenResponse(resp)
}

func decodeTokenResponse(resp *http.Response) (*TokenResponse, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, ringapimodels.NewNetworkError("failed to read token response", err)
	}
	if resp.StatusCode == http.StatusPreconditionFailed {
		return nil, ringapimodels.NewRequires2FAError("2FA code required")
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, ringapimodels.NewRateLimitError("rate limit exceeded")
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ringapimodels.NewAuthenticationError(string(body), resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, ringapimodels.NewBadRequestError(string(body), errors.New(string(body)))
	}
	var tokenResponse TokenResponse
	if err := json.Unmarshal(body, &tokenResponse); err != nil {
		return nil, ringapimodels.NewInternalServerError("failed to parse token response", err)
	}
	return &tokenResponse, nil
}

func resolveOAuthURL(base, location string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	next, err := url.Parse(location)
	if err != nil {
		return "", err
	}
	return baseURL.ResolveReference(next).String(), nil
}

func extractCSRF(html string, jar http.CookieJar) string {
	for _, rawURL := range []string{ringapimodels.RingOAuthBaseURI, ringapimodels.RingOAuthBaseURI + "/oauth/v2/signin"} {
		parsed, _ := url.Parse(rawURL)
		for _, cookie := range jar.Cookies(parsed) {
			switch strings.ToLower(cookie.Name) {
			case "csrf-token", "csrftoken", "csrf_token", "_csrf", "xsrf-token":
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
		pattern := regexp.MustCompile(`(?s)<script[^>]*id=["']` + regexp.QuoteMeta(scriptID) + `["'][^>]*>(.*?)</script>`)
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
	if depth > 8 {
		return ""
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.NewReplacer("-", "", "_", "").Replace(strings.ToLower(key))
			if normalized == "csrftoken" || normalized == "csrf" {
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
