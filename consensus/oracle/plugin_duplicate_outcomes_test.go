package oracle_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/chainlink-protos/cre/go/values"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"

	"github.com/smartcontractkit/capabilities/consensus/oracle"
	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"
)

type reportVerifier struct {
	verifyReport   func(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct)
	reportExpected bool
}

func (r *reportVerifier) Verify(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct) {
	if r.reportExpected {
		r.verifyReport(t, report, infos)
	} else {
		t.Errorf("unexpected report: %v", report)
	}
}

func Test_DuplicateOutcomePrevention(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	md1 := newRequestMetaData()
	md2 := newRequestMetaData()

	md1.KeyBundleID = "evm"

	verifier1 := &reportVerifier{
		reportExpected: true,
		verifyReport: func(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct) {
			verifyValueConsensusReport(t, report, infos, values.NewInt64(40), "evm")
		},
	}

	verifier2 := &reportVerifier{
		reportExpected: true,
		verifyReport: func(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct) {
			verifyValueConsensusReport(t, report, infos, values.NewInt64(140), "")
		},
	}

	reqToObservations := map[string]consensusPluginTest{
		md1.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCr(10, md1), newCr(20, md1), newCr(30, md1),
			newCr(40, md1), newCr(50, md1), newCr(60, md1),
			newCr(70, md1)},
			verifyReport: verifier1.Verify,
		},
		md2.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCr(110, md2), newCr(120, md2), newCr(130, md2),
			newCr(140, md2), newCr(150, md2), newCr(160, md2),
			newCr(170, md2)},
			verifyReport: verifier2.Verify,
		},
	}

	pluginsAndStores := createPluginsAndStores(n, t, lggr, f, batchSize, 5)

	addRequestsToAllStores(pluginsAndStores, reqToObservations, t)

	seqNr := uint64(1)
	outcome := runProtocolRoundTestsWithPlugins(ctx, t, reqToObservations, pluginsAndStores, ocr3types.OutcomeContext{
		SeqNr: seqNr,
	})

	verifier1.reportExpected = false
	verifier2.reportExpected = false

	seqNr++
	runProtocolRoundTestsWithPlugins(ctx, t, reqToObservations, pluginsAndStores, ocr3types.OutcomeContext{
		PreviousOutcome: outcome,
		SeqNr:           seqNr,
	})
}

