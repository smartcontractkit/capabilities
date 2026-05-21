package oracle

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math/big"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/smartcontractkit/libocr/commontypes"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2/types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/smartcontractkit/capabilities/libs/chainconsensus/test"

	commoncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/tests"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"

	"github.com/smartcontractkit/capabilities/libs/chainconsensus/oracle/mocks"
	"github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
	tmocks "github.com/smartcontractkit/capabilities/libs/chainconsensus/types/mocks"
)

func TestValidateChainHeight(t *testing.T) {
	testCases := []struct {
		name          string
		chainHeight   *types.ChainHeight
		expectedError string
	}{
		{
			name:          "nil chain height",
			chainHeight:   nil,
			expectedError: "chain height is nil",
		},
		{
			name: "latest < safe",
			chainHeight: &types.ChainHeight{
				Latest:    5,
				Safe:      10,
				Finalized: 2,
			},
			expectedError: "expected latest 5 to be gt or equal to safe 10",
		},
		{
			name: "safe < finalized",
			chainHeight: &types.ChainHeight{
				Latest:    10,
				Safe:      5,
				Finalized: 6,
			},
			expectedError: "expected safe 5 to be gt or equal to finalized 6",
		},
		{
			name: "valid chain height",
			chainHeight: &types.ChainHeight{
				Latest:    15,
				Safe:      10,
				Finalized: 8,
			},
			expectedError: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateChainHeight(tc.chainHeight)
			if tc.expectedError == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.expectedError)
			}
		})
	}
}

func mustQuery(t *testing.T, requestIDs []string) ocrtypes.Query {
	result, err := proto.Marshal(&types.Query{RequestIDs: requestIDs})
	require.NoError(t, err)
	return result
}

