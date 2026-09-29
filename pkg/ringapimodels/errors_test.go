package ringapimodels_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	m "github.com/portpowered/go-ring/pkg/ringapimodels"
)

type unrelatedTestError string

func (failure unrelatedTestError) Error() string { return string(failure) }

type contextualTestError struct {
	message string
	cause   error
}

func (failure contextualTestError) Error() string {
	return failure.message + ": " + failure.cause.Error()
}

func (failure contextualTestError) Unwrap() error { return failure.cause }

func TestErrorClassificationSurvivesCallerWrapping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		err      error
		classify func(error) bool
	}{
		{"authentication", m.NewAuthenticationError("denied", 401), m.IsAuthenticationError},
		{"connection", m.NewConnectionError("connect", context.Canceled), m.IsConnectionError},
		{"network", m.NewNetworkError("send", context.Canceled), m.IsNetworkError},
		{"token", m.NewTokenError("load", context.Canceled), m.IsTokenError},
		{"bad request", m.NewBadRequestError("decode", context.Canceled), m.IsBadRequestError},
		{"unauthorized", m.NewUnauthorizedError("denied", context.Canceled), m.IsUnauthorizedError},
		{"not found", m.NewNotFoundError("missing", context.Canceled), m.IsNotFoundError},
		{"server", m.NewInternalServerError("failed", context.Canceled), m.IsInternalServerError},
		{
			"HTTP",
			m.NewHTTPError(
				&http.Response{StatusCode: http.StatusTooManyRequests, Status: "429 Too Many Requests"},
				"private",
			),
			m.IsHTTPError,
		},
		{"closed", m.NewClosedError("closed"), m.IsClosedError},
		{"2FA", m.NewRequires2FAError("code needed"), m.IsRequires2FAError},
		{"rate", m.NewRateLimitError("slow down"), m.IsRateLimitError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			wrapped := contextualTestError{message: "load device", cause: tc.err}
			if !tc.classify(tc.err) || !tc.classify(wrapped) {
				t.Fatal("lost error classification")
			}

			if tc.classify(nil) || tc.classify(unrelatedTestError("unrelated")) {
				t.Fatal("misclassified unrelated error")
			}

			if !strings.Contains(wrapped.Error(), "load device:") {
				t.Fatal("lost caller context")
			}

			if unwrapped, ok := tc.err.(interface{ Unwrap() error }); ok && unwrapped.Unwrap() != nil &&
				!errors.Is(wrapped, context.Canceled) {
				t.Fatal("lost cancellation cause")
			}
		})
	}

	var typedNil *m.HTTPError
	if m.IsHTTPError(typedNil) || m.IsHTTPStatusCode(typedNil, 0) {
		t.Fatal("typed nil classified as HTTP failure")
	}
}

func TestHTTPErrorStatusAndBodySafety(t *testing.T) {
	t.Parallel()

	httpError := m.NewHTTPError(
		&http.Response{StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable"},
		`{"token":"do-not-log"}`,
	)

	wrapped := contextualTestError{message: "operation", cause: httpError}
	if !m.IsHTTPStatusCode(wrapped, 503) || m.IsHTTPStatusCode(wrapped, 404) ||
		m.IsHTTPStatusCode(unrelatedTestError("other"), 503) {
		t.Fatal("incorrect wrapped HTTP status")
	}

	if strings.Contains(httpError.Error(), "do-not-log") || !strings.Contains(httpError.Error(), "503") {
		t.Fatalf("unsafe HTTP error: %s", httpError)
	}

	if !strings.Contains(httpError.Body, "do-not-log") {
		t.Fatal("explicit diagnostic body lost")
	}

	httpError.Message = "request rejected"
	if !strings.Contains(httpError.Error(), "request rejected") || strings.Contains(httpError.Error(), "do-not-log") {
		t.Fatal("message formatting leaked body")
	}
}

func TestZeroValueErrorMessagesAndNilCauses(t *testing.T) {
	t.Parallel()

	// Callers can construct exported error types. Zero values must be useful,
	// non-panicking diagnostics and nil causes must not imply cancellation.
	values := []error{
		&m.AuthenticationError{Status: 401},
		&m.ConnectionError{},
		&m.NetworkError{},
		&m.TokenError{},
		&m.BadRequestError{},
		&m.UnauthorizedError{},
		&m.NotFoundError{},
		&m.InternalServerError{},
		&m.HTTPError{},
		&m.ClosedError{},
		&m.Requires2FAError{},
		&m.RateLimitError{},
	}
	for _, err := range values {
		if err.Error() == "" {
			t.Fatalf("empty diagnostic for %T", err)
		}

		if errors.Is(err, context.Canceled) {
			t.Fatalf("false cancellation for %T", err)
		}
	}
}
