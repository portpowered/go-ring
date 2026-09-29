package main

import "fmt"

type captureError struct {
	message string
	cause   error
}

func (failure *captureError) Error() string {
	if failure.cause != nil {
		return failure.message + ": " + failure.cause.Error()
	}

	return failure.message
}

func (failure *captureError) Unwrap() error {
	return failure.cause
}

func captureErrorf(format string, arguments ...any) error {
	return &captureError{message: fmt.Sprintf(format, arguments...), cause: nil}
}

func wrapCaptureError(message string, cause error) error {
	return &captureError{message: message, cause: cause}
}
