package ringapimodels

import (
	"fmt"
	"net/http"
)

// AuthenticationError represents an authentication failure
type AuthenticationError struct {
	Message string
	Status  int
}

// NewRequires2FAError creates a new Requires2FAError
func NewRequires2FAError(message string) *Requires2FAError {
	return &Requires2FAError{
		Message: message,
	}
}

func NewRateLimitError(message string) *RateLimitError {
	return &RateLimitError{
		Message: message,
	}
}

func (e *AuthenticationError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("authentication error: %s", e.Message)
	}
	return fmt.Sprintf("authentication error (status: %d)", e.Status)
}

// IsAuthenticationError checks if an error is an AuthenticationError
func IsAuthenticationError(err error) bool {
	_, ok := err.(*AuthenticationError)
	return ok
}

// ConnectionError represents a connection failure
type ConnectionError struct {
	Message string
	Err     error
}

func (e *ConnectionError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("connection error: %s: %v", e.Message, e.Err)
	}
	return fmt.Sprintf("connection error: %s", e.Message)
}

func (e *ConnectionError) Unwrap() error {
	return e.Err
}

// IsConnectionError checks if an error is a ConnectionError
func IsConnectionError(err error) bool {
	_, ok := err.(*ConnectionError)
	return ok
}

// NetworkError represents a network-level error
type NetworkError struct {
	Message string
	Err     error
}

func (e *NetworkError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("network error: %s: %v", e.Message, e.Err)
	}
	return fmt.Sprintf("network error: %s", e.Message)
}

func (e *NetworkError) Unwrap() error {
	return e.Err
}

// IsNetworkError checks if an error is a NetworkError
func IsNetworkError(err error) bool {
	_, ok := err.(*NetworkError)
	return ok
}

// TokenError represents a token retrieval or validation error
type TokenError struct {
	Message string
	Err     error
}

func (e *TokenError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("token error: %s: %v", e.Message, e.Err)
	}
	return fmt.Sprintf("token error: %s", e.Message)
}

func (e *TokenError) Unwrap() error {
	return e.Err
}

// IsTokenError checks if an error is a TokenError
func IsTokenError(err error) bool {
	_, ok := err.(*TokenError)
	return ok
}

// BadRequestError represents a bad request error
type BadRequestError struct {
	Message string
	Err     error
}

func (e *BadRequestError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("bad request error: %s: %v", e.Message, e.Err)
	}
	return fmt.Sprintf("bad request error: %s", e.Message)
}

func (e *BadRequestError) Unwrap() error {
	return e.Err
}

// IsBadRequestError checks if an error is a BadRequestError
func IsBadRequestError(err error) bool {
	_, ok := err.(*BadRequestError)
	return ok
}

// UnauthorizedError represents an unauthorized error
type UnauthorizedError struct {
	Message string
	Err     error
}

func (e *UnauthorizedError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("unauthorized error: %s: %v", e.Message, e.Err)
	}
	return fmt.Sprintf("unauthorized error: %s", e.Message)
}

func (e *UnauthorizedError) Unwrap() error {
	return e.Err
}

// IsUnauthorizedError checks if an error is a UnauthorizedError
func IsUnauthorizedError(err error) bool {
	_, ok := err.(*UnauthorizedError)
	return ok
}

// NotFoundError represents a not found error
type NotFoundError struct {
	Message string
	Err     error
}

func (e *NotFoundError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("not found error: %s", e.Message)
	}
	return "not found"
}

func (e *NotFoundError) Unwrap() error {
	return e.Err
}

// IsNotFoundError checks if an error is a NotFoundError
func IsNotFoundError(err error) bool {
	_, ok := err.(*NotFoundError)
	return ok
}

// InternalServerError represents an internal server error
type InternalServerError struct {
	Message string
	Err     error
}

func (e *InternalServerError) Error() string {
	return fmt.Sprintf("internal server error: %s: %v", e.Message, e.Err)
}

func (e *InternalServerError) Unwrap() error {
	return e.Err
}

