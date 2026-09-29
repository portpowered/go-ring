// Package exampleerrors adds operation context to example command errors.
package exampleerrors

type operationError struct {
	operation string
	cause     error
}

func (failure operationError) Error() string {
	if failure.cause == nil {
		return failure.operation
	}

	return failure.operation + ": " + failure.cause.Error()
}

func (failure operationError) Unwrap() error { return failure.cause }

// Wrap adds operation context while preserving the underlying error.
func Wrap(operation string, cause error) error {
	if cause == nil {
		return nil
	}

	return operationError{operation: operation, cause: cause}
}
