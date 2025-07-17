package oracle

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk/v2/pb"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"
)

type ConsensusRequestMetadata struct {
	capabilities.RequestMetadata
	KeyBundleID string
	ReportID    string
	RequestType oracletypes.RequestType
}

func (m ConsensusRequestMetadata) RequestID() string {
	return m.WorkflowExecutionID + "-" + m.ReferenceID
}

type ConsensusRequest struct {
	RequestID  string
	Input      *pb.SimpleConsensusInputs
	ReceivedAt time.Time
	ExpiresAt  time.Time

	CallbackCh chan ConsensusResponse

	Metadata ConsensusRequestMetadata
}

func NewConsensusRequest(
	input *pb.SimpleConsensusInputs,
	receivedAt time.Time,
	expiresAt time.Time,
	callbackCh chan ConsensusResponse,
	metadata ConsensusRequestMetadata,
) *ConsensusRequest {
	return &ConsensusRequest{
		RequestID:  metadata.RequestID(),
		Input:      input,
		ReceivedAt: receivedAt,
		ExpiresAt:  expiresAt,
		CallbackCh: callbackCh,
		Metadata:   metadata,
	}
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
		ReqID: r.RequestID,
		Err:   fmt.Errorf("timeout exceeded: could not process consensus request before expiry, requestID %s", r.RequestID),
	}
	r.SendResponse(ctx, timeoutResponse)
}

func (r *ConsensusRequest) ID() string {
	return r.RequestID
}

func (r *ConsensusRequest) ExpiryTime() time.Time {
	return r.ExpiresAt
}

func (r *ConsensusRequest) Copy() *ConsensusRequest {
	return &ConsensusRequest{
		RequestID: r.RequestID,
		Input:     proto.Clone(r.Input).(*pb.SimpleConsensusInputs),

		// No need to copy these, they're value types.
		ReceivedAt: r.ReceivedAt,
		ExpiresAt:  r.ExpiresAt,
		Metadata:   r.Metadata,

		// Intentionally not copied, but are thread-safe.
		CallbackCh: r.CallbackCh,
	}
}

type ConsensusResponse struct {
	ReqID string

	ConfigDigest  types.ConfigDigest
	SeqNr         uint64
	ReportContext []byte
	RawReport     []byte
	Sigs          []types.AttributedOnchainSignature

	Err error
}

func (r ConsensusResponse) RequestID() string {
	return r.ReqID
}
