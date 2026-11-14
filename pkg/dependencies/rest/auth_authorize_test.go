package rest

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

func TestOAuthAuthorizationRejectsUnmodeledRedirectsBeforeNetwork(t *testing.T) {
	t.Parallel()

	for _, redirectURL := range []string{
		"https://oauth.example.test/unmodeled-route",
		"https://elsewhere.example.test/oauth/v2/authorize",
		"https://oauth.example.test/oauth/v2/authorize?unexpected=value",
		"https://oauth.example.test/oauth/v2/authorize?response_type=token",
		"https://oauth.example.test/oauth/v2/authorize?code_challenge_method=plain",
		"https://oauth.example.test/oauth/v2/authorize?dark_mode=invalid",
		"https://oauth.example.test/oauth/v2/authorize?device_model=unsupported",
		"https://oauth.example.test/oauth/v2/authorize?state=first&state=second",
		"https://oauth.example.test/oauth/v2/authorize#fragment",
		"https://oauth.example.test/oauth/v2/signin?unexpected=value",
		"https://oauth.example.test/oauth/v2/signin#fragment",
		"https://oauth.example.test/%invalid",
	} {
		t.Run(redirectURL, func(t *testing.T) {
			t.Parallel()

			client := authTestClient(func(*http.Request) (*http.Response, error) {
				t.Fatal("unmodeled OAuth redirect reached network")

				return nil, restTestError("unexpected network call")
			})

			response, err := client.sendOAuthAuthorize(context.Background(), client.httpClient, redirectURL)
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}

			if !ringapimodels.IsBadRequestError(err) {
				t.Fatalf("sendOAuthAuthorize(%q) error = %v, want BadRequestError", redirectURL, err)
			}
		})
	}
}

const signInPageToken = "sign-in-page-token"

func TestInitiatePKCEFollowsRedirectToSignInPage(t *testing.T) {
	t.Parallel()

	var requests []string

	client := authTestClient(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.Method+" "+req.URL.Path+"?"+req.URL.RawQuery)

		if len(requests) == 1 {
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"/oauth/v2/signin"}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    req,
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/html"}},
			Body: io.NopCloser(strings.NewReader(
				`<script>window.args={"locale":"en-us","csrf-token":"` + signInPageToken + `"}</script>`,
			)),
			Request: req,
		}, nil
	})

	err := client.initiatePKCE(context.Background(), "synthetic-hardware")
	if err != nil {
		t.Fatalf("initiatePKCE() error = %v", err)
	}

	if client.pendingPKCE == nil || client.pendingPKCE.csrfToken != signInPageToken {
		t.Fatalf("initiatePKCE() pending = %#v, want the sign-in page's CSRF token", client.pendingPKCE)
	}

	if len(requests) != 2 || !strings.HasPrefix(requests[0], "GET /oauth/v2/authorize?") ||
		requests[1] != "GET /oauth/v2/signin?" {
		t.Fatalf("requests = %q, want the authorization then the bare sign-in page", requests)
	}
}
