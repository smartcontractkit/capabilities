package oracle

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
)

func TestConsensusRequest_SendTimeout_InsufficientObservations(t *testing.T) {
	t.Parallel()

	tracker := NewObservationQuorumTracker()
	tracker.Record("exec-1-01", 3, 5)

	req := NewConsensusRequest(
		&sdk.SimpleConsensusInputs{},
		time.Now(),
		time.Now().Add(time.Second),
		make(chan ConsensusResponse, 1),
		testRequestMetadata("exec-1", "01"),
		tracker,
	)

	req.SendTimeout(context.Background())

	resp := <-req.CallbackCh
	var capErr caperrors.Error
	require.True(t, errors.As(resp.Err, &capErr))
	require.Equal(t, caperrors.InsufficientObservations, capErr.Code())
	require.Contains(t, capErr.Error(), "insufficient observations")
}

func TestConsensusRequest_SendTimeout_DeadlineExceededWhenQuorumReached(t *testing.T) {
	t.Parallel()

	tracker := NewObservationQuorumTracker()
	tracker.Record("exec-1-01", 5, 5)

	req := NewConsensusRequest(
		&sdk.SimpleConsensusInputs{},
		time.Now(),
		time.Now().Add(time.Second),
		make(chan ConsensusResponse, 1),
		testRequestMetadata("exec-1", "01"),
		tracker,
	)

	req.SendTimeout(context.Background())

	resp := <-req.CallbackCh
	var capErr caperrors.Error
	require.True(t, errors.As(resp.Err, &capErr))
	require.Equal(t, caperrors.DeadlineExceeded, capErr.Code())
}

func testRequestMetadata(workflowExecutionID, referenceID string) ConsensusRequestMetadata {
	return ConsensusRequestMetadata{
		RequestMetadata: capabilities.RequestMetadata{
			WorkflowExecutionID: workflowExecutionID,
			ReferenceID:         referenceID,
		},
	}
}