func TestObservation(t *testing.T) {
	t.Run("Error if query is invalid", func(t *testing.T) {
		plugin := newReportingPlugin(Config{}, logger.Sugared(logger.Test(t)), nil, nil, test.GetConsensusMetrics(t))
		_, err := plugin.Observation(t.Context(), ocr3types.OutcomeContext{}, []byte("invalid json"))
		require.ErrorContains(t, err, "failed to unmarshal request IDs: proto")
	})
	t.Run("Error if query exceeds max batch size", func(t *testing.T) {
		plugin := newReportingPlugin(Config{MaxBatchSize: 2}, logger.Sugared(logger.Test(t)), nil, nil, test.GetConsensusMetrics(t))
		_, err := plugin.Observation(t.Context(), ocr3types.OutcomeContext{}, mustQuery(t, []string{"1", "2", "3"}))
		require.EqualError(t, err, "too many request IDs: got 3, expected 2")
	})
	newBlockProvider := func(t *testing.T, chainHeight *types.ChainHeight) *mocks.BlocksProvider {
		blocksProvider := mocks.NewBlocksProvider(t)
		blocksProvider.On("GetFinalized").Return(chainHeight.Finalized, nil).Once()
		blocksProvider.On("GetSafe").Return(chainHeight.Safe, nil).Once()
		blocksProvider.On("GetLatest").Return(chainHeight.Latest, nil).Once()
		return blocksProvider
	}
	t.Run("Previous outcome overrides chain height", func(t *testing.T) {
		blocksProvider := newBlockProvider(t, &types.ChainHeight{
			Latest:    10,
			Safe:      9,
			Finalized: 8,
		})
		requestsStore := mocks.NewRequestsHandler(t)
		requestsStore.EXPECT().GetRequestIDs(mock.Anything).Return(nil, nil).Once()
		plugin := newReportingPlugin(Config{}, logger.Sugared(logger.Test(t)), blocksProvider, requestsStore, test.GetConsensusMetrics(t))
		previousOutcome := &types.Outcome{
			ChainHeight: &types.ChainHeight{
				Latest:    15,
				Safe:      14,
				Finalized: 7,
			},
		}
		rawPreviousOutcome, err := proto.Marshal(previousOutcome)
		require.NoError(t, err)
		rawObservation, err := plugin.Observation(t.Context(), ocr3types.OutcomeContext{PreviousOutcome: rawPreviousOutcome}, mustQuery(t, nil))
		require.NoError(t, err)
		var observation types.Outcome
		require.NoError(t, proto.Unmarshal(rawObservation, &observation))
		expectedObservation := &types.Outcome{
			ChainHeight: &types.ChainHeight{
				Latest:    15,
				Safe:      14,
				Finalized: 8,
			},
		}
		require.Empty(t, cmp.Diff(expectedObservation, &observation, protocmp.Transform()))
	})
	t.Run("Logs an error if GetOCRObservation fails", func(t *testing.T) {
		blocksProvider := newBlockProvider(t, &types.ChainHeight{
			Latest:    10,
			Safe:      9,
			Finalized: 8,
		})
		requestsStore := mocks.NewRequestsHandler(t)
		requestsStore.EXPECT().GetRequestIDs(mock.Anything).Return(nil, nil).Once()
		lggr, observedLogs := logger.TestObservedSugared(t, zapcore.ErrorLevel)
		plugin := newReportingPlugin(Config{MaxBatchSize: 1}, lggr, blocksProvider, requestsStore, test.GetConsensusMetrics(t))
		req := tmocks.NewRequest(t)
		req.EXPECT().GetOCRObservation().Return(nil, errors.New("ocr observation error")).Once()
		requestsStore.EXPECT().GetRequest("1").Return(req, true)
		rawObs, err := plugin.Observation(t.Context(), ocr3types.OutcomeContext{}, mustQuery(t, []string{"1"}))
		require.Nil(t, err)
		tests.RequireLogMessage(t, observedLogs, "Failed to get observation for a request - skipping")
		var obs types.Observation
		require.NoError(t, proto.Unmarshal(rawObs, &obs))
		require.Empty(t, obs.Observations)
	})
	t.Run("Happy path", func(t *testing.T) {
		expectedChainHeight := &types.ChainHeight{
			Latest:    10,
			Safe:      9,
			Finalized: 8,
		}
		blocksProvider := newBlockProvider(t, expectedChainHeight)
		requestsStore := mocks.NewRequestsHandler(t)
		requestsStore.EXPECT().GetRequestIDs(mock.Anything).Return(nil, nil).Once()
		requestsStore.EXPECT().GetRequest("request_not_present_in_store").Return(nil, false).Once()

		id := "request_without_observation"
		requestsStore.EXPECT().GetRequest(id).Return(types.NewEventuallyConsistentRequest(id, nil), true).Once()

		id = "request_with_observation"
		withObservation := types.NewEventuallyConsistentRequest(id, nil)
		withObservation.SetObservation([]byte("observation"))
		requestsStore.EXPECT().GetRequest(id).Return(withObservation, true).Once()

		id = "lockable_request"
		requestsStore.EXPECT().GetRequest(id).Return(types.NewLockableToBlockRequest(id, nil), true).Once()

		id = "aggregatable_request"
		aggrWithObservation := types.NewAggregatableRequest(id, nil)
		aggrWithObservation.SetObservation(&types.AggregatableObservation{
			Method: types.AggregationMethodFPlusOneHighest,
			Value:  newDecimal(123, 2),
		})
		requestsStore.EXPECT().GetRequest(id).Return(aggrWithObservation, true).Once()

		id = "aggregatable_request_without_observation"
		requestsStore.EXPECT().GetRequest(id).Return(types.NewAggregatableRequest(id, nil), true).Once()

		testMetadata := commoncap.ResponseMetadata{
			Metering: []commoncap.MeteringNodeDetail{
				{SpendUnit: "test-unit", SpendValue: "1"},
			},
		}
		id = "wf_h:ref_h"
		hashableWithObs := types.NewHashableRequest("wf_h", "ref_h", testMetadata, func(context.Context) (*emptypb.Empty, error) {
			return nil, nil
		})
		hashableWithObs.SetObservation(&emptypb.Empty{})
		expectedHashableObs, err := hashableWithObs.GetOCRObservation()
		require.NoError(t, err)
		requestsStore.EXPECT().GetRequest(id).Return(hashableWithObs, true).Once()

		lockableHashableID := "wf_lth:ref_lth"
		lockableHashable := types.NewLockableToBlockHashableRequest("wf_lth", "ref_lth", testMetadata, func(context.Context, *types.ChainHeight) (*emptypb.Empty, error) {
			return nil, nil
		})
		requestsStore.EXPECT().GetRequest(lockableHashableID).Return(lockableHashable, true).Once()

		plugin := newReportingPlugin(Config{MaxBatchSize: 50, MaxObservationLength: 1000}, logger.Sugared(logger.Test(t)), blocksProvider, requestsStore, test.GetConsensusMetrics(t))
		query := mustQuery(t, []string{"request_not_present_in_store", "request_without_observation", "request_with_observation", "lockable_request", "aggregatable_request", "aggregatable_request_without_observation", id, lockableHashableID})
		rawObservation, err := plugin.Observation(t.Context(), ocr3types.OutcomeContext{}, query)
		require.NoError(t, err)
		var observation types.Observation
		require.NoError(t, proto.Unmarshal(rawObservation, &observation))
		expectedObservation := &types.Observation{
			ChainHeight: expectedChainHeight,
			Observations: map[string]*types.RequestObservation{
				"request_with_observation": {
					Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("observation")},
				},
				"lockable_request": {
					Observation: &types.RequestObservation_LockableToBlock{LockableToBlock: &emptypb.Empty{}},
				},
				"aggregatable_request": {
					Observation: &types.RequestObservation_Aggregatable{Aggregatable: &types.AggregatableObservation{
						Method: types.AggregationMethodFPlusOneHighest,
						Value:  newDecimal(123, 2),
					}},
				},
				id: expectedHashableObs,
				lockableHashableID: {
					Observation: &types.RequestObservation_LockableToBlock{LockableToBlock: &emptypb.Empty{}},
				},
			},
		}
		require.Empty(t, cmp.Diff(expectedObservation, &observation, protocmp.Transform()))
	})
	t.Run("Request remains in queue if adding it will exceed max observation size", func(t *testing.T) {
		expectedChainHeight := &types.ChainHeight{
			Latest:    10,
			Safe:      9,
			Finalized: 8,
		}
		blocksProvider := newBlockProvider(t, expectedChainHeight)
		requestsStore := mocks.NewRequestsHandler(t)
		requestsStore.EXPECT().GetRequestIDs(mock.Anything).Return(nil, nil).Once()
		addRequestWithObservation := func(id string, size int) {
			withObservation := types.NewEventuallyConsistentRequest(id, nil)
			withObservation.SetObservation(make([]byte, size))
			requestsStore.EXPECT().GetRequest(id).Return(withObservation, true).Once()
		}

		const maxObservationLength = 1000
		const requestsThatFitSize = 300
		addRequestWithObservation("request_1", requestsThatFitSize)
		addRequestWithObservation("request_2", requestsThatFitSize)

		id := "aggregatable_request"
		aggrWithObservation := types.NewAggregatableRequest(id, nil)
		aggrWithObservation.SetObservation(&types.AggregatableObservation{
			Method: types.AggregationMethodFPlusOneHighest,
			Value:  newDecimal(123, 2),
		})
		requestsStore.EXPECT().GetRequest(id).Return(aggrWithObservation, true).Once()

		addRequestWithObservation("large_request", 400)

		plugin := newReportingPlugin(Config{MaxBatchSize: 50, MaxObservationLength: maxObservationLength}, logger.Sugared(logger.Test(t)), blocksProvider, requestsStore, test.GetConsensusMetrics(t))
		query := mustQuery(t, []string{"request_1", "request_2", "large_request", "aggregatable_request"})
		rawObservation, err := plugin.Observation(t.Context(), ocr3types.OutcomeContext{}, query)
		require.NoError(t, err)
		var observation types.Observation
		require.NoError(t, proto.Unmarshal(rawObservation, &observation))
		expectedObservation := &types.Observation{
			ChainHeight: expectedChainHeight,
			Observations: map[string]*types.RequestObservation{
				"request_1": {
					Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: make([]byte, requestsThatFitSize)},
				},
				"request_2": {
					Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: make([]byte, requestsThatFitSize)},
				},
				"aggregatable_request": {
					Observation: &types.RequestObservation_Aggregatable{Aggregatable: &types.AggregatableObservation{
						Method: types.AggregationMethodFPlusOneHighest,
						Value:  newDecimal(123, 2),
					}},
				},
			},
		}
		require.Empty(t, cmp.Diff(expectedObservation, &observation, protocmp.Transform()))
	})
	t.Run("Missing requests from a previous round are processed before query requests", func(t *testing.T) {
		expectedChainHeight := &types.ChainHeight{
			Latest:    10,
			Safe:      9,
			Finalized: 8,
		}
		blocksProvider := newBlockProvider(t, expectedChainHeight)
		requestsStore := mocks.NewRequestsHandler(t)
		requestsStore.EXPECT().GetRequest("missing_request_but_not_present_in_store").Return(nil, false).Once()

		id := "missing_request_without_observation"
		requestsStore.EXPECT().GetRequest(id).Return(types.NewEventuallyConsistentRequest(id, nil), true).Once()

		mockRequestWithObservation := func(id string) {
			withObservation := types.NewEventuallyConsistentRequest(id, nil)
			withObservation.SetObservation([]byte("observation"))
			requestsStore.EXPECT().GetRequest(id).Return(withObservation, true).Once()
		}

		mockRequestWithObservation("missing_request_with_observation")
		mockRequestWithObservation("regular_request_with_observation")

		requestsStore.EXPECT().GetRequestIDs(mock.Anything).Return(
			[]string{
				"missing_request_without_observation",
				"missing_request_with_observation",
				"regular_request_with_observation",
				"regular_request_won't_fit",
			}, nil).Once()

		plugin := newReportingPlugin(Config{MaxBatchSize: 2, MaxObservationLength: 1000}, logger.Sugared(logger.Test(t)), blocksProvider, requestsStore, test.GetConsensusMetrics(t))
		query := mustQuery(t, []string{"regular_request_with_observation", "regular_request_won't_fit"})
		previousOutcome := mustMarshalProto(&types.Outcome{
			ChainHeight:       expectedChainHeight,
			MissingRequestIDs: []string{"missing_request_but_not_present_in_store", "missing_request_without_observation", "missing_request_with_observation"},
		})
		rawObservation, err := plugin.Observation(t.Context(), ocr3types.OutcomeContext{PreviousOutcome: previousOutcome}, query)
		require.NoError(t, err)
		var observation types.Observation
		require.NoError(t, proto.Unmarshal(rawObservation, &observation))
		expectedObservation := &types.Observation{
			ChainHeight: expectedChainHeight,
			Observations: map[string]*types.RequestObservation{
				"regular_request_with_observation": {
					Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("observation")},
				},
				"missing_request_with_observation": {
					Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("observation")},
				},
			},
			MissingRequestIDs: []string{"missing_request_without_observation", "missing_request_with_observation"},
		}
		require.Empty(t, cmp.Diff(expectedObservation, &observation, protocmp.Transform()))
	})
}

