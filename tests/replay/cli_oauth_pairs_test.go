package replay_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type diagnosticOAuthBindings struct {
	state     string
	challenge string
	hardware  string
}

type diagnosticOAuthRuleError struct {
	field string
	cause error
}

func (err diagnosticOAuthRuleError) Error() string {
	return "invalid synthetic OAuth field: " + err.field
}

func (err diagnosticOAuthRuleError) Unwrap() error { return err.cause }

func newDiagnosticCLIAuthPairs(t *testing.T) *httptest.Server {
	t.Helper()

	var bindings diagnosticOAuthBindings

	restore := func(response *http.Response) {
		location := response.Header.Get("Location")
		if location != "" {
			response.Header.Set("Location", strings.ReplaceAll(location, "$state", bindings.state))
		}
	}

	return newDiagnosticCLIHTTPPairsWithRules(t, "cli-login-refresh.json", bindings.normalize, restore)
}

func (bindings *diagnosticOAuthBindings) normalize(request *http.Request) error {
	query := request.URL.Query()
	if _, exists := query["state"]; exists {
		err := bindings.bindAuthorization(query)
		if err != nil {
			return err
		}

		request.URL.RawQuery = query.Encode()
	}

	if hardware := request.Header.Get("Hardware_id"); hardware != "" {
		if bindings.hardware == "" || hardware != bindings.hardware {
			return diagnosticOAuthRuleError{field: "hardware identity binding", cause: nil}
		}

		request.Header.Set("Hardware_id", "$hardware")
	}

	if request.Method != http.MethodPost {
		return nil
	}

	return bindings.normalizeForm(request)
}

func (bindings *diagnosticOAuthBindings) bindAuthorization(query url.Values) error {
	state, err := hex.DecodeString(query.Get("state"))
	if err != nil || len(state) != 16 || hex.EncodeToString(state) != query.Get("state") || len(query["state"]) != 1 {
		return diagnosticOAuthRuleError{field: "state", cause: nil}
	}

	challenge := query.Get("code_challenge")
	if !canonicalOAuthBytes(challenge) || len(query["code_challenge"]) != 1 {
		return diagnosticOAuthRuleError{field: "challenge", cause: nil}
	}

	hardware := query.Get("hardware_id")

	identity, err := uuid.Parse(hardware)
	if err != nil || identity.String() != hardware || identity.Version() != 4 ||
		identity.Variant() != uuid.RFC4122 || len(query["hardware_id"]) != 1 {
		return diagnosticOAuthRuleError{field: "hardware", cause: nil}
	}

	if bindings.state != "" {
		return diagnosticOAuthRuleError{field: "duplicate authorization binding", cause: nil}
	}

	bindings.state, bindings.challenge, bindings.hardware = query.Get("state"), challenge, hardware
	query.Set("state", "$state")
	query.Set("code_challenge", "$challenge")
	query.Set("hardware_id", "$hardware")

	return nil
}

func canonicalOAuthBytes(value string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(value)

	return err == nil && len(decoded) == 32 && base64.RawURLEncoding.EncodeToString(decoded) == value
}

func (bindings *diagnosticOAuthBindings) normalizeForm(request *http.Request) error {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return diagnosticOAuthRuleError{field: "read form", cause: err}
	}

	if request.Header.Get("Content-Length") != strconv.Itoa(len(body)) {
		return diagnosticOAuthRuleError{field: "content length", cause: nil}
	}

	form, err := url.ParseQuery(string(body))
	if err != nil || form.Encode() != string(body) {
		return diagnosticOAuthRuleError{field: "canonical form encoding", cause: nil}
	}

	if verifier, exists := form["code_verifier"]; exists {
		if len(verifier) != 1 || !canonicalOAuthBytes(verifier[0]) {
			return diagnosticOAuthRuleError{field: "verifier", cause: nil}
		}

		challenge := sha256.Sum256([]byte(verifier[0]))
		if bindings.challenge == "" || base64.RawURLEncoding.EncodeToString(challenge[:]) != bindings.challenge {
			return diagnosticOAuthRuleError{field: "PKCE challenge binding", cause: nil}
		}

		form.Set("code_verifier", "$verifier")
	}

	normalized := form.Encode()
	request.Body = io.NopCloser(strings.NewReader(normalized))
	request.ContentLength = int64(len(normalized))
	request.Header.Set("Content-Length", strconv.Itoa(len(normalized)))

	return nil
}
