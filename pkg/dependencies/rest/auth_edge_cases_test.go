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

func authTestClient(transport roundTripFunc) *Client {
	return NewClient(
		WithHTTPClient(&http.Client{Transport: transport}),
		WithEndpointBases("https://api.example.test", "https://oauth.example.test"),
	)
}

func TestExtractCSRFMalformedAndEmptyProvidersFallThrough(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		html string
		want string
	}{
		{
			name: "malformed first script then valid next data",
			html: `<script id="oauth-args">{not json}</script><script id="__NEXT_DATA__">{"props":{"csrfToken":"next-token"}}</script>`,
			want: "next-token",
		},
		{
			name: "empty JSON token falls through to markup",
			html: `<script id="oauth-args">{"csrfToken":""}</script><input name="csrf-token" value="markup-token">`,
			want: "markup-token",
		},
		{name: "no supported provider", html: `<input name="unknown" value="irrelevant">`, want: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractCSRF(tt.html, jar, "https://oauth.example.test"); got != tt.want {
				t.Fatalf("extractCSRF() = %q, want %q", got, tt.want)
			}
		})
	}

	boundary := any(map[string]any{"csrf-token": "depth-eight"})
	for range 8 {
		boundary = map[string]any{"child": boundary}
	}
	if got := findCSRF(boundary, 0); got != "depth-eight" {
		t.Fatalf("findCSRF() at supported depth = %q, want token", got)
	}
	if got := findCSRF(nil, 0); got != "" {
		t.Fatalf("findCSRF(nil) = %q, want empty", got)
	}
}

func TestInitiatePKCERedirectAndProviderFailures(t *testing.T) {
	tests := []struct {
		name      string
		responses []*http.Response
		wantCalls int
	}{
		{
			name: "absolute redirect to csrf page",
			responses: []*http.Response{
				{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://oauth.example.test/signin"}}, Body: io.NopCloser(strings.NewReader(""))},
				{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`<meta name="csrf-token" content="page-token">`))},
			},
			wantCalls: 2,
		},
		{
			name:      "redirect without location",
			responses: []*http.Response{{StatusCode: http.StatusFound, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}},
			wantCalls: 1,
		},
		{
			name:      "non-success page response",
			responses: []*http.Response{{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("unavailable"))}},
			wantCalls: 1,
		},
		{
			name:      "successful page without csrf provider",
			responses: []*http.Response{{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("<html>login</html>"))}},
			wantCalls: 1,
		},
		{
			name:      "invalid absolute redirect",
			responses: []*http.Response{{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"http://[::1"}}, Body: io.NopCloser(strings.NewReader(""))}},
			wantCalls: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			client := authTestClient(func(req *http.Request) (*http.Response, error) {
				if req.URL.Host != "oauth.example.test" {
					t.Errorf("redirect escaped configured OAuth origin: %q", req.URL)
				}
				calls++
				if calls > len(tt.responses) {
					return nil, errors.New("unexpected extra authorization request")
				}
				resp := tt.responses[calls-1]
				resp.Request = req
				return resp, nil
			})
			err := client.initiatePKCE(context.Background(), "synthetic-hardware")
			if calls != tt.wantCalls {
				t.Fatalf("authorization requests = %d, want %d", calls, tt.wantCalls)
			}
			if tt.name == "absolute redirect to csrf page" {
				if err != nil || client.pendingPKCE == nil || client.pendingPKCE.csrfToken != "page-token" {
					t.Fatalf("initiatePKCE() pending = %#v, error = %v", client.pendingPKCE, err)
				}
			} else if tt.name == "invalid absolute redirect" {
				if !ringapimodels.IsNetworkError(err) {
					t.Fatalf("initiatePKCE() error = %v, want NetworkError for malformed Location", err)
				}
			} else if !ringapimodels.IsAuthenticationError(err) {
				t.Fatalf("initiatePKCE() error = %v, want AuthenticationError", err)
			}
		})
	}
}