func TestValidateObservation(t *testing.T) {
	testCases := []struct {
		name           string
		outcomeContext ocr3types.OutcomeContext
		observations   ocrtypes.AttributedObservation
		expectedError  string
	}{
		{
			name: "Valid observation",
			outcomeContext: ocr3types.OutcomeContext{
				PreviousOutcome: mustMarshalProto(&types.Outcome{
					ChainHeight: &types.ChainHeight{
						Latest:    14,
						Safe:      9,
						Finalized: 8,
					},
				}),
			},
			observations: ocrtypes.AttributedObservation{
				Observation: mustMarshalProto(&types.Observation{
					ChainHeight: &types.ChainHeight{
						Latest:    15,
						Safe:      10,
						Finalized: 8,
					},
				}),
			},
			expectedError: "",
		},
		{
			name:          "Invalid observation",
			observations:  ocrtypes.AttributedObservation{Observation: []byte("invalid-data")},
			expectedError: "could not unmarshal proposed observation",
		},
		{
			name: "Invalid chain Height",
			observations: ocrtypes.AttributedObservation{
				Observation: mustMarshalProto(&types.Observation{
					ChainHeight: &types.ChainHeight{
						Latest:    15,
						Safe:      16,
						Finalized: 17,
					},
				}),
			},
			expectedError: "invalid chain height: expected latest 15 to be gt or equal to safe 16",
		},
		{
			name: "Previous outcome has higher blocks than observation",
			outcomeContext: ocr3types.OutcomeContext{
				PreviousOutcome: mustMarshalProto(&types.Outcome{
					ChainHeight: &types.ChainHeight{
						Latest:    14,
						Safe:      9,
						Finalized: 9,
					},
				}),
			},
			observations: ocrtypes.AttributedObservation{
				Observation: mustMarshalProto(&types.Observation{
					ChainHeight: &types.ChainHeight{
						Latest:    15,
						Safe:      10,
						Finalized: 8,
					},
				}),
			},
			expectedError: "invalid chain height compared to previous outcome: expected finalized 8 to be gt or equal to previous finalized 9",
		},
		{
			name: "Duplicate missing requestID",
			observations: ocrtypes.AttributedObservation{
				Observation: mustMarshalProto(&types.Observation{
					ChainHeight:       &types.ChainHeight{},
					MissingRequestIDs: []string{"1", "2", "3", "2"},
				}),
			},
			expectedError: "invalid missing request ids: duplicate missing request ID: 2. OracleID: 0",
		},
		{
			name: "Valid volatile observation (error only)",
			observations: ocrtypes.AttributedObservation{
				Observation: mustMarshalProto(&types.Observation{
					ChainHeight: &types.ChainHeight{Latest: 15, Safe: 10, Finalized: 8},
					Observations: map[string]*types.RequestObservation{
						"volatile-req": {
							Observation: &types.RequestObservation_Volatile{
								Volatile: &types.VolatileObservations{
									Error: []byte("upstream unavailable"),
								},
							},
						},
					},
				}),
			},
			expectedError: "",
		},
		{
			name: "Valid volatile observation with distinct hashes",
			observations: ocrtypes.AttributedObservation{
				Observation: mustMarshalProto(&types.Observation{
					ChainHeight: &types.ChainHeight{Latest: 15, Safe: 10, Finalized: 8},
					Observations: map[string]*types.RequestObservation{
						"volatile-req": {
							Observation: &types.RequestObservation_Volatile{
								Volatile: &types.VolatileObservations{
									Observations: []*types.VolatileObservation{
										{Height: 1, Hash: bytes.Repeat([]byte{1}, types.HashLength)},
										{Height: 2, Hash: bytes.Repeat([]byte{2}, types.HashLength)},
									},
								},
							},
						},
					},
				}),
			},
			expectedError: "",
		},
		{
			name: "Valid volatile observation with error and hashes",
			observations: ocrtypes.AttributedObservation{
				Observation: mustMarshalProto(&types.Observation{
					ChainHeight: &types.ChainHeight{Latest: 15, Safe: 10, Finalized: 8},
					Observations: map[string]*types.RequestObservation{
						"volatile-req": {
							Observation: &types.RequestObservation_Volatile{
								Volatile: &types.VolatileObservations{
									Error: []byte("stale RPC error retained"),
									Observations: []*types.VolatileObservation{
										{Height: 3, Hash: bytes.Repeat([]byte{3}, types.HashLength)},
									},
								},
							},
						},
					},
				}),
			},
			expectedError: "",
		},
		{
			name: "Volatile observation has zero-length hash after proto round-trip",
			observations: ocrtypes.AttributedObservation{
				Observation: mustMarshalProto(&types.Observation{
					ChainHeight: &types.ChainHeight{Latest: 15, Safe: 10, Finalized: 8},
					Observations: map[string]*types.RequestObservation{
						"volatile-req": {
							Observation: &types.RequestObservation_Volatile{
								Volatile: &types.VolatileObservations{
									Observations: []*types.VolatileObservation{
										{Height: 42}, // valid round-trip; hash defaults to empty
									},
								},
							},
						},
					},
				}),
			},
			expectedError: "invalid hash length for volatile observation of request ID volatile-req",
		},
		{
			name: "Volatile observation hash wrong length",
			observations: ocrtypes.AttributedObservation{
				Observation: mustMarshalProto(&types.Observation{
					ChainHeight: &types.ChainHeight{Latest: 15, Safe: 10, Finalized: 8},
					Observations: map[string]*types.RequestObservation{
						"volatile-req": {
							Observation: &types.RequestObservation_Volatile{
								Volatile: &types.VolatileObservations{
									Observations: []*types.VolatileObservation{
										{Height: 1, Hash: bytes.Repeat([]byte{9}, types.HashLength-1)},
									},
								},
							},
						},
					},
				}),
			},
			expectedError: "invalid hash length for volatile observation of request ID volatile-req",
		},
		{
			name: "Duplicate volatile observations for same hash",
			observations: ocrtypes.AttributedObservation{
				Observation: mustMarshalProto(&types.Observation{
					ChainHeight: &types.ChainHeight{Latest: 15, Safe: 10, Finalized: 8},
					Observations: map[string]*types.RequestObservation{
						"volatile-req": {
							Observation: &types.RequestObservation_Volatile{
								Volatile: &types.VolatileObservations{
									Observations: []*types.VolatileObservation{
										{Height: 1, Hash: bytes.Repeat([]byte{0xaa}, types.HashLength)},
										{Height: 9, Hash: bytes.Repeat([]byte{0xaa}, types.HashLength)},
									},
								},
							},
						},
					},
				}),
			},
			expectedError: "duplicate volatile observation for request ID volatile-req",
		},
		{
			name: "Too many volatile observations",
			observations: ocrtypes.AttributedObservation{
				Observation: mustMarshalProto(&types.Observation{
					ChainHeight: &types.ChainHeight{Latest: 15, Safe: 10, Finalized: 8},
					Observations: map[string]*types.RequestObservation{
						"volatile-req": {
							Observation: &types.RequestObservation_Volatile{
								Volatile: &types.VolatileObservations{
									Observations: volatileObservationsOverLimit(),
								},
							},
						},
					},
				}),
			},
			expectedError: "too many volatile observations for request ID volatile-req",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			lggr := logger.Sugared(logger.Test(t))
			plugin := newReportingPlugin(Config{MaxBatchSize: 10}, lggr, nil, nil, test.GetConsensusMetrics(t))

			err := plugin.ValidateObservation(t.Context(), tc.outcomeContext, nil, tc.observations)
			if tc.expectedError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.expectedError)
			}
		})
	}
}

