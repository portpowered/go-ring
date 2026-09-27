package main

// cliError adds command context while retaining a concrete error type and its
// cause for callers that need to classify failures.
type cliError struct {
	message string
	cause   error
	detail  string
}

func (e cliError) Error() string {
	message := e.message
	if e.cause != nil {
		message += ": " + e.cause.Error()
	}
	if e.detail != "" {
		message += ": " + e.detail
	}
	return message
}

func (e cliError) Unwrap() error {
	return e.cause
}

func commandError(message string) error {
	return cliError{message: message}
}

func wrapCommandError(message string, cause error) error {
	return cliError{message: message, cause: cause}
}

func wrapCommandErrorDetail(message string, cause error, detail string) error {
	return cliError{message: message, cause: cause, detail: detail}
}
