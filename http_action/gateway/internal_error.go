package gateway

import "fmt"

const internalErrorPrefix = "internal error"

// internalError is a custom error type that automatically prepends "internal error" to error messages
type internalError struct {
	message string
	err     error
}

// Error does not expose the underlying error
func (e internalError) Error() string {
	return fmt.Sprintf("%s: %s", internalErrorPrefix, e.message)
}

// Unwrap returns the underlying error if it exists
func (e internalError) Unwrap() error {
	return e.err
}

// newInternalError creates a new internalError with a message and underlying error
func newInternalError(message string, err error) internalError {
	return internalError{message: message, err: err}
}
