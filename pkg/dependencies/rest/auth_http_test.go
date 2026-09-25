package rest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

func TestExtractCSRFHTMLFormsAndNestedState(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		html string
		want string
	}{
		{"name before value", `<input name="csrf-token" value="a">`, "a"},
		{"value before name", `<input value="b" name="csrf-token">`, "b"},
		{"underscore spelling", `<input name="csrf_token" value="c">`, "c"},
		{"meta content", `<meta name="csrfToken" content="d">`, "d"},
		{"normalized nested script key", `<script id="oauth-args">{"CSRF_TOKEN":"e"}</script>`, "e"},
		{"nested array", `<script id="__NEXT_DATA__">{"props":[{"auth":{"CSRF_TOKEN":"f"}}]}</script>`, "f"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractCSRF(tt.html, jar, "https://oauth.example.test"); got != tt.want {
				t.Fatalf("extractCSRF() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractCSRFCookiePrecedesPageAndFindCSRFStopsAtDepthLimit(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	oauthURL, _ := url.Parse("https://oauth.example.test/oauth/v2/signin")
	jar.SetCookies(oauthURL, []*http.Cookie{{Name: "XSRF-TOKEN", Value: "cookie-token"}})
	if got := extractCSRF(`<input name="csrf-token" value="page-token">`, jar, "https://oauth.example.test"); got != "cookie-token" {
		t.Fatalf("cookie CSRF token = %q", got)
	}
	deep := map[string]any{"csrf_token": "too-deep"}
	for range 9 {
		deep = map[string]any{"child": deep}
	}
	if got := findCSRF(deep, 0); got != "" {
		t.Fatalf("findCSRF() beyond depth limit = %q, want empty", got)
	}
}

func TestResolveOAuthURL(t *testing.T) {
	got, err := resolveOAuthURL("https://oauth.example.test/oauth/v2/authorize", "../signin/callback?code=value")
	if err != nil || got != "https://oauth.example.test/oauth/signin/callback?code=value" {
		t.Fatalf("resolveOAuthURL() = %q, %v", got, err)
	}
	if _, err := resolveOAuthURL(":", "/callback"); err == nil {
		t.Fatal("resolveOAuthURL() accepted an invalid base URL")
	}
}

func TestDecodeTokenResponseStatusAndBodyFailures(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       io.ReadCloser
		checkError func(error) bool
	}{
		{"two factor", http.StatusPreconditionFailed, io.NopCloser(strings.NewReader("{}")), ringapimodels.IsRequires2FAError},
		{"rate limit", http.StatusTooManyRequests, io.NopCloser(strings.NewReader("{}")), ringapimodels.IsRateLimitError},
		{"unauthorized", http.StatusUnauthorized, io.NopCloser(strings.NewReader("expired")), ringapimodels.IsAuthenticationError},
		{"other client error", http.StatusBadRequest, io.NopCloser(strings.NewReader("bad request")), ringapimodels.IsBadRequestError},
		{"malformed success", http.StatusOK, io.NopCloser(strings.NewReader("{")), ringapimodels.IsInternalServerError},
		{"read failure", http.StatusOK, &trackedBody{readErr: errors.New("read failed")}, ringapimodels.IsNetworkError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: tt.status, Status: http.StatusText(tt.status), Body: tt.body}
			got, err := decodeTokenResponse(resp)
			if got != nil || !tt.checkError(err) {
				t.Fatalf("decodeTokenResponse() = %#v, %v", got, err)
			}
		})
	}
	resp := &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader(`{"access_token":"a","refresh_token":"r","expires_in":30,"token_type":"Bearer"}`))}
	got, err := decodeTokenResponse(resp)
	if err != nil || got.AccessToken != "a" || got.RefreshToken != "r" || got.ExpiresIn != 30 {
		t.Fatalf("decodeTokenResponse() success = %#v, %v", got, err)
	}
}

func TestRefreshAccessTokenUsesConfiguredOAuthEndpointAndRotatesResponse(t *testing.T) {
	calls := 0
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.String() != "https://oauth.example.test/oauth/token" {
			t.Errorf("refresh URL = %s", req.URL)
		}
		if req.Method != http.MethodPost || req.Header.Get("Content-Type") != "application/x-www-form-urlencoded" ||
			req.Header.Get("hardware_id") != "synthetic-hardware" || req.Header.Get("User-Agent") != "auth-test/1" {
			t.Errorf("unexpected refresh request headers/method: %s %#v", req.Method, req.Header)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		form, err := url.ParseQuery(string(body))
		if err != nil || form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "old-refresh" {
			t.Errorf("refresh form = %q, parse error = %v", string(body), err)
		}
		return testResponse(req, http.StatusOK, io.NopCloser(strings.NewReader(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":1800,"token_type":"Bearer"}`))), nil
	})
	client := NewClient(WithHTTPClient(&http.Client{Transport: transport}), WithEndpointBases("https://api.example.test", "https://oauth.example.test"), WithHardwareID("synthetic-hardware"), WithUserAgent("auth-test/1"))
	response, err := client.RefreshAccessToken(context.Background(), "old-refresh")
	if err != nil {
		t.Fatalf("RefreshAccessToken() error = %v", err)
	}
	if calls != 1 || response.AccessToken != "new-access" || response.RefreshToken != "new-refresh" || response.ExpiresIn != 1800 {
		t.Fatalf("refresh response = %#v, calls = %d", response, calls)
	}
}