// IsInternalServerError checks if an error is a InternalServerError
func IsInternalServerError(err error) bool {
	_, ok := err.(*InternalServerError)
	return ok
}

// HTTPError represents an HTTP-level error with status code
type HTTPError struct {
	StatusCode int
	Status     string
	Body       string
	Message    string
}

func (e *HTTPError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("HTTP error %d (%s): %s", e.StatusCode, e.Status, e.Message)
	}
	if e.Body != "" {
		return fmt.Sprintf("HTTP error %d (%s): %s", e.StatusCode, e.Status, e.Body)
	}
	return fmt.Sprintf("HTTP error %d (%s)", e.StatusCode, e.Status)
}

// IsHTTPError checks if an error is an HTTPError
func IsHTTPError(err error) bool {
	_, ok := err.(*HTTPError)
	return ok
}

// IsHTTPStatusCode checks if an error is an HTTPError with a specific status code
func IsHTTPStatusCode(err error, code int) bool {
	httpErr, ok := err.(*HTTPError)
	if !ok {
		return false
	}
	return httpErr.StatusCode == code
}

// ClosedError represents an error when trying to use a closed connection
type ClosedError struct {
	Message string
}

func (e *ClosedError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("connection closed: %s", e.Message)
	}
	return "connection closed"
}

// IsClosedError checks if an error is a ClosedError
func IsClosedError(err error) bool {
	_, ok := err.(*ClosedError)
	return ok
}

// Requires2FAError represents an error when 2FA is required
type Requires2FAError struct {
	Message string
}

type RateLimitError struct {
	Message string
}

func (e *RateLimitError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("rate limit error: %s", e.Message)
	}
	return "rate limit error"
}

func (e *Requires2FAError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("2FA required: %s", e.Message)
	}
	return "2FA required"
}

// IsRequires2FAError checks if an error is a Requires2FAError
func IsRequires2FAError(err error) bool {
	_, ok := err.(*Requires2FAError)
	return ok
}

// NewAuthenticationError creates a new AuthenticationError
func NewAuthenticationError(message string, status int) *AuthenticationError {
	return &AuthenticationError{
		Message: message,
		Status:  status,
	}
}

// NewConnectionError creates a new ConnectionError
func NewConnectionError(message string, err error) *ConnectionError {
	return &ConnectionError{
		Message: message,
		Err:     err,
	}
}

// NewNetworkError creates a new NetworkError
func NewNetworkError(message string, err error) *NetworkError {
	return &NetworkError{
		Message: message,
		Err:     err,
	}
}

// NewTokenError creates a new TokenError
func NewTokenError(message string, err error) *TokenError {
	return &TokenError{
		Message: message,
		Err:     err,
	}
}

// NewHTTPError creates a new HTTPError from an HTTP response
func NewHTTPError(resp *http.Response, body string) *HTTPError {
	return &HTTPError{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Body:       body,
	}
}

// NewClosedError creates a new ClosedError
func NewClosedError(message string) *ClosedError {
	return &ClosedError{
		Message: message,
	}
}

func IsRateLimitError(err error) bool {
	_, ok := err.(*RateLimitError)
	return ok
}

// NewBadRequestError creates a new BadRequestError
func NewBadRequestError(message string, err error) *BadRequestError {
	return &BadRequestError{
		Message: message,
		Err:     err,
	}
}

// NewNotFoundError creates a new NotFoundError
func NewNotFoundError(message string, err error) *NotFoundError {
	return &NotFoundError{
		Message: message,
		Err:     err,
	}
}

// NewUnauthorizedError creates a new UnauthorizedError
func NewUnauthorizedError(message string, err error) *UnauthorizedError {
	return &UnauthorizedError{
		Message: message,
		Err:     err,
	}
}

// NewInternalServerError creates a new InternalServerError
func NewInternalServerError(message string, err error) *InternalServerError {
	return &InternalServerError{
		Message: message,
		Err:     err,
	}
}
