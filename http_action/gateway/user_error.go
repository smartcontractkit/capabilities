package gateway

// ErrMsgGatewayResponseWait is the stable prefix for timeouts and context cancellation while waiting on the gateway.
const ErrMsgGatewayResponseWait = "request canceled before gateway response"

// UserError represents an error caused by user input or user endpoint
// These errors should be surfaced to the user as public errors
type UserError struct {
	err error
}

func (e UserError) Error() string {
	return e.err.Error()
}

func (e UserError) Unwrap() error {
	return e.err
}

func NewUserError(err error) UserError {
	return UserError{err: err}
}
