package rest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type trackedBody struct {
	reader  io.Reader
	closed  bool
	readErr error
}

func (b *trackedBody) Read(p []byte) (int, error) {
	if b.readErr != nil {
		return 0, b.readErr
	}
	return b.reader.Read(p)
}
func (b *trackedBody) Close() error { b.closed = true; return nil }

func testResponse(req *http.Request, status int, body io.ReadCloser) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     make(http.Header),
		Body:       body,
		Request:    req,
	}
}

func TestDoRequestRetryRewindsJSONBodyAndClosesDiscardedResponse(t *testing.T) {
	var calls int
	var bodies []string
	var firstResponse *trackedBody
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		var body []byte
		var err error
		if req.Body != nil {
			body, err = io.ReadAll(req.Body)
			_ = req.Body.Close()
		}
		if err != nil {
			return nil, err
		}
		bodies = append(bodies, string(body))
		if req.URL.String() != "https://api.example.test/v3/devices" || req.Method != http.MethodGet {
			t.Errorf("unexpected request target: %s %s", req.Method, req.URL)
		}
		if calls == 1 {
			firstResponse = &trackedBody{reader: strings.NewReader("busy")}
			return testResponse(req, http.StatusServiceUnavailable, firstResponse), nil
		}
		return testResponse(req, http.StatusNoContent, io.NopCloser(strings.NewReader(""))), nil
	})
	httpClient := &http.Client{Transport: transport}
	tokenCalls := 0
	client := NewClient(
		WithHTTPClient(httpClient),
		WithEndpointBases("https://api.example.test", "https://oauth.example.test"),
		WithUserAgent("coverage-client/1"),
		WithHardwareID("test-hardware"),
		WithTokenGetter(func(ctx context.Context) (string, error) {
			tokenCalls++
			return "test-access-token", nil
		}),
	)
	resp, err := client.doRequest(context.Background(), http.MethodGet, "/v3/devices", map[string]any{"enabled": false, "name": "lamp"})
	if err != nil {
		t.Fatalf("doRequest() error = %v", err)
	}
	_ = resp.Body.Close()
	if calls != 2 || tokenCalls != 1 {
		t.Fatalf("RoundTrip calls = %d, token getter calls = %d; want 2 and 1", calls, tokenCalls)
	}
	if len(bodies) != 2 || bodies[0] != bodies[1] || bodies[0] != `{"enabled":false,"name":"lamp"}` {
		t.Fatalf("retry request bodies = %#v", bodies)
	}
	if firstResponse == nil || !firstResponse.closed {
		t.Fatal("discarded 503 response body was not closed before retry")
	}
	if client.HTTPClient() != httpClient {
		t.Fatal("custom HTTP client was not retained")
	}
}

func TestDoRequestDoesNotRetryMutation(t *testing.T) {
	t.Run("server error", func(t *testing.T) {
		calls := 0
		client := NewClient(WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			return testResponse(req, http.StatusInternalServerError, io.NopCloser(strings.NewReader("busy"))), nil
		})}))
		resp, err := client.doRequest(context.Background(), http.MethodPatch, "/device", map[string]bool{"enabled": false})
		if err != nil {
			t.Fatalf("doRequest() error = %v", err)
		}
		_ = resp.Body.Close()
		if calls != 1 {
			t.Fatalf("PATCH attempts = %d, want 1", calls)
		}
	})

	t.Run("transport failure", func(t *testing.T) {
		calls := 0
		transportErr := errors.New("connection lost")
		client := NewClient(WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, transportErr
		})}))
		_, err := client.doRequest(context.Background(), http.MethodPost, "/command", map[string]string{"command": "reboot"})
		if !ringapimodels.IsNetworkError(err) || !errors.Is(err, transportErr) || calls != 1 {
			t.Fatalf("POST error = %v, attempts = %d; want wrapped transport error and one attempt", err, calls)
		}
	})
}

func TestDoRequestCancellationDuringRetryBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	discarded := &trackedBody{reader: strings.NewReader("busy")}
	client := NewClient(WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		cancel()
		return testResponse(req, http.StatusInternalServerError, discarded), nil
	})}))
	_, err := client.doRequest(ctx, http.MethodGet, "/retry", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("doRequest() error = %v, want context cancellation", err)
	}
	if calls != 1 || !discarded.closed {
		t.Fatalf("RoundTrip calls = %d, response closed = %v", calls, discarded.closed)
	}
}

