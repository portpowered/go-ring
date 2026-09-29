/*
 * Copyright (c) 2019 Zenichi Amano
 *
 * This file is part of go-push-receiver, which is MIT licensed.
 * See http://opensource.org/licenses/MIT
 */

package pushreceiver

type terminalError string

func (e terminalError) Error() string {
	return string(e)
}

type contextualError struct {
	operation string
	cause     error
}

func (e contextualError) Error() string {
	return e.operation + ": " + e.cause.Error()
}

func (e contextualError) Unwrap() error {
	return e.cause
}

func wrapError(cause error, operation string) error {
	if cause == nil {
		return nil
	}

	return contextualError{operation: operation, cause: cause}
}

// ErrGcmAuthorization is authorization error of GCM.
var ErrGcmAuthorization error = terminalError("GCM authorization error")

// ErrFcmNotEnoughData is error that data is not enough data from FCM.
var ErrFcmNotEnoughData error = terminalError("data Enough from FCM")

// ErrNotFoundInAppData is error that key not found in app data.
var ErrNotFoundInAppData error = terminalError("key not found")