func TestAgreeOnChainHeight(t *testing.T) {
	testCases := []struct {
		name                 string
		observedChainHeights []*types.ChainHeight
		expectedChainHeight  *types.ChainHeight
		expectedError        string
	}{
		{
			name:                 "not enough observations",
			observedChainHeights: []*types.ChainHeight{{}},
			expectedError:        "not enough observations to calculate chain height. Got 1, expected at least 2",
		},
		{
			name:                 "happy path",
			observedChainHeights: []*types.ChainHeight{{Latest: 10, Safe: 9, Finalized: 5}, {Latest: 11, Safe: 7, Finalized: 6}, {Latest: 12, Safe: 8, Finalized: 7}},
			expectedChainHeight:  &types.ChainHeight{Latest: 11, Safe: 8, Finalized: 6},
		},
		{
			name:                 "happy path, small number of observations",
			observedChainHeights: []*types.ChainHeight{{Latest: 10, Safe: 9, Finalized: 5}, {Latest: 11, Safe: 7, Finalized: 6}},
			expectedChainHeight:  &types.ChainHeight{Latest: 11, Safe: 9, Finalized: 6},
		},
		{
			name:                 "happy path, all equal",
			observedChainHeights: []*types.ChainHeight{{Latest: 10, Safe: 9, Finalized: 5}, {Latest: 10, Safe: 9, Finalized: 5}, {Latest: 10, Safe: 9, Finalized: 5}, {Latest: 10, Safe: 9, Finalized: 5}},
			expectedChainHeight:  &types.ChainHeight{Latest: 10, Safe: 9, Finalized: 5},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			plugin := newReportingPlugin(Config{ReportingPluginConfig: ocr3types.ReportingPluginConfig{F: 1}}, logger.Sugared(logger.Test(t)), nil, nil, test.GetConsensusMetrics(t))
			aos := make([]attributedObservation, len(tc.observedChainHeights))
			for i, chainHeight := range tc.observedChainHeights {
				aos[i] = attributedObservation{Observation: &types.Observation{ChainHeight: chainHeight}}
			}
			chainHeight, err := plugin.agreeOnChainHeight(aos)
			if tc.expectedError == "" {
				require.NoError(t, err)
				require.Equal(t, tc.expectedChainHeight, chainHeight)
			} else {
				require.EqualError(t, err, tc.expectedError)
			}
		})
	}
}

