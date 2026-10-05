package ringerrors_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/internal/ringerrors"
)

const maxDiagnosticErrorLength = 256

func TestEmptyDetailErrorsRemainClassifiedAndSafe(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name            string
		err             error
		isClassified    func(error) bool
		statusMatches   func(error) bool
		forbiddenDetail string
	}{
		{
			name:         "authentication",
			err:          &ringerrors.AuthenticationError{Message: "", Status: http.StatusUnauthorized},
			isClassified: ringerrors.IsAuthenticationError,
			statusMatches: func(err error) bool {
				var authenticationError *ringerrors.AuthenticationError
				if !errors.As(err, &authenticationError) {
					return false
				}

				return authenticationError.Status == http.StatusUnauthorized
			},
			forbiddenDetail: "",
		},
		{
			name:            "token",
			err:             &ringerrors.TokenError{Message: "", Err: nil},
			isClassified:    ringerrors.IsTokenError,
			statusMatches:   nil,
			forbiddenDetail: "",
		},
		{
			name:            "unauthorized",
			err:             &ringerrors.UnauthorizedError{Message: "", Err: nil},
			isClassified:    ringerrors.IsUnauthorizedError,
			statusMatches:   nil,
			forbiddenDetail: "",
		},
		{
			name:            "not found",
			err:             &ringerrors.NotFoundError{Message: "", Err: nil},
			isClassified:    ringerrors.IsNotFoundError,
			statusMatches:   nil,
			forbiddenDetail: "",
		},
		{
			name: "http",
			err: &ringerrors.HTTPError{
				StatusCode: http.StatusTeapot,
				Status:     "418 I'm a teapot",
				Body:       "private response details",
				Message:    "",
			},
			isClassified: ringerrors.IsHTTPError,
			statusMatches: func(err error) bool {
				return ringerrors.IsHTTPStatusCode(err, http.StatusTeapot)
			},
			forbiddenDetail: "private response details",
		},
		{
			name:            "closed",
			err:             &ringerrors.ClosedError{Message: "", Err: nil},
			isClassified:    ringerrors.IsClosedError,
			statusMatches:   nil,
			forbiddenDetail: "",
		},
		{
			name:            "rate limit",
			err:             &ringerrors.RateLimitError{Message: "", Err: nil},
			isClassified:    ringerrors.IsRateLimitError,
			statusMatches:   nil,
			forbiddenDetail: "",
		},
		{
			name:            "two factor required",
			err:             &ringerrors.Requires2FAError{Message: ""},
			isClassified:    ringerrors.IsRequires2FAError,
			statusMatches:   nil,
			forbiddenDetail: "",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			message := testCase.err.Error()
			if message == "" || len(message) > maxDiagnosticErrorLength || strings.Contains(message, "%!") {
				t.Fatalf("Error() returned an invalid diagnostic of length %d", len(message))
			}

			if !testCase.isClassified(testCase.err) {
				t.Fatal("error lost its public classification")
			}

			if testCase.statusMatches != nil && !testCase.statusMatches(testCase.err) {
				t.Fatal("error lost its expected HTTP status")
			}

			if testCase.forbiddenDetail != "" && strings.Contains(message, testCase.forbiddenDetail) {
				t.Fatal("error diagnostic exposed the response body")
			}
		})
	}
}

func TestTokenErrorPreservesCause(t *testing.T) {
	t.Parallel()

	err := &ringerrors.TokenError{Message: "", Err: io.EOF}
	if !errors.Is(err, io.EOF) {
		t.Fatal("TokenError did not preserve its cause")
	}

	if !ringerrors.IsTokenError(err) {
		t.Fatal("wrapped TokenError lost its public classification")
	}
}
