package routegate

import "fmt"

type routeGateError struct {
	message string
	cause   error
}

func (failure routeGateError) Error() string {
	if failure.cause == nil {
		return failure.message
	}

	return failure.message + ": " + failure.cause.Error()
}

func (failure routeGateError) Unwrap() error {
	return failure.cause
}

func wrapRouteGateError(cause error, format string, values ...any) error {
	return routeGateError{message: fmt.Sprintf(format, values...), cause: cause}
}

func newRouteGateError(format string, values ...any) error {
	return routeGateError{message: fmt.Sprintf(format, values...), cause: nil}
}
