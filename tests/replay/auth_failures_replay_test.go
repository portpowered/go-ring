package replay_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

type authFailureCase struct {
	Case   string `json:"case"`
	Stage  string `json:"stage"`
	Status int    `json:"status"`
	Error  string `json:"error"`
}

type authCSRFCase struct {
	Case     string `json:"case"`
	HTML     string `json:"html"`
	Expected string `json:"expected"`
}

type csrfPageReplay struct {
	page   string
	token  string
	signin bool
}

func (p *csrfPageReplay) RoundTrip(request *http.Request) (*http.Response, error) {
	status, body := http.StatusOK, p.page
	if request.URL.Path == "/oauth/v2/signin" {
		p.signin = true
		form, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		values, err := url.ParseQuery(string(form))
		if err != nil || values.Get("csrf-token") != p.token {
			return nil, errors.New("CSRF token differs from portable page fixture")
		}
		status, body = http.StatusUnauthorized, `{}`
	}
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func TestPortableOAuthCSRFPages(t *testing.T) {
	cases, err := replay.LoadCases[authCSRFCase](filepath.Join("fixtures", "auth", "synthetic", "auth-csrf-variants.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Case, func(t *testing.T) {
			transport := &csrfPageReplay{page: tc.HTML, token: tc.Expected}
			client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}), ring.WithEndpoints(ring.Endpoints{OAuthBaseURL: "https://oauth.example.test"}))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = client.Close() }()
			_, err = client.Authenticate(context.Background(), ring.AuthenticateRequest{HardwareID: "fixture-hardware", Username: "fixture-user", Password: "fixture-password"})
			if !ringapimodels.IsAuthenticationError(err) || transport.signin != (tc.Expected != "") {
				t.Fatalf("CSRF page behavior: signin=%t error=%v", transport.signin, err)
			}
		})
	}
}

type authStageReplay struct {
	stage      string
	status     int
	state      string
	authorized bool
	failed     bool
	calls      int
}

func (a *authStageReplay) RoundTrip(request *http.Request) (*http.Response, error) {
	a.calls++
	status, body := http.StatusOK, `{}`
	header := make(http.Header)
	stage := ""
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/oauth/v2/authorize" && !a.authorized:
		a.state = request.URL.Query().Get("state")
		a.authorized = true
		body = `<script id="oauth-args">{"csrf-token":"fixture-csrf"}</script>`
	case strings.HasSuffix(request.URL.Path, "/signin"):
		stage, status, body = "signin", http.StatusPreconditionFailed, `{"tsv_state":"email"}`
	case strings.HasSuffix(request.URL.Path, "/2fa/verify"):
		stage = "verify"
	case request.Method == http.MethodGet && request.URL.Path == "/oauth/v2/authorize":
		stage, status = "authorize", http.StatusFound
		header.Set("Location", "https://oauth.example.test/callback?code=fixture-code&state="+url.QueryEscape(a.state))
	case strings.HasSuffix(request.URL.Path, "/token"):
		formBody, _ := io.ReadAll(request.Body)
		if strings.Contains(string(formBody), "grant_type=refresh_token") {
			stage = "refresh"
		} else {
			stage = "exchange"
		}
		body = `{"access_token":"fixture-access","refresh_token":"fixture-refresh","expires_in":3600,"token_type":"Bearer"}`
	default:
		return nil, errors.New("unexpected OAuth route in replay")
	}
	if stage == a.stage {
		a.failed = true
		if a.status == 0 {
			return nil, errors.New("fixture transport failure")
		}
		status, header = a.status, make(http.Header)
		body = `{}`
		if a.stage == "refresh" && status == http.StatusOK {
			body = `{`
		}
	}
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func TestPortableOAuthFailureStages(t *testing.T) {
	cases, err := replay.LoadCases[authFailureCase](filepath.Join("fixtures", "auth", "synthetic", "auth-stage-failures.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Case, func(t *testing.T) {
			transport := &authStageReplay{stage: tc.Stage, status: tc.Status}
			client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}), ring.WithEndpoints(ring.Endpoints{OAuthBaseURL: "https://oauth.example.test"}))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = client.Close() }()
			var got error
			if tc.Stage == "refresh" {
				_, got = client.RefreshToken(context.Background(), ring.RefreshTokenRequest{HardwareID: "fixture-hardware", RefreshToken: "fixture-refresh"})
			} else {
				_, got = client.Authenticate(context.Background(), ring.AuthenticateRequest{HardwareID: "fixture-hardware", Username: "fixture-user", Password: "fixture-password", OTPCode: "123456"})
			}
			if !transport.failed || got == nil {
				t.Fatalf("failure stage %s not reached: calls=%d error=%v", tc.Stage, transport.calls, got)
			}
			if message := got.Error(); message == "" || strings.Contains(message, "fixture-password") || strings.Contains(message, "fixture-refresh") {
				t.Fatalf("unsafe OAuth error message: %q", message)
			}
			switch tc.Error {
			case "network":
				if !ringapimodels.IsNetworkError(got) {
					t.Fatalf("expected network error, got %v", got)
				}
			case "authentication":
				if !ringapimodels.IsAuthenticationError(got) {
					t.Fatalf("expected authentication error, got %v", got)
				}
			case "rate-limit":
				if !ringapimodels.IsRateLimitError(got) {
					t.Fatalf("expected rate limit, got %v", got)
				}
			case "server":
				if !ringapimodels.IsInternalServerError(got) {
					t.Fatalf("expected response decode error, got %v", got)
				}
			}
		})
	}
}
