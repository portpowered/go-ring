package replay_test

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func syntheticOAuthAuthorization() (url.Values, string) {
	verifier := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	challenge := sha256.Sum256([]byte(verifier))

	return url.Values{
		"state":          {"0123456789abcdef0123456789abcdef"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"hardware_id":    {"12345678-1234-4234-8234-123456789abc"},
	}, verifier
}

func TestDiagnosticOAuthRulesRejectMalformedBindings(t *testing.T) {
	t.Parallel()

	for _, field := range []string{"state", "code_challenge", "hardware_id"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()

			for _, value := range []string{"", "malformed"} {
				query, _ := syntheticOAuthAuthorization()
				query.Set(field, value)

				var bindings diagnosticOAuthBindings

				err := bindings.bindAuthorization(query)
				if err == nil {
					t.Fatalf("accepted malformed %s", field)
				}
			}

			query, _ := syntheticOAuthAuthorization()
			query.Add(field, query.Get(field))

			var bindings diagnosticOAuthBindings

			err := bindings.bindAuthorization(query)
			if err == nil {
				t.Fatalf("accepted repeated %s", field)
			}
		})
	}
}

func TestDiagnosticOAuthRulesRejectChangedPKCEAndHardware(t *testing.T) {
	t.Parallel()

	for _, scenario := range []string{
		"verifier format", "challenge binding", "hardware binding", "length", "duplicate binding",
	} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()

			query, verifier := syntheticOAuthAuthorization()

			var bindings diagnosticOAuthBindings

			err := bindings.bindAuthorization(query)
			if err != nil {
				t.Fatal(err)
			}

			body := url.Values{"code_verifier": {verifier}}.Encode()
			if scenario == "verifier format" {
				body = "code_verifier=invalid"
			}

			if scenario == "challenge binding" {
				bindings.challenge = "different"
			}

			request, err := http.NewRequestWithContext(
				t.Context(), http.MethodPost, diagnosticReplayOrigin+"/oauth/token", strings.NewReader(body),
			)
			if err != nil {
				t.Fatal(err)
			}

			request.Header.Set("Content-Length", strconv.Itoa(len(body)))

			if scenario == "hardware binding" {
				request.Header.Set("Hardware_id", "12345678-1234-4234-8234-123456789abd")
			}

			if scenario == "length" {
				request.Header.Set("Content-Length", "0")
			}

			if scenario == "duplicate binding" {
				fresh, _ := syntheticOAuthAuthorization()
				request.URL.RawQuery = fresh.Encode()
			}

			err = bindings.normalize(request)
			if err == nil {
				t.Fatalf("accepted invalid %s", scenario)
			}
		})
	}
}

func TestDiagnosticOAuthRulesAcceptBoundVerifier(t *testing.T) {
	t.Parallel()

	query, verifier := syntheticOAuthAuthorization()

	var bindings diagnosticOAuthBindings

	err := bindings.bindAuthorization(query)
	if err != nil {
		t.Fatal(err)
	}

	body := url.Values{"code_verifier": {verifier}}.Encode()

	request, err := http.NewRequestWithContext(
		t.Context(), http.MethodPost, diagnosticReplayOrigin+"/oauth/token", strings.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}

	request.Header.Set("Content-Length", strconv.Itoa(len(body)))
	request.Header.Set("Hardware_id", bindings.hardware)

	err = bindings.normalize(request)
	if err != nil {
		t.Fatal(err)
	}

	if request.Header.Get("Hardware_id") != "$hardware" || query.Get("state") != "$state" {
		t.Fatal("validated volatile fields were not bound to fixture placeholders")
	}
}
