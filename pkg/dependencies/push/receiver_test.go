package push_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/ringerrors"
	"github.com/portpowered/go-ring/pkg/dependencies/push"
)

type registrationRoundTrip func(*http.Request) (*http.Response, error)

func (fn registrationRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type failedRegistrationReader struct{}

type unexpectedForwardError struct{}

func (unexpectedForwardError) Error() string { return "unexpected FCM request forwarding" }

const unlistedTestHost = "unlisted.example"

func (failedRegistrationReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func newFCMRegistrationRequest(t *testing.T, body io.Reader) *http.Request {
	t.Helper()

	request, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"https://"+protocol.FCMRegistrationsHost+protocol.FCMRegistrationsPath,
		body,
	)
	if err != nil {
		t.Fatal(err)
	}

	request.Header.Set(protocol.FCMContentTypeHeader, protocol.FCMContentTypeJSON)
	request.Header.Set(protocol.FCMInstallationsAPIKeyHeader, protocol.FCMAPIKey)
	request.Header.Set(protocol.FCMInstallationsAuthHeader, protocol.FCMInstallationsAuthPrefix+"installation-token")

	return request
}

func validRegistrationJSON(t *testing.T) []byte {
	t.Helper()

	body, err := json.Marshal(map[string]any{
		protocol.FCMRegistrationWebKey: map[string]string{
			protocol.FCMRegistrationVAPIDKey:    protocol.FCMDefaultVAPIDKey,
			protocol.FCMRegistrationEndpointKey: protocol.FCMRegistrationEndpointPrefix + "registration-token",
			protocol.FCMRegistrationP256DHKey:   base64.URLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 65)),
			protocol.FCMRegistrationAuthKey:     base64.URLEncoding.EncodeToString(bytes.Repeat([]byte{2}, 16)),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	return body
}

func TestRegistrationTransportForwardsStatusAndRecovery(t *testing.T) {
	t.Parallel()

	status := http.StatusServiceUnavailable
	transport := push.NewRegistrationTransport(registrationRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	}))

	req := newFCMRegistrationRequest(t, bytes.NewReader(validRegistrationJSON(t)))

	response, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}

	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("failed response status = %d", response.StatusCode)
	}

	_ = response.Body.Close()

	status = http.StatusOK
	req = newFCMRegistrationRequest(t, bytes.NewReader(validRegistrationJSON(t)))

	response, err = transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}

	if response.StatusCode != http.StatusOK {
		t.Fatalf("recovery response status = %d", response.StatusCode)
	}

	_ = response.Body.Close()
}

func TestRegistrationTransportWrapsNetworkCause(t *testing.T) {
	t.Parallel()

	transport := push.NewRegistrationTransport(registrationRoundTrip(func(*http.Request) (*http.Response, error) {
		return nil, io.ErrUnexpectedEOF
	}))

	request := newFCMRegistrationRequest(t, bytes.NewReader(validRegistrationJSON(t)))

	response, err := transport.RoundTrip(request)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if !ringerrors.IsNetworkError(err) || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("network failure lost its typed cause: %v", err)
	}
}

func TestRegistrationBodyRejectsMalformedAndUnreadableJSON(t *testing.T) {
	t.Parallel()

	req := newFCMRegistrationRequest(t, strings.NewReader("{"))

	transport := push.NewRegistrationTransport(registrationRoundTrip(func(*http.Request) (*http.Response, error) {
		t.Fatal("registration transport forwarded malformed JSON")

		return nil, unexpectedForwardError{}
	}))
	{
		var syntaxError *json.SyntaxError

		response, err := transport.RoundTrip(req)
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}

		if !ringerrors.IsBadRequestError(err) || !errors.As(err, &syntaxError) {
			t.Fatalf("malformed registration error lost its typed cause: %v", err)
		}
	}

	req.Body = io.NopCloser(failedRegistrationReader{})
	{
		response, err := transport.RoundTrip(req)
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}

		if !ringerrors.IsBadRequestError(err) || !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("unreadable registration error lost its cause: %v", err)
		}
	}
}

func TestRegistrationTransportFailsClosedForUnlistedRoutes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*http.Request)
	}{
		{name: "scheme", mutate: func(request *http.Request) { request.URL.Scheme = "http" }},
		{name: "host", mutate: func(request *http.Request) { request.URL.Host = unlistedTestHost }},
		{name: "method", mutate: func(request *http.Request) { request.Method = http.MethodGet }},
		{name: "path", mutate: func(request *http.Request) { request.URL.Path += "/unexpected" }},
		{name: "query", mutate: func(request *http.Request) { request.URL.RawQuery = "unexpected=1" }},
		{name: "empty query marker", mutate: func(request *http.Request) { request.URL.ForceQuery = true }},
		{name: "fragment", mutate: func(request *http.Request) { request.URL.Fragment = "unexpected" }},
		{name: "userinfo", mutate: func(request *http.Request) { request.URL.User = url.User("unexpected") }},
		{name: "host override", mutate: func(request *http.Request) { request.Host = unlistedTestHost }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			forwarded := false
			transport := push.NewRegistrationTransport(registrationRoundTrip(func(*http.Request) (*http.Response, error) {
				forwarded = true

				return nil, unexpectedForwardError{}
			}))
			request := newFCMRegistrationRequest(t, bytes.NewReader(validRegistrationJSON(t)))
			test.mutate(request)

			response, err := transport.RoundTrip(request)
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}

			if !ringerrors.IsNetworkError(err) {
				t.Fatalf("unlisted route error = %v", err)
			}

			if forwarded {
				t.Fatal("unlisted FCM route reached the next transport")
			}
		})
	}
}

func TestRegistrationTransportFailsClosedForUnexpectedHeaders(t *testing.T) {
	t.Parallel()

	forwarded := false
	transport := push.NewRegistrationTransport(registrationRoundTrip(func(*http.Request) (*http.Response, error) {
		forwarded = true

		return nil, unexpectedForwardError{}
	}))
	request := newFCMRegistrationRequest(t, bytes.NewReader(validRegistrationJSON(t)))
	request.Header.Set("X-Unlisted", "unexpected")

	response, err := transport.RoundTrip(request)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if !ringerrors.IsBadRequestError(err) {
		t.Fatalf("unexpected header error = %v", err)
	}

	if forwarded {
		t.Fatal("request with an unexpected header reached the next transport")
	}
}

func TestRegistrationTransportRejectsUnexpectedVAPIDKey(t *testing.T) {
	t.Parallel()

	forwarded := false
	transport := push.NewRegistrationTransport(registrationRoundTrip(func(*http.Request) (*http.Response, error) {
		forwarded = true

		return nil, unexpectedForwardError{}
	}))
	body := bytes.Replace(
		validRegistrationJSON(t),
		[]byte(protocol.FCMDefaultVAPIDKey),
		[]byte("unexpected-vapid-key"),
		1,
	)
	request := newFCMRegistrationRequest(t, bytes.NewReader(body))

	response, err := transport.RoundTrip(request)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if !ringerrors.IsBadRequestError(err) {
		t.Fatalf("unexpected VAPID key error = %v", err)
	}

	if forwarded {
		t.Fatal("request with an unexpected VAPID key reached the next transport")
	}
}

func TestStartRejectsMalformedSavedCredentials(t *testing.T) {
	t.Parallel()

	var syntaxError *json.SyntaxError

	events, err := push.Start(context.Background(), json.RawMessage("{"))
	if events != nil || !ringerrors.IsBadRequestError(err) || !errors.As(err, &syntaxError) {
		t.Fatalf("malformed saved credentials error = %v, events = %v", err, events)
	}
}
