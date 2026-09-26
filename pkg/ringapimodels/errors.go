package ringapimodels

import (
	"net/http"

	"github.com/portpowered/go-ring/internal/ringerrors"
)

// Public error aliases preserve errors.As and existing SDK callers while the
// transport packages use the shared internal definitions directly.
type AuthenticationError = ringerrors.AuthenticationError
type ConnectionError = ringerrors.ConnectionError
type NetworkError = ringerrors.NetworkError
type TokenError = ringerrors.TokenError
type BadRequestError = ringerrors.BadRequestError
type UnauthorizedError = ringerrors.UnauthorizedError
type NotFoundError = ringerrors.NotFoundError
type InternalServerError = ringerrors.InternalServerError
type HTTPError = ringerrors.HTTPError
type ClosedError = ringerrors.ClosedError
type Requires2FAError = ringerrors.Requires2FAError
type RateLimitError = ringerrors.RateLimitError

func NewRequires2FAError(message string) *Requires2FAError {
	return ringerrors.NewRequires2FAError(message)
}
func NewRateLimitError(message string) *RateLimitError { return ringerrors.NewRateLimitError(message) }
func NewAuthenticationError(message string, status int) *AuthenticationError {
	return ringerrors.NewAuthenticationError(message, status)
}
func NewConnectionError(message string, err error) *ConnectionError {
	return ringerrors.NewConnectionError(message, err)
}
func NewNetworkError(message string, err error) *NetworkError {
	return ringerrors.NewNetworkError(message, err)
}
func NewTokenError(message string, err error) *TokenError {
	return ringerrors.NewTokenError(message, err)
}
func NewHTTPError(resp *http.Response, body string) *HTTPError {
	return ringerrors.NewHTTPError(resp, body)
}
func NewClosedError(message string) *ClosedError { return ringerrors.NewClosedError(message) }
func NewBadRequestError(message string, err error) *BadRequestError {
	return ringerrors.NewBadRequestError(message, err)
}
func NewNotFoundError(message string, err error) *NotFoundError {
	return ringerrors.NewNotFoundError(message, err)
}
func NewUnauthorizedError(message string, err error) *UnauthorizedError {
	return ringerrors.NewUnauthorizedError(message, err)
}
func NewInternalServerError(message string, err error) *InternalServerError {
	return ringerrors.NewInternalServerError(message, err)
}

func IsAuthenticationError(err error) bool { return ringerrors.IsAuthenticationError(err) }
func IsConnectionError(err error) bool     { return ringerrors.IsConnectionError(err) }
func IsNetworkError(err error) bool        { return ringerrors.IsNetworkError(err) }
func IsTokenError(err error) bool          { return ringerrors.IsTokenError(err) }
func IsBadRequestError(err error) bool     { return ringerrors.IsBadRequestError(err) }
func IsUnauthorizedError(err error) bool   { return ringerrors.IsUnauthorizedError(err) }
func IsNotFoundError(err error) bool       { return ringerrors.IsNotFoundError(err) }
func IsInternalServerError(err error) bool { return ringerrors.IsInternalServerError(err) }
func IsHTTPError(err error) bool           { return ringerrors.IsHTTPError(err) }
func IsHTTPStatusCode(err error, code int) bool {
	return ringerrors.IsHTTPStatusCode(err, code)
}
func IsClosedError(err error) bool      { return ringerrors.IsClosedError(err) }
func IsRequires2FAError(err error) bool { return ringerrors.IsRequires2FAError(err) }
func IsRateLimitError(err error) bool   { return ringerrors.IsRateLimitError(err) }
