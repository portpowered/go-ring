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
	cases, err := replay.LoadCases[authFailureCase](filepath.Join("fixtures", "porting", "auth-stage-failures.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Case, func(t *testing.T) {
			transport := &authStageReplay{stage: tc.Stage, status: tc.Status}
			client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}), ring.WithEndpoints(ring.Endpoints{OAuthBaseURL: "https://oauth.example.test"}), ring.WithHardwareID("fixture-hardware"))
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			var got error
			if tc.Stage == "refresh" {
				_, got = client.RefreshToken(context.Background(), ring.RefreshTokenRequest{RefreshToken: "fixture-refresh"})
			} else {
				_, got = client.Authenticate(context.Background(), ring.AuthenticateRequest{Username: "fixture-user", Password: "fixture-password", OTPCode: "123456"})
			}
			if !transport.failed || got == nil {
				t.Fatalf("failure stage %s not reached: calls=%d error=%v", tc.Stage, transport.calls, got)
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
