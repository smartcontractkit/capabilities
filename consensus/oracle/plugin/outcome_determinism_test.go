package plugin_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/smartcontractkit/capabilities/consensus/oracle/plugin"
	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"

	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"

	"github.com/smartcontractkit/libocr/commontypes"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	libocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// Verifies Outcome() is deterministic for identical inputs even if the observations arrive in a different order.
func TestOutcomeDeterministicWithErrors(t *testing.T) {
	lggr := logger.Test(t)
	ctx := context.Background()

	const f, n = 1, 4
	reportingPlugin, _ := createReportingPlugin(t, lggr, f, n, 5, defaultMaxLengthBytes)

	md := newRequestMetaData()
	md.KeyBundleID = "evm"
	reqID := md.RequestID()

	makeObs := func(errMsg string, observerID uint8) libocrtypes.AttributedObservation {
		ro := &oracletypes.RequestObservation{
			Metadata: plugin.ToRequestMetaData(md),
			// individual errors are included in the aggregated outcome when consensus fails
			Input:      &sdk.SimpleConsensusInputs{Observation: &sdk.SimpleConsensusInputs_Error{Error: errMsg}},
			ReceivedAt: timestamppb.Now(),
		}
		obs := &oracletypes.Observation{
			Observations: map[string]*oracletypes.RequestObservation{
				reqID: ro,
			},
		}
		b, err := proto.Marshal(obs)
		require.NoError(t, err)
		return libocrtypes.AttributedObservation{
			Observation: b,
			Observer:    commontypes.OracleID(observerID),
		}
	}

	attributed := []libocrtypes.AttributedObservation{
		makeObs("first error", 0),
		makeObs("second error", 1),
		makeObs("third error", 2),
	}

	qBytes, err := proto.Marshal(&oracletypes.Query{RequestIDs: []string{reqID}})
	require.NoError(t, err)

	outctx := ocr3types.OutcomeContext{SeqNr: 1}

	outcomeA, err := reportingPlugin.Outcome(ctx, outctx, qBytes, attributed)
	require.NoError(t, err)

	// Reverse attributed observations to mimic nondeterministic ordering from map iteration.
	reversed := make([]libocrtypes.AttributedObservation, len(attributed))
	for i := range attributed {
		reversed[i] = attributed[len(attributed)-1-i]
	}

	outcomeB, err := reportingPlugin.Outcome(ctx, outctx, qBytes, reversed)
	require.NoError(t, err)

	require.Equal(t, outcomeA, outcomeB, "Outcome must be deterministic regardless of observation order")
}
