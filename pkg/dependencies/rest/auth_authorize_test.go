package rest

import (
	"context"
	"net/http"
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
