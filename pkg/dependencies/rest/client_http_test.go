package rest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/internal/requestauth"
	"github.com/portpowered/go-ring/pkg/generatedhttp"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

func authenticatedContext() context.Context {
	return requestauth.WithAccount(
		context.Background(),
		requestauth.Account{AccessToken: "test-access-token", HardwareID: "test-hardware"},
	)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type pointerRoundTripper struct {
	roundTrip func(*http.Request) (*http.Response, error)
}

func (t *pointerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.roundTrip(req)
}

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
func (b *trackedBody) Close() error {
	b.closed = true

	return nil
}

func testResponse(req *http.Request, status int, body io.ReadCloser) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     make(http.Header),
		Body:       body,
		Request:    req,
	}
}

func listDevicesRequest(t *testing.T, server string) *http.Request {
	t.Helper()

	request, err := generatedhttp.NewListDevicesRequest(generatedServerBase(server))
	if err != nil {
		t.Fatalf("build generated list devices request: %v", err)
	}

	return request
}

func rebootRequest(t *testing.T, server string, deviceID int64) *http.Request {
	t.Helper()

	request, err := generatedhttp.NewSendDeviceCommandRequest(
		generatedServerBase(server),
		deviceID,
		generatedhttp.DeviceCommand{CommandName: generatedhttp.Reboot},
	)
	if err != nil {
		t.Fatalf("build generated reboot request: %v", err)
	}

	return request
}

func TestRetryRewindsGeneratedRequestBodyAndClosesDiscardedResponse(t *testing.T) {
	t.Parallel()

	var (
		calls         int
		bodies        []string
		firstResponse *trackedBody
	)

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++

		var (
			body []byte
			err  error
		)

		if req.Body != nil {
			body, err = io.ReadAll(req.Body)
			_ = req.Body.Close()
		}

		if err != nil {
			return nil, ringapimodels.NewNetworkError("test transport could not read outgoing request body", err)
		}

		bodies = append(bodies, string(body))

		if req.URL.String() != "https://api.example.test/device_info/v3/devices" || req.Method != http.MethodGet {
			t.Errorf("unexpected request target: %s %s", req.Method, req.URL)
		}

		if calls == 1 {
			firstResponse = &trackedBody{reader: strings.NewReader("busy")}

			return testResponse(req, http.StatusServiceUnavailable, firstResponse), nil
		}

		return testResponse(req, http.StatusNoContent, io.NopCloser(strings.NewReader(""))), nil
	})
	httpClient := &http.Client{Transport: transport}
	client := NewClient(
		WithHTTPClient(httpClient),
		WithEndpointBases("https://api.example.test", "https://oauth.example.test"),
		WithUserAgent("coverage-client/1"),
	)

	request := listDevicesRequest(t, "https://api.example.test")
	request.Body = io.NopCloser(strings.NewReader(`{"enabled":false,"name":"lamp"}`))
	request.ContentLength = int64(len(`{"enabled":false,"name":"lamp"}`))
	request.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(`{"enabled":false,"name":"lamp"}`)), nil
	}

	resp, err := client.sendWithRetry(authenticatedContext(), request)
	if err != nil {
		t.Fatalf("sendWithRetry() error = %v", err)
	}

	_ = resp.Body.Close()

	if calls != 2 {
		t.Fatalf("RoundTrip calls = %d; want 2", calls)
	}

	if len(bodies) != 2 || bodies[0] != bodies[1] || bodies[0] != `{"enabled":false,"name":"lamp"}` {
		t.Fatalf("retry request bodies = %#v", bodies)
	}

	if firstResponse == nil || !firstResponse.closed {
		t.Fatal("discarded 503 response body was not closed before retry")
	}

	if client.HTTPClient() == httpClient {
		t.Fatal("HTTPClient() returned the mutable injected client instead of a copy")
	}

	if client.ConfigurationError() != nil {
		t.Fatalf("valid custom HTTP client configuration error = %v", client.ConfigurationError())
	}
}

