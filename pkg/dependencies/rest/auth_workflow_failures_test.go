package rest

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

const oauthExchangeStage = "exchange"

// These are synthetic failure variants of the existing PKCE workflow, not
// evidence that OAuth appeared in the device recording.
func TestAuthenticateStopsAtFailedStage(t *testing.T) {
	t.Parallel()

	for _, stage := range []string{"signin", "verify", "authorize", oauthExchangeStage} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()

			var client *Client

			calls := 0
			failed := false
			client = authTestClient(func(req *http.Request) (*http.Response, error) {
				calls++

				if failed {
					t.Error("authentication continued after failure")
				}

				status, body := http.StatusOK, `{}`
				header := make(http.Header)

				current := ""

				switch {
				case strings.HasSuffix(req.URL.Path, "/signin"):
					current = "signin"
					status = http.StatusPreconditionFailed
				case strings.HasSuffix(req.URL.Path, "/2fa/verify"):
					current = "verify"
				case strings.HasSuffix(req.URL.Path, "/token"):
					current = oauthExchangeStage
				case client.pendingPKCE == nil:
					body = `<input name="csrf-token" value="synthetic">`
				default:
					current = "authorize"
					status = http.StatusFound

					header.Set(
						"Location",
						"https://oauth.example.test/callback?code=synthetic&state="+client.pendingPKCE.state,
					)
				}

				if current == stage {
					failed = true

					if stage == oauthExchangeStage {
						return nil, io.ErrUnexpectedEOF
					}

					status = http.StatusUnauthorized
					header = make(http.Header)
				}

				return &http.Response{
					StatusCode: status,
					Header:     header,
					Body:       io.NopCloser(strings.NewReader(body)),
					Request:    req,
				}, nil
			})

			token, err := client.Authenticate(context.Background(), "user", "password", "hardware", "123456")
			if token != nil || err == nil || !failed {
				t.Fatalf("token=%v error=%v failed=%v calls=%d", token, err, failed, calls)
			}

			if stage == oauthExchangeStage && !ringapimodels.IsNetworkError(err) {
				t.Fatalf("want network error, got %v", err)
			}

			if stage != oauthExchangeStage && !ringapimodels.IsAuthenticationError(err) {
				t.Fatalf("want authentication error, got %v", err)
			}
		})
	}
}

func TestPendingAuthenticationRequiresCodeWithoutHTTP(t *testing.T) {
	t.Parallel()

	client := authTestClient(func(*http.Request) (*http.Response, error) {
		t.Fatal("must not send without verification code")

		return nil, context.Canceled
	})
	client.pendingPKCE = &pkceState{client: client.httpClient}

	token, err := client.Authenticate(context.Background(), "", "", "", "")
	if token != nil || !ringapimodels.IsRequires2FAError(err) {
		t.Fatalf("token=%v error=%v", token, err)
	}
}

func TestRequestCodePropagatesProviderFailure(t *testing.T) {
	t.Parallel()

	client := authTestClient(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("unavailable")),
			Request:    req,
		}, nil
	})

	err := client.Request2FACode(context.Background(), "user", "password", "hardware")
	if !ringapimodels.IsAuthenticationError(err) {
		t.Fatalf("provider failure lost: %v", err)
	}
}