func Test_HistoricalOutcomesAreRemovedOnExpiry(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	successfulRequest := newRequestMetaData()
	pendingRequest := newRequestMetaData()

	successfulRequest.KeyBundleID = "evm"

	reportVerifier1 := &reportVerifier{
		reportExpected: true,
		verifyReport: func(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct) {
			verifyValueConsensusReport(t, report, infos, values.NewInt64(40), "evm")
		},
	}

	reportVerifier2 := &reportVerifier{
		reportExpected: true,
		verifyReport: func(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct) {
			verifyValueConsensusReport(t, report, infos, values.NewInt64(140), "")
		},
	}

	reqToObservations := map[string]consensusPluginTest{
		// Consensus will succeed
		successfulRequest.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCr(10, successfulRequest), newCr(20, successfulRequest), newCr(30, successfulRequest),
			newCr(40, successfulRequest), newCr(50, successfulRequest), newCr(60, successfulRequest),
			newCr(70, successfulRequest)},
			verifyReport: reportVerifier1.Verify,
		},

		// Consensus will fail as insufficient observations and the request will remain pending
		pendingRequest.RequestID(): {requests: []*oracle.ConsensusRequest{
			newCr(110, pendingRequest), nil, newCr(130, pendingRequest),
			newCr(140, pendingRequest), nil, newCr(160, pendingRequest),
			nil},
			verifyReport: reportVerifier2.Verify,
		},
	}

	historicalOutcomeExpirySpan := uint64(3)
	pluginsAndStores := createPluginsAndStores(n, t, lggr, f, batchSize, historicalOutcomeExpirySpan)

	addRequestsToAllStores(pluginsAndStores, reqToObservations, t)

	postProtocolRound := func(t *testing.T, outcome *oracletypes.Outcome, requestIDToOutcome map[string]*oracletypes.RequestOutcome,
		requestIDToHistoricalOutcome map[string]*oracletypes.HistoricalRequestOutcome) {
		// If a request is successful post round remove it here
		for requestID, ro := range requestIDToOutcome {
			if ro.Status == oracletypes.RequestStatus_REQUEST_STATUS_CONSENSUS_SUCCESS {
				removeRequestFromAllStores(pluginsAndStores, requestID)
			}
		}
	}

	var previousOutcome []byte
	for seqNr := uint64(1); seqNr <= 6; seqNr++ {
		var verifyOutcome func(t *testing.T, outcome *oracletypes.Outcome, requestIDToOutcome map[string]*oracletypes.RequestOutcome,
			requestIDToHistoricalOutcome map[string]*oracletypes.HistoricalRequestOutcome)

		switch seqNr {
		case 1:
			reportVerifier1.reportExpected = true
			reportVerifier2.reportExpected = false
			verifyOutcome = func(t *testing.T, outcome *oracletypes.Outcome, requestIDToOutcome map[string]*oracletypes.RequestOutcome,
				requestIDToHistoricalOutcome map[string]*oracletypes.HistoricalRequestOutcome) {
				require.Len(t, outcome.Outcomes, 1)
				require.Len(t, outcome.HistoricalOutcomes, 2)

				require.Equal(t, requestIDToHistoricalOutcome[successfulRequest.RequestID()].Status, oracletypes.RequestStatus_REQUEST_STATUS_CONSENSUS_SUCCESS)
			}
		case 2:

			// Submit the original successfully processed request here to ensure the that the plugin uses historical outcomes to prevent duplicate outcome
			addRequestsToAllStores(pluginsAndStores, map[string]consensusPluginTest{successfulRequest.RequestID(): reqToObservations[successfulRequest.RequestID()]}, t)

			reportVerifier1.reportExpected = false
			reportVerifier2.reportExpected = false
			verifyOutcome = func(t *testing.T, outcome *oracletypes.Outcome,
				requestIDToOutcome map[string]*oracletypes.RequestOutcome,
				requestIDToHistoricalOutcome map[string]*oracletypes.HistoricalRequestOutcome) {
				require.Len(t, outcome.Outcomes, 0)
				require.Len(t, outcome.HistoricalOutcomes, 2)
			}

		case 3:
			// Simulate the observations arriving for the pending request
			updatedPendingRequestObservations := map[string]consensusPluginTest{
				pendingRequest.RequestID(): {requests: []*oracle.ConsensusRequest{
					newCr(110, pendingRequest), newCr(120, pendingRequest), newCr(130, pendingRequest),
					newCr(140, pendingRequest), newCr(150, pendingRequest), newCr(160, pendingRequest),
					newCr(170, pendingRequest)},
					verifyReport: func(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct) {
						verifyValueConsensusReport(t, report, infos, values.NewInt64(140), "")
					}},
			}

			removeRequestFromAllStores(pluginsAndStores, pendingRequest.RequestID())
			addRequestsToAllStores(pluginsAndStores, updatedPendingRequestObservations, t)

			reportVerifier1.reportExpected = false
			reportVerifier2.reportExpected = true
			verifyOutcome = func(t *testing.T, outcome *oracletypes.Outcome,
				requestIDToOutcome map[string]*oracletypes.RequestOutcome,
				requestIDToHistoricalOutcome map[string]*oracletypes.HistoricalRequestOutcome) {
				require.Len(t, outcome.Outcomes, 1)
				require.Len(t, outcome.HistoricalOutcomes, 2)
			}
		case 4:
			reportVerifier1.reportExpected = false
			reportVerifier2.reportExpected = false
			verifyOutcome = func(t *testing.T, outcome *oracletypes.Outcome,
				requestIDToOutcome map[string]*oracletypes.RequestOutcome,
				requestIDToHistoricalOutcome map[string]*oracletypes.HistoricalRequestOutcome) {
				require.Len(t, outcome.Outcomes, 0)
				require.Len(t, outcome.HistoricalOutcomes, 2)
			}

			// Simulate the expiry of the pending request and eventual receipt of the outcome for the successful request, both of which
			// would result in them being removed from the request store.
			removeRequestFromAllStores(pluginsAndStores, pendingRequest.RequestID())
			removeRequestFromAllStores(pluginsAndStores, successfulRequest.RequestID())

		case 5:
			// By this point the historical record of the outcomes for the requests should have been removed
			reportVerifier1.reportExpected = false
			reportVerifier2.reportExpected = false
			verifyOutcome = func(t *testing.T, outcome *oracletypes.Outcome,
				requestIDToOutcome map[string]*oracletypes.RequestOutcome,
				requestIDToHistoricalOutcome map[string]*oracletypes.HistoricalRequestOutcome) {
				require.Len(t, outcome.Outcomes, 0)
				require.Len(t, outcome.HistoricalOutcomes, 0)
			}
		case 6:
			// Simulate what would happen if the historical outcome expiry span was too small and the outcome for the first request was removed too early
			// followed by a resubmission of the request
			addRequestsToAllStores(pluginsAndStores, map[string]consensusPluginTest{successfulRequest.RequestID(): reqToObservations[successfulRequest.RequestID()]}, t)

			reportVerifier1.reportExpected = true
			reportVerifier2.reportExpected = false
			verifyOutcome = func(t *testing.T, outcome *oracletypes.Outcome, requestIDToOutcome map[string]*oracletypes.RequestOutcome,
				requestIDToHistoricalOutcome map[string]*oracletypes.HistoricalRequestOutcome) {
				require.Len(t, outcome.Outcomes, 1)
				require.Len(t, outcome.HistoricalOutcomes, 1)

				require.Equal(t, requestIDToHistoricalOutcome[successfulRequest.RequestID()].Status, oracletypes.RequestStatus_REQUEST_STATUS_CONSENSUS_SUCCESS)
			}
		}

		previousOutcome = runProtocolRoundTestsWithPlugins(ctx, t, reqToObservations, pluginsAndStores, ocr3types.OutcomeContext{
			SeqNr:           seqNr,
			PreviousOutcome: previousOutcome})

		requestsOutcome := &oracletypes.Outcome{}
		err := proto.Unmarshal(previousOutcome, requestsOutcome)
		require.NoError(t, err)

		requestIDToOutcome := make(map[string]*oracletypes.RequestOutcome)
		for _, ro := range requestsOutcome.Outcomes {
			requestIDToOutcome[ro.Metadata.RequestId] = ro
		}

		requestIDToHistoricalOutcome := make(map[string]*oracletypes.HistoricalRequestOutcome)
		for _, ho := range requestsOutcome.HistoricalOutcomes {
			requestIDToHistoricalOutcome[ho.RequestId] = ho
		}

		verifyOutcome(t, requestsOutcome, requestIDToOutcome, requestIDToHistoricalOutcome)

		postProtocolRound(t, requestsOutcome, requestIDToOutcome, requestIDToHistoricalOutcome)
	}
}