func TestOutcome(t *testing.T) {
	newAggregatableObservation := func(coefficient, exponent int32) *types.RequestObservation {
		decimal := newDecimal(coefficient, exponent)
		return &types.RequestObservation{Observation: &types.RequestObservation_Aggregatable{
			Aggregatable: &types.AggregatableObservation{
				Value:  decimal,
				Method: types.AggregationMethodFPlusOneHighest,
			}},
		}
	}
	chainHeight := &types.ChainHeight{Latest: 10, Safe: 9, Finalized: 8}
	testCases := []struct {
		name              string
		requestIDs        []string
		nodesObservations []types.Observation
		expectedError     string
		expectedOutcome   *types.Outcome
		expectedLogs      []string
	}{
		{
			name:              "fails to agree on chain height",
			nodesObservations: []types.Observation{{}}, // only one node provided observations
			expectedError:     "could not determine chain height: not enough observations to calculate chain height. Got 1, expected at least 2",
		},
		{
			name:       "not enough observations of a request to agree on observation type",
			requestIDs: []string{"request_1", "request_2"},
			nodesObservations: []types.Observation{
				{
					// node1
					Observations: map[string]*types.RequestObservation{
						// node1
						"request_1": {Observation: &types.RequestObservation_EventuallyConsistent{}},
					},
				},
				{
					// node1
					Observations: map[string]*types.RequestObservation{
						// node1
						"request_1": {Observation: &types.RequestObservation_EventuallyConsistent{}},
					},
				},
				{
					// node1
					Observations: map[string]*types.RequestObservation{
						// node1
						"request_2": {Observation: &types.RequestObservation_EventuallyConsistent{}},
					},
				},
			},
			expectedOutcome: &types.Outcome{ChainHeight: chainHeight},
			expectedLogs:    []string{"Could not determine observation type"},
		},
		{
			name:       "fails to determine request value",
			requestIDs: []string{"request_1"},
			nodesObservations: []types.Observation{
				{
					// node1
					Observations: map[string]*types.RequestObservation{
						"request_1": {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value1")}},
					},
				},
				{
					// node2
					Observations: map[string]*types.RequestObservation{
						"request_1": {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value2")}},
					},
				},
				{
					// node3
					Observations: map[string]*types.RequestObservation{
						"request_1": {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value3")}},
					},
				},
			},
			expectedOutcome: &types.Outcome{ChainHeight: chainHeight},
			expectedLogs:    []string{"Could not determine request value"},
		},
		{
			name:       "returns error on unsupported observation type",
			requestIDs: []string{"request_1"},
			nodesObservations: []types.Observation{
				{
					// node1
					Observations: map[string]*types.RequestObservation{
						"request_1": {},
					},
				},
				{
					// node2
					Observations: map[string]*types.RequestObservation{
						"request_1": {},
					},
				},
				{
					// node3
					Observations: map[string]*types.RequestObservation{
						"request_1": {},
					},
				},
			},
			expectedError: "unsupported observation type: UNKNOWN",
		},
		{
			name: "Missing requests happy path",
			nodesObservations: []types.Observation{
				{
					// node1
					MissingRequestIDs: []string{"request_1", "request_2"},
				},
				{
					// node2
					MissingRequestIDs: []string{"request_1", "request_3"},
				},
				{
					// node3
					MissingRequestIDs: []string{"request_2"},
				},
			},
			expectedOutcome: &types.Outcome{
				ChainHeight:       chainHeight,
				MissingRequestIDs: []string{"request_1", "request_2"},
			},
		},
		{
			name:       "F+1 nodes agree on request value and F observed error",
			requestIDs: []string{"request"},
			nodesObservations: []types.Observation{
				{
					// node1
					Observations: map[string]*types.RequestObservation{
						"request": {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value1")}},
					},
				},
				{
					// node2
					Observations: map[string]*types.RequestObservation{
						"request": {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value1")}},
					},
				},
				{
					// node3
					Observations: map[string]*types.RequestObservation{
						"request": {Observation: &types.RequestObservation_Error{Error: []byte("request fdailed")}},
					},
				},
			},
			expectedOutcome: &types.Outcome{
				ChainHeight: chainHeight,
				Outcomes: []*types.RequestOutcome{
					{
						RequestID: "request",
						Outcome:   &types.RequestOutcome_EventuallyConsistent{EventuallyConsistent: []byte("value1")},
					},
				},
			},
		},
		{
			name:       "happy path",
			requestIDs: []string{"request_with_common_value", "request_without_common_value", "lockable_request", "hashable_request", "request_known_to_insufficient_number_of_nodes", "aggregatable_request"},
			nodesObservations: func() []types.Observation {
				commonHash := bytes.Repeat([]byte{0x42}, types.HashLength)
				return []types.Observation{
					{
						// node1
						Observations: map[string]*types.RequestObservation{
							"request_with_common_value":                     {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value1")}},
							"request_without_common_value":                  {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value1")}},
							"lockable_request":                              {Observation: &types.RequestObservation_LockableToBlock{LockableToBlock: &emptypb.Empty{}}},
							"hashable_request":                              {Observation: &types.RequestObservation_Hashable{Hashable: commonHash}},
							"request_known_to_insufficient_number_of_nodes": {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value1")}},
							"aggregatable_request":                          newAggregatableObservation(123, 2),
						},
					},
					{
						// node2
						Observations: map[string]*types.RequestObservation{
							"request_with_common_value":                     {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value1")}},
							"request_without_common_value":                  {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value2")}},
							"lockable_request":                              {Observation: &types.RequestObservation_LockableToBlock{LockableToBlock: &emptypb.Empty{}}},
							"hashable_request":                              {Observation: &types.RequestObservation_Hashable{Hashable: commonHash}},
							"request_known_to_insufficient_number_of_nodes": {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value1")}},
							"aggregatable_request":                          newAggregatableObservation(124, 2),
						},
					},
					{
						// node3
						Observations: map[string]*types.RequestObservation{
							"request_with_common_value":    {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value1")}},
							"request_without_common_value": {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value3")}},
							"lockable_request":             {Observation: &types.RequestObservation_LockableToBlock{LockableToBlock: &emptypb.Empty{}}},
							"hashable_request":             {Observation: &types.RequestObservation_Hashable{Hashable: commonHash}},
							"aggregatable_request":         newAggregatableObservation(124, 3),
						},
					},
				}
			}(),
			expectedOutcome: &types.Outcome{
				ChainHeight: chainHeight,
				Outcomes: []*types.RequestOutcome{
					{
						RequestID: "request_with_common_value",
						Outcome:   &types.RequestOutcome_EventuallyConsistent{EventuallyConsistent: []byte("value1")},
					},
					{
						RequestID: "lockable_request",
						Outcome:   &types.RequestOutcome_LockableToBlock{LockableToBlock: &emptypb.Empty{}},
					},
					{
						RequestID: "hashable_request",
						Outcome:   &types.RequestOutcome_Hashable{Hashable: bytes.Repeat([]byte{0x42}, types.HashLength)},
					},
					{
						RequestID: "aggregatable_request",
						Outcome:   &types.RequestOutcome_Aggregatable{Aggregatable: newDecimal(124, 2)},
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			lggr, observed := logger.TestObserved(t, zapcore.DebugLevel)
			plugin := newReportingPlugin(Config{ReportingPluginConfig: ocr3types.ReportingPluginConfig{F: 1, N: 4}}, logger.Sugared(lggr), nil, nil, test.GetConsensusMetrics(t))
			var rawAOs []ocrtypes.AttributedObservation
			for i := range tc.nodesObservations {
				nodesObservations := &tc.nodesObservations[i]
				nodesObservations.ChainHeight = chainHeight
				rawObservation, err := proto.Marshal(nodesObservations)
				require.NoError(t, err)
				rawAOs = append(rawAOs, ocrtypes.AttributedObservation{Observation: rawObservation})
			}
			rawOutcome, err := plugin.Outcome(t.Context(), ocr3types.OutcomeContext{}, mustQuery(t, tc.requestIDs), rawAOs)
			if tc.expectedError == "" {
				require.NoError(t, err)
				var outcome types.Outcome
				require.NoError(t, proto.Unmarshal(rawOutcome, &outcome))
				require.Empty(t, cmp.Diff(tc.expectedOutcome, &outcome, protocmp.Transform()))
			} else {
				require.EqualError(t, err, tc.expectedError)
			}

			for _, expectedLog := range tc.expectedLogs {
				tests.RequireLogMessage(t, observed, expectedLog)
			}
		})
	}
}

func TestAgreeOnEventuallyConsistentValue(t *testing.T) {
	const id = "request_1"
	testCases := []struct {
		name              string
		nodesObservations [][]byte
		expectedError     string
		expectedValue     []byte
	}{
		{
			name: "insufficient total number of observations",
			nodesObservations: [][]byte{
				[]byte("value1"),
				[]byte("value1"),
			},
			expectedError: "insufficient number of observations: expected 3, got 2",
		},
		{
			name: "insufficient number of identical observations",
			nodesObservations: [][]byte{
				[]byte("value1"),
				[]byte("value2"),
				[]byte("value3"),
				[]byte("value4"),
			},
			expectedError: "insufficient number of identical observations: expected 2, got 1",
		},
		{
			name: "prefer value observed by oracle with lowest id",
			nodesObservations: [][]byte{
				[]byte("value1"),
				[]byte("value2"),
				[]byte("value2"),
				[]byte("value1"),
			},
			expectedValue: []byte("value1"),
		},
		{
			name: "happy path",
			nodesObservations: [][]byte{
				[]byte("invalid_value"),
				[]byte("value2"),
				[]byte("value2"),
				[]byte("value4"),
			},
			expectedValue: []byte("value2"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			plugin := newReportingPlugin(Config{ReportingPluginConfig: ocr3types.ReportingPluginConfig{F: 1, N: 4}}, logger.Sugared(logger.Test(t)), nil, nil, test.GetConsensusMetrics(t))
			nodesObservations := make([]attributedObservation, 0, len(tc.nodesObservations))
			for i, ob := range tc.nodesObservations {
				nodesObservations = append(nodesObservations, attributedObservation{
					// G115: integer overflow conversion int -> uint8
					//nolint:gosec
					Observer: commontypes.OracleID(i),
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: ob}},
						},
					},
				})
			}
			value, err := plugin.agreeOnEventuallyConsistentValue(id, nodesObservations)
			if tc.expectedError == "" {
				require.NoError(t, err)
				require.Equal(t, tc.expectedValue, value)
			} else {
				require.EqualError(t, err, tc.expectedError)
			}
		})
	}
}

func TestAgreeOnHashableValue(t *testing.T) {
	const id = "request_1"
	hash32 := func(b byte) []byte {
		return bytes.Repeat([]byte{b}, types.HashLength)
	}
	testCases := []struct {
		name              string
		nodesObservations [][]byte // each entry is types.HashLength bytes (interpretation depends on flags below)
		useWrongType      []bool   // if true, use EventuallyConsistent instead of Hashable
		wrongLength       []bool   // if true, use Hashable with 31-byte payload (invalid length)
		expectedError     string
		expectedValue     []byte
	}{
		{
			name: "insufficient total number of observations",
			nodesObservations: [][]byte{
				hash32(1),
				hash32(1),
			},
			expectedError: "insufficient number of observations: expected 3, got 2",
		},
		{
			name: "insufficient number of identical observations",
			nodesObservations: [][]byte{
				hash32(1),
				hash32(2),
				hash32(3),
				hash32(4),
			},
			expectedError: "insufficient number of identical observations: expected 2, got 1",
		},
		{
			name: "prefer hash observed by oracle with lowest id when counts tie",
			nodesObservations: [][]byte{
				hash32(1),
				hash32(2),
				hash32(2),
				hash32(1),
			},
			expectedValue: hash32(1),
		},
		{
			name: "happy path",
			nodesObservations: [][]byte{
				hash32(0xAA),
				hash32(0xBB),
				hash32(0xBB),
				hash32(0xDD),
			},
			expectedValue: hash32(0xBB),
		},
		{
			name: "wrong observation type and wrong length are ignored",
			nodesObservations: [][]byte{
				hash32(1),
				hash32(1),
				hash32(2),
				hash32(1),
			},
			useWrongType:  []bool{true, false, false, false},
			wrongLength:   []bool{false, false, true, false},
			expectedValue: hash32(1),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			plugin := newReportingPlugin(Config{ReportingPluginConfig: ocr3types.ReportingPluginConfig{F: 1, N: 4}}, logger.Sugared(logger.Test(t)), nil, nil, test.GetConsensusMetrics(t))
			nodesObservations := make([]attributedObservation, 0, len(tc.nodesObservations))
			for i, h := range tc.nodesObservations {
				var ro *types.RequestObservation
				switch {
				case len(tc.useWrongType) > i && tc.useWrongType[i]:
					ro = &types.RequestObservation{Observation: &types.RequestObservation_LockableToBlock{LockableToBlock: &emptypb.Empty{}}}
				case len(tc.wrongLength) > i && tc.wrongLength[i]:
					ro = &types.RequestObservation{Observation: &types.RequestObservation_Hashable{Hashable: h[:types.HashLength-1]}}
				default:
					ro = &types.RequestObservation{Observation: &types.RequestObservation_Hashable{Hashable: h}}
				}
				nodesObservations = append(nodesObservations, attributedObservation{
					// G115: integer overflow conversion int -> uint8
					//nolint:gosec
					Observer: commontypes.OracleID(i),
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: ro,
						},
					},
				})
			}
			value, err := plugin.agreeOnHashableValue(id, nodesObservations)
			if tc.expectedError == "" {
				require.NoError(t, err)
				require.Equal(t, tc.expectedValue, value)
			} else {
				require.EqualError(t, err, tc.expectedError)
			}
		})
	}
}

func TestAgreeOnVolatileValue(t *testing.T) {
	const id = "request_1"
	hash32 := func(b byte) []byte {
		return bytes.Repeat([]byte{b}, types.HashLength)
	}
	testCases := []struct {
		name           string
		observations   []*types.RequestObservation
		expectedError  string
		expectedHash   []byte
		expectedErrors [][]byte // error outcome (modeForError on Volatile.Error); nil if not expected
	}{
		{
			name: "insufficient total number of observations",
			observations: []*types.RequestObservation{
				{Observation: &types.RequestObservation_Volatile{Volatile: &types.VolatileObservations{
					Observations: []*types.VolatileObservation{{Hash: hash32(1), Height: 0}},
				}}},
				{Observation: &types.RequestObservation_Volatile{Volatile: &types.VolatileObservations{
					Observations: []*types.VolatileObservation{{Hash: hash32(1), Height: 0}},
				}}},
			},
			expectedError: "insufficient number of observations: expected 3, got 2",
		},
		{
			name: "happy path",
			observations: []*types.RequestObservation{
				{Observation: &types.RequestObservation_Volatile{Volatile: &types.VolatileObservations{
					Observations: []*types.VolatileObservation{{Hash: hash32(1), Height: 1}},
				}}},
				{Observation: &types.RequestObservation_Volatile{Volatile: &types.VolatileObservations{
					Observations: []*types.VolatileObservation{{Hash: hash32(2), Height: 2}},
				}}},
				{Observation: &types.RequestObservation_Volatile{Volatile: &types.VolatileObservations{
					Observations: []*types.VolatileObservation{{Hash: hash32(2), Height: 3}},
				}}},
				{Observation: &types.RequestObservation_Volatile{Volatile: &types.VolatileObservations{
					Observations: []*types.VolatileObservation{{Hash: hash32(3), Height: 4}},
				}}},
			},
			expectedHash: hash32(2),
		},
		{
			name: "tie-break prefers candidate with lower lowestOracle when supporter counts and median heights tie",
			observations: []*types.RequestObservation{
				{Observation: &types.RequestObservation_Volatile{Volatile: &types.VolatileObservations{
					Observations: []*types.VolatileObservation{{Hash: hash32(1), Height: 10}},
				}}},
				{Observation: &types.RequestObservation_Volatile{Volatile: &types.VolatileObservations{
					Observations: []*types.VolatileObservation{{Hash: hash32(2), Height: 10}},
				}}},
				{Observation: &types.RequestObservation_Volatile{Volatile: &types.VolatileObservations{
					Observations: []*types.VolatileObservation{{Hash: hash32(2), Height: 10}},
				}}},
				{Observation: &types.RequestObservation_Volatile{Volatile: &types.VolatileObservations{
					Observations: []*types.VolatileObservation{{Hash: hash32(1), Height: 10}},
				}}},
			},
			expectedHash: hash32(1),
		},
		{
			name: "tie-break prefers candidate with higher median height when supporter counts tie",
			observations: []*types.RequestObservation{
				{Observation: &types.RequestObservation_Volatile{Volatile: &types.VolatileObservations{
					Observations: []*types.VolatileObservation{{Hash: hash32(1), Height: 10}},
				}}},
				{Observation: &types.RequestObservation_Volatile{Volatile: &types.VolatileObservations{
					Observations: []*types.VolatileObservation{{Hash: hash32(1), Height: 10}},
				}}},
				{Observation: &types.RequestObservation_Volatile{Volatile: &types.VolatileObservations{
					Observations: []*types.VolatileObservation{{Hash: hash32(2), Height: 20}},
				}}},
				{Observation: &types.RequestObservation_Volatile{Volatile: &types.VolatileObservations{
					Observations: []*types.VolatileObservation{{Hash: hash32(2), Height: 20}},
				}}},
			},
			expectedHash: hash32(2),
		},
		{
			name: "wrong observation type and wrong hash length are ignored",
			observations: []*types.RequestObservation{
				{Observation: &types.RequestObservation_LockableToBlock{LockableToBlock: &emptypb.Empty{}}},
				{Observation: &types.RequestObservation_Volatile{Volatile: &types.VolatileObservations{
					Observations: []*types.VolatileObservation{{Hash: hash32(1), Height: 1}},
				}}},
				{Observation: &types.RequestObservation_Volatile{Volatile: &types.VolatileObservations{
					Observations: []*types.VolatileObservation{{Hash: hash32(2)[:types.HashLength-1], Height: 3}},
				}}},
				{Observation: &types.RequestObservation_Volatile{Volatile: &types.VolatileObservations{
					Observations: []*types.VolatileObservation{{Hash: hash32(1), Height: 4}},
				}}},
			},
			expectedHash: hash32(1),
		},
		{
			name: "no hash quorum and no volatile errors yields error",
			observations: []*types.RequestObservation{
				{Observation: &types.RequestObservation_Volatile{Volatile: &types.VolatileObservations{
					Observations: []*types.VolatileObservation{{Hash: hash32(1), Height: 1}},
				}}},
				{Observation: &types.RequestObservation_Volatile{Volatile: &types.VolatileObservations{
					Observations: []*types.VolatileObservation{{Hash: hash32(2), Height: 1}},
				}}},
				{Observation: &types.RequestObservation_Volatile{Volatile: &types.VolatileObservations{
					Observations: []*types.VolatileObservation{{Hash: hash32(3), Height: 1}},
				}}},
				{Observation: &types.RequestObservation_Volatile{Volatile: &types.VolatileObservations{
					Observations: []*types.VolatileObservation{{Hash: hash32(4), Height: 1}},
				}}},
			},
			expectedError: "no volatile outcome candidate reached F+1 supporters",
		},
		{
			name: "no hash quorum but shared Volatile.Error yields error outcome",
			observations: []*types.RequestObservation{
				{Observation: &types.RequestObservation_Volatile{Volatile: &types.VolatileObservations{
					Observations: []*types.VolatileObservation{{Hash: hash32(1), Height: 1}},
					Error:        []byte("boom"),
				}}},
				{Observation: &types.RequestObservation_Volatile{Volatile: &types.VolatileObservations{
					Observations: []*types.VolatileObservation{{Hash: hash32(2), Height: 1}},
					Error:        []byte("boom"),
				}}},
				{Observation: &types.RequestObservation_Volatile{Volatile: &types.VolatileObservations{
					Observations: []*types.VolatileObservation{{Hash: hash32(3), Height: 1}},
					Error:        []byte("boom"),
				}}},
			},
			expectedErrors: [][]byte{[]byte("boom")},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			plugin := newReportingPlugin(Config{ReportingPluginConfig: ocr3types.ReportingPluginConfig{F: 1, N: 4}}, logger.Sugared(logger.Test(t)), nil, nil, test.GetConsensusMetrics(t))
			nodesObservations := make([]attributedObservation, 0, len(tc.observations))
			for i := range tc.observations {
				ro := tc.observations[i]
				nodesObservations = append(nodesObservations, attributedObservation{
					// G115: integer overflow conversion int -> uint8
					//nolint:gosec
					Observer: commontypes.OracleID(i),
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: ro,
						},
					},
				})
			}
			outcome, err := plugin.agreeOnVolatileValue(id, nodesObservations)
			if tc.expectedError != "" {
				require.EqualError(t, err, tc.expectedError)
				require.Nil(t, outcome)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, outcome)
			if tc.expectedErrors != nil {
				require.Equal(t, tc.expectedErrors, outcome.GetError().GetErrors())
			} else {
				require.Equal(t, tc.expectedHash, outcome.GetHashable())
			}
		})
	}
}

func TestAgreeOnObservationType(t *testing.T) {
	const id = "request_1"
	testCases := []struct {
		name          string
		observations  []types.RequestObservation
		expectedError string
		expectedValue types.ObservationType
	}{
		{
			name: "insufficient total number of observations",
			observations: []types.RequestObservation{
				{Observation: &types.RequestObservation_EventuallyConsistent{}},
				{Observation: &types.RequestObservation_EventuallyConsistent{}},
			},
			expectedError: "insufficient number of observations: expected 3, got 2",
		},
		{
			name: "insufficient number of identical observations",
			observations: []types.RequestObservation{
				{Observation: &types.RequestObservation_EventuallyConsistent{}},
				{Observation: &types.RequestObservation_LockableToBlock{}},
				{Observation: &types.RequestObservation_Aggregatable{}},
			},
			expectedError: "insufficient number of identical observations: expected 2, got 1",
		},
		{
			name: "prefer value observed by oracle with lowest id",
			observations: []types.RequestObservation{
				{Observation: &types.RequestObservation_EventuallyConsistent{}},
				{Observation: &types.RequestObservation_LockableToBlock{}},
				{Observation: &types.RequestObservation_LockableToBlock{}},
				{Observation: &types.RequestObservation_EventuallyConsistent{}},
			},
			expectedValue: types.ObservationType_EVENTUALLY_CONSISTENT,
		},
		{
			name: "Happy path aggregatable",
			observations: []types.RequestObservation{
				{Observation: &types.RequestObservation_LockableToBlock{}},
				{Observation: &types.RequestObservation_Aggregatable{}},
				{Observation: &types.RequestObservation_Aggregatable{}},
				{Observation: &types.RequestObservation_EventuallyConsistent{}},
			},
			expectedValue: types.ObservationType_AGGREGATABLE,
		},
		{
			name: "Happy path lockable",
			observations: []types.RequestObservation{
				{Observation: &types.RequestObservation_Aggregatable{}},
				{Observation: &types.RequestObservation_LockableToBlock{}},
				{Observation: &types.RequestObservation_LockableToBlock{}},
				{Observation: &types.RequestObservation_EventuallyConsistent{}},
			},
			expectedValue: types.ObservationType_LOCKABLE_TO_BLOCK,
		},
		{
			name: "Happy path eventually consistent",
			observations: []types.RequestObservation{
				{Observation: &types.RequestObservation_Aggregatable{}},
				{Observation: &types.RequestObservation_EventuallyConsistent{}},
				{Observation: &types.RequestObservation_EventuallyConsistent{}},
				{Observation: &types.RequestObservation_LockableToBlock{}},
			},
			expectedValue: types.ObservationType_EVENTUALLY_CONSISTENT,
		},
		{
			name: "Happy path hashable",
			observations: []types.RequestObservation{
				{Observation: &types.RequestObservation_Error{}},
				{Observation: &types.RequestObservation_Hashable{}},
				{Observation: &types.RequestObservation_Hashable{}},
				{Observation: &types.RequestObservation_LockableToBlock{}},
			},
			expectedValue: types.ObservationType_HASHABLE,
		},
		{
			name: "Happy path volatile",
			observations: []types.RequestObservation{
				{Observation: &types.RequestObservation_Error{}},
				{Observation: &types.RequestObservation_Volatile{}},
				{Observation: &types.RequestObservation_Volatile{}},
				{Observation: &types.RequestObservation_LockableToBlock{}},
			},
			expectedValue: types.ObservationType_VOLATILE,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			plugin := newReportingPlugin(Config{ReportingPluginConfig: ocr3types.ReportingPluginConfig{F: 1, N: 4}}, logger.Sugared(logger.Test(t)), nil, nil, test.GetConsensusMetrics(t))
			nodesObservations := make([]attributedObservation, 0, len(tc.observations))
			for i := range tc.observations {
				ob := &tc.observations[i]
				nodesObservations = append(nodesObservations, attributedObservation{
					// G115: integer overflow conversion int -> uint8
					//nolint:gosec
					Observer:    commontypes.OracleID(i),
					Observation: &types.Observation{Observations: map[string]*types.RequestObservation{id: ob}},
				})
			}
			value, err := plugin.agreeOnObservationType(id, nodesObservations)
			if tc.expectedError == "" {
				require.NoError(t, err)
				require.Equal(t, tc.expectedValue, value)
			} else {
				require.EqualError(t, err, tc.expectedError)
			}
		})
	}
}

func TestAggregateValue(t *testing.T) {
	testCases := []struct {
		name          string
		observations  []*types.AggregatableObservation
		expectedError string
		expectedValue *valuespb.Decimal
	}{
		{
			name: "insufficient total number of observations",
			observations: []*types.AggregatableObservation{
				nil,
				{
					Method: types.AggregationMethodFPlusOneHighest,
					Value:  newDecimal(1, 2),
				},
				{
					Method: types.AggregationMethodFPlusOneHighest,
					Value:  newDecimal(2, 2),
				},
			},
			expectedError: "could not determine aggregation method: insufficient number of observations: expected 3, got 2",
		},
		{
			name: "insufficient number of values",
			observations: []*types.AggregatableObservation{
				{
					Method: types.AggregationMethodFPlusOneHighest,
					Value:  newDecimal(1, 2),
				},
				{
					Method: types.AggregationMethodFPlusOneHighest,
					Value:  nil,
				},
				{
					Method: types.AggregationMethodFPlusOneHighest,
					Value:  &valuespb.Decimal{Coefficient: nil, Exponent: 2},
				},
			},
			expectedError: "not enough observations to aggregate value. Got 1, expected at least 3",
		},
		{
			name: "not supported aggregation method",
			observations: []*types.AggregatableObservation{
				{
					Method: "my_aggregation_method",
					Value:  newDecimal(1, 2),
				},
				{
					Method: "my_aggregation_method",
					Value:  newDecimal(1, 2),
				},
				{
					Method: "my_aggregation_method",
					Value:  newDecimal(1, 2),
				},
			},
			expectedError: "unsupported aggregation method: my_aggregation_method",
		},
		{
			name: "happy path",
			observations: []*types.AggregatableObservation{
				{
					Method: types.AggregationMethodFPlusOneHighest,
					Value:  newDecimal(1, 2),
				},
				{
					Method: types.AggregationMethodFPlusOneHighest,
					Value:  newDecimal(2, 2),
				},
				{
					Method: types.AggregationMethodFPlusOneHighest,
					Value:  newDecimal(3, 2),
				},
				{
					Method: types.AggregationMethodFPlusOneHighest,
					Value:  newDecimal(4, 0),
				},
			},
			expectedValue: newDecimal(2, 2),
		},
	}

	for _, tc := range testCases {
		const id = "id"
		t.Run(tc.name, func(t *testing.T) {
			plugin := newReportingPlugin(Config{ReportingPluginConfig: ocr3types.ReportingPluginConfig{F: 1, N: 4}}, logger.Sugared(logger.Test(t)), nil, nil, test.GetConsensusMetrics(t))
			nodesObservations := make([]attributedObservation, 0, len(tc.observations))
			for i := range tc.observations {
				ob := tc.observations[i]
				nodesObservations = append(nodesObservations, attributedObservation{
					// G115: integer overflow conversion int -> uint8
					//nolint:gosec
					Observer: commontypes.OracleID(i),
					Observation: &types.Observation{Observations: map[string]*types.RequestObservation{id: {
						Observation: &types.RequestObservation_Aggregatable{Aggregatable: ob},
					}}},
				})
			}
			value, err := plugin.aggregateValue(id, nodesObservations)
			if tc.expectedError == "" {
				require.NoError(t, err)
				require.Equal(t, tc.expectedValue, value)
			} else {
				require.EqualError(t, err, tc.expectedError)
			}
		})
	}
}

func TestReports(t *testing.T) {
	info, err := marshalInfo(map[string]any{
		"keyBundleName": "evm",
	})
	require.NoError(t, err)
	hashableRequestInfo, err := marshalInfo(map[string]any{
		"keyBundleName":         "evm",
		reportInfoKeyReportType: reportTypeHashable,
		reportInfoKeyRequestID:  "request_4",
	})
	require.NoError(t, err)
	testCases := []struct {
		name             string
		outcome          *types.Outcome
		expectedErrorLog string
		expectedReports  []ocr3types.ReportPlus[[]byte]
	}{
		{
			name: "successful reports generation",
			outcome: &types.Outcome{
				Outcomes: []*types.RequestOutcome{
					{
						RequestID: "request_1",
						Outcome:   &types.RequestOutcome_EventuallyConsistent{EventuallyConsistent: []byte("value_1")},
					},
					{
						RequestID: "request_2",
						Outcome:   &types.RequestOutcome_LockableToBlock{LockableToBlock: &emptypb.Empty{}},
					},
					{
						RequestID: "request_3",
						Outcome:   &types.RequestOutcome_Aggregatable{Aggregatable: newDecimal(124, 2)},
					},
					{
						RequestID: "request_4",
						Outcome:   &types.RequestOutcome_Hashable{Hashable: bytes.Repeat([]byte{0x42}, types.HashLength)},
					},
				},
				ChainHeight: &types.ChainHeight{
					Latest:    15,
					Safe:      10,
					Finalized: 8,
				},
			},
			expectedReports: []ocr3types.ReportPlus[[]byte]{
				{
					ReportWithInfo: ocr3types.ReportWithInfo[[]byte]{
						Report: mustMarshalProto(&types.RequestReport{
							RequestID: "request_1",
							Report:    &types.RequestReport_EventuallyConsistent{EventuallyConsistent: []byte("value_1")},
						}),
						Info: info,
					},
				},
				{
					ReportWithInfo: ocr3types.ReportWithInfo[[]byte]{
						Report: mustMarshalProto(&types.RequestReport{
							RequestID: "request_2",
							Report:    &types.RequestReport_LockableToBlock{LockableToBlock: &types.ChainHeight{Latest: 15, Safe: 10, Finalized: 8}},
						}),
						Info: info,
					},
				},
				{
					ReportWithInfo: ocr3types.ReportWithInfo[[]byte]{
						Report: mustMarshalProto(&types.RequestReport{
							RequestID: "request_3",
							Report:    &types.RequestReport_Aggregatable{Aggregatable: newDecimal(124, 2)},
						}),
						Info: info,
					},
				},
				{
					ReportWithInfo: ocr3types.ReportWithInfo[[]byte]{
						Report: bytes.Repeat([]byte{0x42}, types.HashLength),
						Info:   hashableRequestInfo,
					},
				},
			},
		},
		{
			name: "unsupported observation type",
			outcome: &types.Outcome{
				Outcomes: []*types.RequestOutcome{
					{
						RequestID: "invalid_request",
						Outcome:   nil,
					},
				},
			},
			expectedErrorLog: "Failed to get report and info for request outcome, skipping report generation for this request",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			lggr, observer := logger.TestObservedSugared(t, zapcore.ErrorLevel)
			rp := newReportingPlugin(Config{}, lggr, nil, nil, test.GetConsensusMetrics(t))

			reports, err := rp.Reports(t.Context(), 1, mustMarshalProto(tc.outcome))
			if tc.expectedErrorLog != "" {
				tests.RequireLogMessage(t, observer, tc.expectedErrorLog)
				require.Empty(t, reports)
				require.Nil(t, err)
			} else {
				require.NoError(t, err)
				require.Len(t, reports, len(tc.expectedReports))
				for i := range reports {
					require.Equal(t, tc.expectedReports[i].ReportWithInfo.Report, reports[i].ReportWithInfo.Report)
					require.Equal(t, tc.expectedReports[i].ReportWithInfo.Info, reports[i].ReportWithInfo.Info)
				}
			}
		})
	}
}

func newDecimal(coefficient, exponent int32) *valuespb.Decimal {
	return &valuespb.Decimal{
		Coefficient: valuespb.NewBigIntFromInt(big.NewInt(int64(coefficient))),
		Exponent:    exponent,
	}
}

func volatileObservationsOverLimit() []*types.VolatileObservation {
	obs := make([]*types.VolatileObservation, types.MaxNumberOfVolatileObservations+1)
	for i := range obs {
		hash := make([]byte, types.HashLength)
		binary.BigEndian.PutUint64(hash, uint64(i))
		obs[i] = &types.VolatileObservation{Height: 1, Hash: hash}
	}
	return obs
}

func mustMarshalProto(v proto.Message) []byte {
	result, err := proto.Marshal(v)
	if err != nil {
		panic(err)
	}
	return result
}

func TestIsVolatileCandidateBetter(t *testing.T) {
	testCases := []struct {
		name string
		a, b volatileOutcomeCandidate
		want bool
	}{
		{
			name: "more supporters wins regardless of height",
			a: volatileOutcomeCandidate{
				hash: types.Hash{1}, supporters: 5, lowestOracle: 9, heights: []int64{1},
			},
			b: volatileOutcomeCandidate{
				hash: types.Hash{2}, supporters: 3, lowestOracle: 1, heights: []int64{1_000_000},
			},
			want: true,
		},
		{
			name: "equal supporters higher median height wins",
			a: volatileOutcomeCandidate{
				hash: types.Hash{1}, supporters: 3, lowestOracle: 5, heights: []int64{30, 1},
			},
			b: volatileOutcomeCandidate{
				hash: types.Hash{2}, supporters: 3, lowestOracle: 1, heights: []int64{10, 10},
			},
			want: true,
		},
		{
			name: "equal supporters higher median height wins (height order does not matter)",
			a: volatileOutcomeCandidate{
				hash: types.Hash{1}, supporters: 3, lowestOracle: 5, heights: []int64{1, 30},
			},
			b: volatileOutcomeCandidate{
				hash: types.Hash{2}, supporters: 3, lowestOracle: 1, heights: []int64{10, 10},
			},
			want: true,
		},
		{
			name: "equal supporters equal median uses lower oracle id",
			a: volatileOutcomeCandidate{
				hash: types.Hash{2}, supporters: 2, lowestOracle: 2, heights: []int64{100, 200},
			},
			b: volatileOutcomeCandidate{
				hash: types.Hash{1}, supporters: 2, lowestOracle: 7, heights: []int64{150, 150},
			},
			want: true,
		},
		{
			name: "tie on supporters median oracle uses lexicographically smaller hash",
			a: volatileOutcomeCandidate{
				hash: types.Hash{1}, supporters: 2, lowestOracle: 4, heights: []int64{10, 20},
			},
			b: volatileOutcomeCandidate{
				hash: types.Hash{2}, supporters: 2, lowestOracle: 4, heights: []int64{10, 20},
			},
			want: true,
		},
		{
			name: "single observation median odd count",
			a: volatileOutcomeCandidate{
				hash: types.Hash{1}, supporters: 1, lowestOracle: 0, heights: []int64{42, 41, 1},
			},
			b: volatileOutcomeCandidate{
				hash: types.Hash{2}, supporters: 1, lowestOracle: 0, heights: []int64{50, 40, 39},
			},
			want: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			a, b := tc.a, tc.b
			require.Equal(t, tc.want, isVolatileCandidateABetter(&a, &b))
			// ensure that order of comparison does not matter
			require.Equal(t, !tc.want, isVolatileCandidateABetter(&b, &a))
		})
	}
}
