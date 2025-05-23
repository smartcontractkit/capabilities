package oracle

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	pb2 "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk/v2/pb"
)

type ConsensusRequest struct {
	id string

	input     *pb.SimpleConsensusInputs
	ExpiresAt time.Time

	CallbackCh chan ConsensusResponse

	Metadata capabilities.RequestMetadata

	KeyBundleID string
}

func NewConsensusRequest(
	id string,
	input *pb.SimpleConsensusInputs,
	expiresAt time.Time,
	callbackCh chan ConsensusResponse,
	metadata capabilities.RequestMetadata,
	keyBundleID string,
) *ConsensusRequest {
	return &ConsensusRequest{
		id:          id,
		input:       input,
		ExpiresAt:   expiresAt,
		CallbackCh:  callbackCh,
		Metadata:    metadata,
		KeyBundleID: keyBundleID,
	}
}

func (r *ConsensusRequest) ID() string {
	return r.id
}

func (r *ConsensusRequest) ExpiryTime() time.Time {
	return r.ExpiresAt
}

func (r *ConsensusRequest) SendResponse(ctx context.Context, resp ConsensusResponse) {
	select {
	case <-ctx.Done():
		return
	case r.CallbackCh <- resp:
		close(r.CallbackCh)
	}
}

func (r *ConsensusRequest) SendTimeout(ctx context.Context) {
	timeoutResponse := ConsensusResponse{
		ReqID: r.ID(),
		Err:   fmt.Errorf("timeout exceeded: could not process consensus request before expiry, requestID %s", r.ID()),
	}
	r.SendResponse(ctx, timeoutResponse)
}

func (r *ConsensusRequest) Copy() *ConsensusRequest {
	return &ConsensusRequest{
		id:    r.id,
		input: proto.Clone(r.input).(*pb.SimpleConsensusInputs),

		// No need to copy these, they're value types.
		ExpiresAt:   r.ExpiresAt,
		Metadata:    r.Metadata,
		KeyBundleID: r.KeyBundleID,

		// Intentionally not copied, but are thread-safe.
		CallbackCh: r.CallbackCh,
	}
}

type ConsensusResponse struct {
	ReqID string
	Value *pb2.Value
	Err   error
}

func (r ConsensusResponse) RequestID() string {
	return r.ReqID
}
