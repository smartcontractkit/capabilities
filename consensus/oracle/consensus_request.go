package oracle

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"

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
	Input      *sdk.SimpleConsensusInputs
	ReceivedAt time.Time
	ExpiresAt  time.Time

	CallbackCh chan ConsensusResponse

	Metadata ConsensusRequestMetadata

	observationQuorumTracker *ObservationQuorumTracker
}

func NewConsensusRequest(
	input *sdk.SimpleConsensusInputs,
	receivedAt time.Time,
	expiresAt time.Time,
	callbackCh chan ConsensusResponse,
	metadata ConsensusRequestMetadata,
	observationQuorumTracker *ObservationQuorumTracker,
) *ConsensusRequest {
	return &ConsensusRequest{
		RequestID:                metadata.RequestID(),
		Input:                    input,
		ReceivedAt:               receivedAt,
		ExpiresAt:                expiresAt,
		CallbackCh:               callbackCh,
		Metadata:                 metadata,
		observationQuorumTracker: observationQuorumTracker,
	}
}

func (r *ConsensusRequest) SendResponse(ctx context.Context, resp ConsensusResponse) {
	defer r.forgetObservationQuorumTracking()
	select {
	case <-ctx.Done():
		return
	case r.CallbackCh <- resp:
		close(r.CallbackCh)
	}
}

func (r *ConsensusRequest) SendTimeout(ctx context.Context) {
	var timeoutErr caperrors.Error
	if !r.observationQuorumTracker.ReachedQuorum(r.RequestID) {
		maxObs := r.observationQuorumTracker.MaxObservations(r.RequestID)
		timeoutErr = caperrors.NewPublicSystemError(
			fmt.Errorf("insufficient observations: received at most %d observations before request expired, requestID %s", maxObs, r.RequestID),
			caperrors.InsufficientObservations,
		)
	} else {

		// HERE should this be user error? tbc
		timeoutErr = caperrors.NewPublicSystemError(
			fmt.Errorf("timeout exceeded: could not process consensus request before expiry, requestID %s", r.RequestID),
			caperrors.DeadlineExceeded,
		)
	}

	timeoutResponse := ConsensusResponse{
		ReqID: r.RequestID,
		Err:   timeoutErr,
	}
	r.SendResponse(ctx, timeoutResponse)
}

func (r *ConsensusRequest) forgetObservationQuorumTracking() {
	if r.observationQuorumTracker != nil {
		r.observationQuorumTracker.Forget(r.RequestID)
	}
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
		Input:     proto.Clone(r.Input).(*sdk.SimpleConsensusInputs),

		// No need to copy these, they're value types.
		ReceivedAt: r.ReceivedAt,
		ExpiresAt:  r.ExpiresAt,
		Metadata:   r.Metadata,

		// Intentionally not copied, but are thread-safe.
		CallbackCh: r.CallbackCh,

		// Shared across copies; tracks quorum for the request ID.
		observationQuorumTracker: r.observationQuorumTracker,
	}
}

type ConsensusResponse struct {
	ReqID string

	ConfigDigest  types.ConfigDigest
	SeqNr         uint64
	ReportContext []byte
	RawReport     []byte
	Sigs          []types.AttributedOnchainSignature

	Err caperrors.Error
}

func (r ConsensusResponse) RequestID() string {
	return r.ReqID
}