func TestWithHTTPClientSnapshotsMutableClientAndReturnsDefensiveCopy(t *testing.T) {
	t.Parallel()

	type observedRequest struct {
		cookie string
		body   string
	}

	var (
		observed []observedRequest
		calls    int
	)

	transport := &pointerRoundTripper{roundTrip: func(req *http.Request) (*http.Response, error) {
		calls++
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, ringapimodels.NewNetworkError("test transport could not read refresh request body", err)
		}
		observed = append(observed, observedRequest{cookie: req.Header.Get("Cookie"), body: string(body)})

		return testResponse(req, http.StatusOK, io.NopCloser(strings.NewReader(
			`{"access_token":"next-access","refresh_token":"next-refresh","token_type":"Bearer"}`,
		))), nil
	}}

	injectedHTTPClient := &http.Client{Transport: transport}
	client := NewClient(
		WithHTTPClient(injectedHTTPClient),
		WithEndpointBases("https://api.example.test", "https://oauth.example.test"),
	)

	if client.ConfigurationError() != nil {
		t.Fatalf("valid HTTP client configuration error = %v", client.ConfigurationError())
	}

	if client.HTTPClient().Transport != transport {
		t.Fatal("HTTP client snapshot did not preserve the injected Transport pointer")
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}

	oauthURL, err := url.Parse("https://oauth.example.test")
	if err != nil {
		t.Fatalf("parse OAuth URL: %v", err)
	}

	jar.SetCookies(oauthURL, []*http.Cookie{{Name: "account_session", Value: "late-cookie"}})
	injectedHTTPClient.Jar = jar

	returnedHTTPClient := client.HTTPClient()
	if returnedHTTPClient == injectedHTTPClient {
		t.Fatal("HTTPClient() returned the caller's mutable HTTP client")
	}

	returnedHTTPClient.Jar = jar

	internalHTTPClient := client.HTTPClient()
	if internalHTTPClient.Jar != nil {
		t.Fatal("mutating the defensive HTTP client copy changed reusable client state")
	}

	accounts := []struct{ refreshToken, hardwareID string }{
		{refreshToken: "first-refresh", hardwareID: "first-hardware"},
		{refreshToken: "second-refresh", hardwareID: "second-hardware"},
	}
	for _, account := range accounts {
		_, err = client.RefreshAccessTokenFor(context.Background(), account.refreshToken, account.hardwareID)
		if err != nil {
			t.Fatalf("refresh %q: %v", account.refreshToken, err)
		}
	}

	if calls != 2 || len(observed) != 2 {
		t.Fatalf("transport calls = %d, observations = %d, want 2", calls, len(observed))
	}

	for index, want := range []string{"first-refresh", "second-refresh"} {
		if observed[index].cookie != "" {
			t.Errorf("request %d sent shared cookie %q", index, observed[index].cookie)
		}

		values, parseErr := url.ParseQuery(observed[index].body)
		if parseErr != nil || values.Get("refresh_token") != want {
			t.Errorf("request %d refresh body = %q, parse error = %v", index, observed[index].body, parseErr)
		}
	}
}

func TestGeneratedMutationIsNotRetried(t *testing.T) {
	t.Parallel()

	t.Run("server error", func(t *testing.T) {
		t.Parallel()

		calls := 0
		client := NewClient(
			WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++

				return testResponse(req, http.StatusInternalServerError, io.NopCloser(strings.NewReader("busy"))), nil
			})}),
		)

		err := client.doGeneratedJSON(authenticatedContext(), rebootRequest(t, "https://api.ring.com", 12345), nil)
		if err == nil {
			t.Fatal("expected the generated mutation request to report the server error")
		}

		if calls != 1 {
			t.Fatalf("PATCH attempts = %d, want 1", calls)
		}
	})

	t.Run("transport failure", func(t *testing.T) {
		t.Parallel()

		calls := 0
		transportErr := restTestError("connection lost")
		client := NewClient(
			WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++

				return nil, transportErr
			})}),
		)

		err := client.doGeneratedJSON(authenticatedContext(), rebootRequest(t, "https://api.ring.com", 12345), nil)

		if !ringapimodels.IsNetworkError(err) || !errors.Is(err, transportErr) || calls != 1 {
			t.Fatalf("PATCH error = %v, attempts = %d; want wrapped transport error and one attempt", err, calls)
		}
	})
}

func TestGeneratedGetRetryCancellationDuringBackoff(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(authenticatedContext())
	defer cancel()

	calls := 0
	discarded := &trackedBody{reader: strings.NewReader("busy")}
	client := NewClient(
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			cancel()

			return testResponse(req, http.StatusInternalServerError, discarded), nil
		})}),
	)

	err := client.doGeneratedJSON(ctx, listDevicesRequest(t, "https://api.ring.com"), nil)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("generated GET error = %v, want context cancellation", err)
	}

	if calls != 1 || !discarded.closed {
		t.Fatalf("RoundTrip calls = %d, response closed = %v", calls, discarded.closed)
	}
}

