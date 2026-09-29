package rest

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/portpowered/go-ring/internal/generatedhttp"
	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/ringerrors"
)

// sendOAuthAuthorize follows only the schema-defined authorization route on
// the configured OAuth origin. Redirects to other paths are never fetched.
func (c *Client) sendOAuthAuthorize(
	ctx context.Context, authClient *http.Client, currentURL string,
) (*http.Response, error) {
	candidate, err := url.Parse(currentURL)
	if err != nil {
		return nil, ringerrors.NewBadRequestError("invalid OAuth authorization URL", err)
	}

	base, err := url.Parse(c.oauthBaseURI)
	if err != nil {
		return nil, ringerrors.NewBadRequestError("invalid OAuth origin", err)
	}

	expectedPath := strings.TrimSuffix(base.EscapedPath(), "/") + protocol.OAuthAuthorizePath
	if candidate.Scheme != base.Scheme || !strings.EqualFold(candidate.Host, base.Host) ||
		candidate.EscapedPath() != expectedPath || candidate.Fragment != "" {
		return nil, ringerrors.NewBadRequestError("OAuth redirect is outside the authorization operation", nil)
	}

	query := candidate.Query()
	for key, values := range query {
		if len(values) != 1 || !oauthAuthorizeParameter(key) {
			return nil, ringerrors.NewBadRequestError("OAuth redirect has an unsupported query parameter", nil)
		}
	}

	value := func(name string) *string {
		values := query[name]
		if len(values) == 0 {
			return nil
		}

		return &values[0]
	}

	var responseType *generatedhttp.BeginOrContinueOAuthAuthorizationParamsResponseType

	if raw := value("response_type"); raw != nil {
		if *raw != "code" {
			return nil, ringerrors.NewBadRequestError("unsupported OAuth response type", nil)
		}

		typed := generatedhttp.BeginOrContinueOAuthAuthorizationParamsResponseType(*raw)
		responseType = &typed
	}

	var challengeMethod *generatedhttp.BeginOrContinueOAuthAuthorizationParamsCodeChallengeMethod

	if raw := value("code_challenge_method"); raw != nil {
		if *raw != "S256" {
			return nil, ringerrors.NewBadRequestError("unsupported OAuth challenge method", nil)
		}

		typed := generatedhttp.BeginOrContinueOAuthAuthorizationParamsCodeChallengeMethod(*raw)
		challengeMethod = &typed
	}

	var darkMode *bool

	if raw := value("dark_mode"); raw != nil {
		parsed, parseErr := strconv.ParseBool(*raw)
		if parseErr != nil {
			return nil, ringerrors.NewBadRequestError("invalid OAuth dark mode", parseErr)
		}

		darkMode = &parsed
	}

	params := &generatedhttp.BeginOrContinueOAuthAuthorizationParams{
		RedirectUri:         value("redirect_uri"),
		ClientId:            value("client_id"),
		ResponseType:        responseType,
		Prompt:              value("prompt"),
		State:               value("state"),
		Scope:               value("scope"),
		CodeChallenge:       value("code_challenge"),
		CodeChallengeMethod: challengeMethod,
		DeviceModel:         value("device_model"),
		AppVersion:          value("app_version"),
		DarkMode:            darkMode,
		DeviceBrand:         value("device_brand"),
		DeviceOsVersion:     value("device_os_version"),
		AppBrand:            value("app_brand"),
		HardwareId:          value("hardware_id"),
	}

	req, err := generatedhttp.NewBeginOrContinueOAuthAuthorizationRequest(generatedServerBase(c.oauthBaseURI), params)
	if err != nil {
		return nil, ringerrors.NewNetworkError("failed to build OAuth authorization request", err)
	}

	req = req.WithContext(ctx)
	req.Header.Set(string(generatedhttp.UserAgent), c.userAgent)

	resp, err := authClient.Do(req)
	if err != nil {
		return nil, ringerrors.NewNetworkError("OAuth authorization request failed", err)
	}

	return resp, nil
}

func oauthAuthorizeParameter(name string) bool {
	switch name {
	case "redirect_uri", "client_id", "response_type", "prompt", "state", "scope", "code_challenge",
		"code_challenge_method", "device_model", "app_version", "dark_mode", "device_brand",
		"device_os_version", "app_brand", "hardware_id":
		return true
	default:
		return false
	}
}
