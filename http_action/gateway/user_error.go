package gateway

// ErrMsgGatewayResponseWait is the stable prefix for cancellation by the caller while waiting on the gateway.
const ErrMsgGatewayResponseWait = "request canceled before gateway response"

// ErrMsgGatewayResponseTimeout is the stable prefix for the gateway failing to respond before the deadline.
const ErrMsgGatewayResponseTimeout = "gateway did not respond before deadline"

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

// TimeoutError means the gateway delivered no response before the deadline. Endpoint failures are
// reported back explicitly, so silence is a platform fault, not a user one.
type TimeoutError struct {
	err error
}

func (e TimeoutError) Error() string {
	return e.err.Error()
}

func (e TimeoutError) Unwrap() error {
	return e.err
}

func NewTimeoutError(err error) TimeoutError {
	return TimeoutError{err: err}
}

// CanceledError means the caller cancelled before a response arrived, e.g. engine shutdown or an
// exhausted execution budget. Not the user's fault, but caperrors.Origin has no "neither side" state,
// so it surfaces as a system error coded Canceled; alerts should exclude that code.
type CanceledError struct {
	err error
}

func (e CanceledError) Error() string {
	return e.err.Error()
}

func (e CanceledError) Unwrap() error {
	return e.err
}

func NewCanceledError(err error) CanceledError {
	return CanceledError{err: err}
}