func TestConfiguredTokenGetterErrorStopsBeforeSendingRequest(t *testing.T) {
	t.Parallel()

	calls := 0
	client := NewClient(
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++

			return testResponse(req, http.StatusOK, io.NopCloser(strings.NewReader("{}"))), nil
		})}),
	)

	err := client.doGeneratedJSON(context.Background(), listDevicesRequest(t, "https://api.ring.com"), nil)

	if !ringapimodels.IsTokenError(err) || calls != 0 {
		t.Fatalf("generated request error = %v, transport calls = %d", err, calls)
	}
}

func TestWithBaseURIAndDirectTokenConfigureRequest(t *testing.T) {
	t.Parallel()

	var observed *http.Request

	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		observed = req.Clone(req.Context())

		return testResponse(req, http.StatusOK, io.NopCloser(strings.NewReader("{}"))), nil
	})}
	client := NewClient(WithHTTPClient(httpClient), WithBaseURI("https://region.example.test/api"))

	err := client.doGeneratedJSON(authenticatedContext(), listDevicesRequest(t, client.baseURI), nil)
	if err != nil {
		t.Fatalf("generated request error = %v", err)
	}

	if observed == nil || observed.URL.String() != "https://region.example.test/api/device_info/v3/devices" ||
		observed.Header.Get("Authorization") != "Bearer test-access-token" {
		t.Fatalf("observed request URL=%v authorization=%q", observed.URL, observed.Header.Get("Authorization"))
	}
}

func TestDoGeneratedJSONDecodesAndPreservesHTTPFailure(t *testing.T) {
	t.Parallel()

	t.Run("decodes success", func(t *testing.T) {
		t.Parallel()

		client := NewClient(
			WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return testResponse(
					req,
					http.StatusOK,
					io.NopCloser(strings.NewReader(`{"count":3,"items":["a","b"]}`)),
				), nil
			})}),
		)

		var result struct {
			Count int      `json:"count"`
			Items []string `json:"items"`
		}

		err := client.doGeneratedJSON(authenticatedContext(), listDevicesRequest(t, client.baseURI), &result)
		if err != nil {
			t.Fatalf("doGeneratedJSON() error = %v", err)
		}

		if result.Count != 3 || len(result.Items) != 2 {
			t.Fatalf("decoded result = %#v", result)
		}
	})

	t.Run("keeps status and body for caller inspection", func(t *testing.T) {
		t.Parallel()

		body := `{"error":"not allowed"}`
		client := NewClient(
			WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return testResponse(req, http.StatusForbidden, io.NopCloser(strings.NewReader(body))), nil
			})}),
		)
		err := client.doGeneratedJSON(authenticatedContext(), listDevicesRequest(t, client.baseURI), nil)

		var httpErr *ringapimodels.HTTPError
		if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusForbidden || httpErr.Body != body {
			t.Fatalf("doGeneratedJSON() error = %#v", err)
		}
	})

	t.Run("reports malformed JSON", func(t *testing.T) {
		t.Parallel()

		client := NewClient(
			WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return testResponse(req, http.StatusOK, io.NopCloser(strings.NewReader("{"))), nil
			})}),
		)

		var result map[string]any

		err := client.doGeneratedJSON(authenticatedContext(), listDevicesRequest(t, client.baseURI), &result)
		if !ringapimodels.IsInternalServerError(err) {
			t.Fatalf("doGeneratedJSON() error = %v, want InternalServerError", err)
		}
	})

	t.Run("reports body read failure", func(t *testing.T) {
		t.Parallel()

		readFailure := restTestError("reader failed")
		client := NewClient(
			WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return testResponse(req, http.StatusOK, &trackedBody{readErr: readFailure}), nil
			})}),
		)

		var result map[string]any

		err := client.doGeneratedJSON(authenticatedContext(), listDevicesRequest(t, client.baseURI), &result)
		if !ringapimodels.IsNetworkError(err) || !errors.Is(err, readFailure) {
			t.Fatalf("doGeneratedJSON() error = %v, want wrapped read failure", err)
		}
	})

	t.Run("accepts empty success when no result is expected", func(t *testing.T) {
		t.Parallel()

		client := NewClient(
			WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return testResponse(req, http.StatusNoContent, io.NopCloser(strings.NewReader(""))), nil
			})}),
		)

		request, err := generatedhttp.NewTurnFloodlightOnRequest(client.baseURI, 12345)
		if err != nil {
			t.Fatalf("build generated floodlight request: %v", err)
		}

		err = client.doGeneratedJSON(authenticatedContext(), request, nil)
		if err != nil {
			t.Fatalf("doGeneratedJSON() empty success error = %v", err)
		}
	})
}
