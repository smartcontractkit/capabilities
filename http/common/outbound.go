package common

import (
	"context"

	"github.com/smartcontractkit/chainlink-common/pkg/services"
	gateway "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
)

// Outbound is how a request leaves this process.
//
// It is the whole of what the HTTP action capability needs of the outside world.
// The capability decides whether a workflow may make a request at all - the
// limits, the validation, the errors a workflow is answered with - and hands the
// request over. Where it then goes, and how, is this: a node sends it to the
// gateway its DON shares, which either fetches it or opens a tunnel for the node
// to fetch through; something running in a CLI, or in an enclave with no gateway
// to speak of, makes the request itself.
//
// None of that reaches the capability, which is the point: swapping how requests
// leave is a matter of handing it a different one of these.
type Outbound interface {
	services.Service

	// SendRequest makes the request and returns what answered it.
	//
	// An error means the request could not be made, rather than that the far side
	// refused it: a 500 from the far side is a response. A UserError marks what the
	// workflow did wrong, so that it is told rather than shown an internal failure.
	SendRequest(ctx context.Context, request gateway.OutboundHTTPRequest) (gateway.OutboundHTTPResponse, error)
}

// UserError is an error the workflow caused: the URL it named, the endpoint it
// chose, the certificate it supplied. It is surfaced to the workflow as its own
// mistake rather than as a failure of the capability.
type UserError struct {
	err error
}

func (e UserError) Error() string { return e.err.Error() }

func (e UserError) Unwrap() error { return e.err }

func NewUserError(err error) UserError { return UserError{err: err} }
