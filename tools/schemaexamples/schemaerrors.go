package main

import "fmt"

type reasonError string

func (reason reasonError) Error() string {
	return string(reason)
}

type contextualError struct {
	operation string
	cause     error
}

func (wrapped contextualError) Error() string {
	return fmt.Sprintf("%s: %v", wrapped.operation, wrapped.cause)
}

func (wrapped contextualError) Unwrap() error {
	return wrapped.cause
}

func wrapError(operation string, cause error) error {
	if cause == nil {
		return nil
	}

	return contextualError{operation: operation, cause: cause}
}

const (
	errNoSchemaDocuments      reasonError = "no YAML or JSON schema documents found"
	errRemoteLoadingDisabled  reasonError = "remote schema loading is disabled"
	errReferenceNotLocal      reasonError = "reference is not a local file reference"
	errReferenceFragment      reasonError = "reference fragment does not resolve"
	errCyclicReference        reasonError = "cyclic reference"
	errCyclicExampleReference reasonError = "cyclic example reference"
	errUnresolvedReference    reasonError = "unresolved reference"
)
