package main

import (
	"fmt"
	"strings"
)

type contextualError struct {
	message string
	cause   error
}

func (e *contextualError) Error() string {
	return e.message
}

func (e *contextualError) Unwrap() error {
	return e.cause
}

func errorf(format string, args ...any) error {
	if strings.Contains(format, "%w") && len(args) > 0 {
		if cause, ok := args[len(args)-1].(error); ok {
			format = strings.ReplaceAll(format, "%w", "%v")

			return &contextualError{
				message: fmt.Sprintf(format, args...),
				cause:   cause,
			}
		}
	}

	return &contextualError{message: fmt.Sprintf(format, args...), cause: nil}
}