func TestAuthorizationCodeRequiresValidRedirectAndState(t *testing.T) {
	for _, tt := range []struct {
		name      string
		responses []*http.Response
		want      string
	}{
		{
			name: "relative callback with code and state",
			responses: []*http.Response{{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"../callback?code=synthetic-code&state=expected-state"}},
				Body:       io.NopCloser(strings.NewReader("")),
			}},
			want: "synthetic-code",
		},
		{
			name: "absolute callback with code and state",
			responses: []*http.Response{{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"https://oauth.example.test/callback?code=synthetic-code&state=expected-state"}},
				Body:       io.NopCloser(strings.NewReader("")),
			}},
			want: "synthetic-code",
		},
		{
			name: "non-redirect response",
			responses: []*http.Response{{
				StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")),
			}},
		},
		{
			name: "redirect without location",
			responses: []*http.Response{{
				StatusCode: http.StatusFound, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")),
			}},
		},
		{
			name: "state mismatch",
			responses: []*http.Response{{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"/callback?code=synthetic-code&state=other"}},
				Body:       io.NopCloser(strings.NewReader("")),
			}},
		},
		{
			name: "no code after bounded redirects",
			responses: []*http.Response{
				{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"/step/1"}}, Body: io.NopCloser(strings.NewReader(""))},
				{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"/step/2"}}, Body: io.NopCloser(strings.NewReader(""))},
				{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"/step/3"}}, Body: io.NopCloser(strings.NewReader(""))},
				{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"/step/4"}}, Body: io.NopCloser(strings.NewReader(""))},
				{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"/step/5"}}, Body: io.NopCloser(strings.NewReader(""))},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			client := authTestClient(func(req *http.Request) (*http.Response, error) {
				if calls >= len(tt.responses) {
					t.Fatalf("unexpected extra authorization request to %s", req.URL)
				}
				resp := tt.responses[calls]
				calls++
				resp.Request = req
				return resp, nil
			})
			client.pendingPKCE = &pkceState{
				state: "expected-state",
				client: &http.Client{
					Transport:     client.httpClient.Transport,
					CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
				},
			}
			code, err := client.authorizationCode(context.Background())
			if tt.want != "" {
				if err != nil || code != tt.want {
					t.Fatalf("authorizationCode() = %q, %v; want code", code, err)
				}
			} else if code != "" || !ringapimodels.IsAuthenticationError(err) {
				t.Fatalf("authorizationCode() = %q, %v; want auth error", code, err)
			}
			if calls != len(tt.responses) {
				t.Fatalf("authorization requests = %d, want %d", calls, len(tt.responses))
			}
		})
	}
}

func TestAuthFormRequestContextAndResponseReadFailures(t *testing.T) {
	t.Run("canceled context", func(t *testing.T) {
		client := authTestClient(func(req *http.Request) (*http.Response, error) {
			if err := req.Context().Err(); err != nil {
				return nil, err
			}
			t.Fatal("transport should see the canceled request context")
			return nil, errors.New("unreachable")
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := client.authFormRequest(ctx, client.httpClient, "/oauth/v2/signin", url.Values{"username": {"synthetic-user"}})
		if !ringapimodels.IsNetworkError(err) || !errors.Is(err, context.Canceled) {
			t.Fatalf("authFormRequest() error = %v, want wrapped context cancellation", err)
		}
	})

	t.Run("response body read failure closes body", func(t *testing.T) {
		body := &trackedBody{readErr: errors.New("synthetic reader failure")}
		client := authTestClient(func(req *http.Request) (*http.Response, error) {
			return testResponse(req, http.StatusOK, body), nil
		})
		_, _, err := client.authFormRequest(context.Background(), client.httpClient, "/oauth/v2/signin", url.Values{"username": {"synthetic-user"}})
		if !ringapimodels.IsNetworkError(err) || !errors.Is(err, body.readErr) || !body.closed {
			t.Fatalf("authFormRequest() error = %v, body closed = %v", err, body.closed)
		}
	})

	t.Run("encodes form and closes successful response", func(t *testing.T) {
		body := &trackedBody{reader: strings.NewReader(`{"accepted":true}`)}
		client := authTestClient(func(req *http.Request) (*http.Response, error) {
			encoded, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatal(err)
			}
			if req.Method != http.MethodPost || req.URL.String() != "https://oauth.example.test/oauth/v2/signin" ||
				req.Header.Get("Content-Type") != "application/x-www-form-urlencoded" ||
				!strings.Contains(string(encoded), "username=synthetic+user") {
				t.Errorf("OAuth form request had unexpected method, URL, headers, or form shape")
			}
			return testResponse(req, http.StatusOK, body), nil
		})
		resp, result, err := client.authFormRequest(context.Background(), client.httpClient, "/oauth/v2/signin", url.Values{"username": {"synthetic user"}})
		if err != nil || resp.StatusCode != http.StatusOK || string(result) != `{"accepted":true}` || !body.closed {
			t.Fatalf("authFormRequest() = %#v, %q, %v; body closed = %v", resp, result, err, body.closed)
		}
	})
}
