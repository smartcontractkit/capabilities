package types

import (
	"context"

	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"
)

type Request interface {
	ID() string
	Type() evmservice.RequestType
}

type EventuallyConsistentRequest interface {
	Request
	Observe(context.Context) ([]byte, error)
}

type LockableToBlockRequest interface {
	Request
	ToEventuallyConsistent(chainHeight *evmservice.ChainHeight) EventuallyConsistentRequest
}

type AggregatableRequest interface {
	Request
	Observe(context.Context, evmservice.ChainHeight)
}

func NewRequest(id string, requestType evmservice.RequestType) Request {
	return &request{
		id:          id,
		requestType: requestType,
	}
}

type request struct {
	id          string
	requestType evmservice.RequestType
}

func (r *request) ID() string {
	return r.id
}

func (r *request) Type() evmservice.RequestType {
	return r.requestType
}

var _ EventuallyConsistentRequest = (*eventuallyConsistentRequest)(nil)

type eventuallyConsistentRequest struct {
	Request
	observe func(context.Context) ([]byte, error)
}

func NewEventuallyConsistentRequest(id string, observe func(context.Context) ([]byte, error)) EventuallyConsistentRequest {
	return &eventuallyConsistentRequest{
		Request: NewRequest(id, evmservice.RequestType_REQUEST_TYPE_EVENTUALLY_CONSISTENT),
		observe: observe,
	}
}

func (r *eventuallyConsistentRequest) Observe(ctx context.Context) ([]byte, error) {
	return r.observe(ctx)
}

type lockableToABlockRequest struct {
	Request
	observe func(context.Context, *evmservice.ChainHeight) ([]byte, error)
}

func NewLockableToABlockRequest(id string, observe func(context.Context, *evmservice.ChainHeight) ([]byte, error)) LockableToBlockRequest {
	return &lockableToABlockRequest{
		Request: NewRequest(id, evmservice.RequestType_REQUEST_TYPE_LOCKABLE_TO_BLOCK),
		observe: observe,
	}
}

func (r *lockableToABlockRequest) ToEventuallyConsistent(chainHeight *evmservice.ChainHeight) EventuallyConsistentRequest {
	return NewEventuallyConsistentRequest(r.ID(), func(ctx context.Context) ([]byte, error) {
		return r.observe(ctx, chainHeight)
	})
}