func TestConfiguredTokenGetterErrorStopsBeforeSendingRequest(t *testing.T) {
	getterErr := errors.New("token source unavailable")
	calls := 0
	client := NewClient(
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			return testResponse(req, http.StatusOK, io.NopCloser(strings.NewReader("{}"))), nil
		})}),
		WithTokenGetter(func(context.Context) (string, error) { return "", getterErr }),
	)
	_, err := client.doRequest(context.Background(), http.MethodGet, "/requires-auth", nil)
	if !ringapimodels.IsTokenError(err) || !errors.Is(err, getterErr) || calls != 0 {
		t.Fatalf("doRequest() error = %v, transport calls = %d", err, calls)
	}
}

func TestWithBaseURIAndDirectTokenConfigureRequest(t *testing.T) {
	var observed *http.Request
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		observed = req.Clone(req.Context())
		return testResponse(req, http.StatusOK, io.NopCloser(strings.NewReader("{}"))), nil
	})}
	client := NewClient(WithHTTPClient(httpClient), WithBaseURI("https://region.example.test/api"), WithAccessToken("direct-token"))
	resp, err := client.doRequest(context.Background(), http.MethodGet, "/devices", nil)
	if err != nil {
		t.Fatalf("doRequest() error = %v", err)
	}
	_ = resp.Body.Close()
	if observed == nil || observed.URL.String() != "https://region.example.test/api/devices" || observed.Header.Get("Authorization") != "Bearer direct-token" {
		t.Fatalf("observed request = %#v", observed)
	}
}

func TestDoJSONRequestDecodesAndPreservesHTTPFailure(t *testing.T) {
	t.Run("decodes success", func(t *testing.T) {
		client := NewClient(WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return testResponse(req, http.StatusOK, io.NopCloser(strings.NewReader(`{"count":3,"items":["a","b"]}`))), nil
		})}))
		var result struct {
			Count int      `json:"count"`
			Items []string `json:"items"`
		}
		if err := client.doJSONRequest(context.Background(), http.MethodGet, "/shape", nil, &result); err != nil {
			t.Fatalf("doJSONRequest() error = %v", err)
		}
		if result.Count != 3 || len(result.Items) != 2 {
			t.Fatalf("decoded result = %#v", result)
		}
	})

	t.Run("keeps status and body for caller inspection", func(t *testing.T) {
		body := `{"error":"not allowed"}`
		client := NewClient(WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return testResponse(req, http.StatusForbidden, io.NopCloser(strings.NewReader(body))), nil
		})}))
		err := client.doJSONRequest(context.Background(), http.MethodGet, "/private", nil, nil)
		var httpErr *ringapimodels.HTTPError
		if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusForbidden || httpErr.Body != body {
			t.Fatalf("doJSONRequest() error = %#v", err)
		}
	})

	t.Run("reports malformed JSON", func(t *testing.T) {
		client := NewClient(WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return testResponse(req, http.StatusOK, io.NopCloser(strings.NewReader("{"))), nil
		})}))
		var result map[string]any
		err := client.doJSONRequest(context.Background(), http.MethodGet, "/malformed", nil, &result)
		if !ringapimodels.IsBadRequestError(err) {
			t.Fatalf("doJSONRequest() error = %v, want BadRequestError", err)
		}
	})

	t.Run("reports body read failure", func(t *testing.T) {
		readFailure := errors.New("reader failed")
		client := NewClient(WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return testResponse(req, http.StatusOK, &trackedBody{readErr: readFailure}), nil
		})}))
		var result map[string]any
		err := client.doJSONRequest(context.Background(), http.MethodGet, "/read-failure", nil, &result)
		if !ringapimodels.IsNetworkError(err) || !errors.Is(err, readFailure) {
			t.Fatalf("doJSONRequest() error = %v, want wrapped read failure", err)
		}
	})

	t.Run("accepts empty success when no result is expected", func(t *testing.T) {
		client := NewClient(WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return testResponse(req, http.StatusNoContent, io.NopCloser(strings.NewReader(""))), nil
		})}))
		if err := client.doJSONRequest(context.Background(), http.MethodPut, "/empty-success", map[string]bool{"enabled": false}, nil); err != nil {
			t.Fatalf("doJSONRequest() empty success error = %v", err)
		}
	})
}

func TestDoRequestRejectsUnmarshalableBodyAndInvalidMethod(t *testing.T) {
	client := NewClient(WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return testResponse(req, http.StatusOK, io.NopCloser(strings.NewReader("{}"))), nil
	})}))
	if _, err := client.doRequest(context.Background(), http.MethodPost, "/marshal", map[string]any{"callback": func() {}}); !ringapimodels.IsBadRequestError(err) {
		t.Fatalf("unmarshalable request body error = %v", err)
	}
	if _, err := client.doRequest(context.Background(), "bad\nmethod", "/invalid", nil); !ringapimodels.IsNetworkError(err) {
		t.Fatalf("invalid method error = %v", err)
	}
}
