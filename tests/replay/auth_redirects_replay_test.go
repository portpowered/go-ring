package replay_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

type oauthRedirectCase struct {
	Case     string `json:"case"`
	Stage    string `json:"stage"`
	Location string `json:"location"`
}

type oauthRedirectTransport struct {
	variant        oauthRedirectCase
	authorizations int
}

func (tr *oauthRedirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	status, body := http.StatusOK, `{}`
	header := make(http.Header)
	switch req.URL.Path {
	case "/oauth/v2/authorize":
		tr.authorizations++
		if tr.variant.Stage == "bootstrap" || tr.authorizations > 1 {
			status = http.StatusFound
			header.Set("Location", tr.variant.Location)
		} else {
			body = `<script id="oauth-args">{"csrf-token":"fixture-csrf"}</script>`
		}
	case "/oauth/v2/signin":
		status = http.StatusPreconditionFailed
		body = `{"tsv_state":"email"}`
	case "/oauth/v2/2fa/verify":
		// The portable login capture completes verification before requesting
		// its authorization-code redirect.
	default:
		return nil, errors.New("unexpected route in OAuth redirect fixture")
	}
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

func TestPortableOAuthRedirectFailureVariants(t *testing.T) {
	cases, err := replay.LoadCases[oauthRedirectCase](filepath.Join("fixtures", "auth", "synthetic", "oauth-redirect-variants.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Case, func(t *testing.T) {
			transport := &oauthRedirectTransport{variant: tc}
			client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}), ring.WithEndpoints(ring.Endpoints{OAuthBaseURL: "https://oauth.example.test"}))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = client.Close() }()
			_, got := client.Authenticate(context.Background(), ring.AuthenticateRequest{HardwareID: "fixture-hardware", Username: "fixture-user", Password: "fixture-password", OTPCode: "123456"})
			if strings.Contains(tc.Case, "malformed-location") {
				if !ringapimodels.IsNetworkError(got) {
					t.Fatalf("invalid HTTP Location error = %v", got)
				}
			} else if !ringapimodels.IsAuthenticationError(got) {
				t.Fatalf("redirect error = %v", got)
			}
			if tc.Stage == "authorization" && transport.authorizations < 2 {
				t.Fatalf("authorization-code request missing")
			}
		})
	}
}
