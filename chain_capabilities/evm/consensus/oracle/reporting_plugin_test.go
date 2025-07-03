package oracle

import (
	"testing"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/tests"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2/types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/google/go-cmp/cmp"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/oracle/mocks"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/types"
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
		plugin := newReportingPlugin(Config{}, logger.Sugared(logger.Test(t)), nil, nil)
		_, err := plugin.Observation(t.Context(), ocr3types.OutcomeContext{}, []byte("invalid json"))
		require.ErrorContains(t, err, "failed to unmarshal request IDs: proto")
	})
	t.Run("Error if query exceeds max batch size", func(t *testing.T) {
		plugin := newReportingPlugin(Config{BatchSize: 1, MaxAllowedBatchSize: 2}, logger.Sugared(logger.Test(t)), nil, nil)
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
		plugin := newReportingPlugin(Config{}, logger.Sugared(logger.Test(t)), blocksProvider, nil)
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
	t.Run("Returns an error if request is of unknown type", func(t *testing.T) {
		blocksProvider := newBlockProvider(t, &types.ChainHeight{
			Latest:    10,
			Safe:      9,
			Finalized: 8,
		})
		requestsStore := mocks.NewRequestsHandler(t)
		plugin := newReportingPlugin(Config{MaxAllowedBatchSize: 1}, logger.Sugared(logger.Test(t)), blocksProvider, requestsStore)
		requestsStore.EXPECT().GetRequest("1").Return(types.Request(nil), true)
		_, err := plugin.Observation(t.Context(), ocr3types.OutcomeContext{}, mustQuery(t, []string{"1"}))
		require.EqualError(t, err, "failed to observe request: unsupported request type: <nil>")
	})
	t.Run("Happy path", func(t *testing.T) {
		expectedChainHeight := &types.ChainHeight{
			Latest:    10,
			Safe:      9,
			Finalized: 8,
		}
		blocksProvider := newBlockProvider(t, expectedChainHeight)
		requestsStore := mocks.NewRequestsHandler(t)
		requestsStore.EXPECT().GetRequest("request_not_present_in_store").Return(nil, false).Once()

		id := "request_without_observation"
		requestsStore.EXPECT().GetRequest(id).Return(types.NewEventuallyConsistentRequest(id, nil), true).Once()

		id = "request_with_observation"
		withObservation := types.NewEventuallyConsistentRequest(id, nil)
		withObservation.SetObservation([]byte("observation"))
		requestsStore.EXPECT().GetRequest(id).Return(withObservation, true).Once()

		id = "lockable_request"
		requestsStore.EXPECT().GetRequest(id).Return(types.NewLockableToBlockRequest(id, nil), true).Once()

		plugin := newReportingPlugin(Config{MaxAllowedBatchSize: 50}, logger.Sugared(logger.Test(t)), blocksProvider, requestsStore)
		query := mustQuery(t, []string{"request_not_present_in_store", "request_without_observation", "request_with_observation", "lockable_request"})
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
			},
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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			lggr := logger.Sugared(logger.Test(t))
			plugin := newReportingPlugin(Config{}, lggr, nil, nil)

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
			plugin := newReportingPlugin(Config{ReportingPluginConfig: ocr3types.ReportingPluginConfig{F: 1}}, logger.Sugared(logger.Test(t)), nil, nil)
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
	chainHeight := &types.ChainHeight{Latest: 10, Safe: 9, Finalized: 8}
	testCases := []struct {
		name              string
		requestIDs        []string
		nodesObservations []map[string]*types.RequestObservation
		expectedError     string
		expectedOutcome   *types.Outcome
		expectedLogs      []string
	}{
		{
			name:              "fails to agree on chain height",
			nodesObservations: []map[string]*types.RequestObservation{{}}, // only one node provided observations
			expectedError:     "could not determine chain height: not enough observations to calculate chain height. Got 1, expected at least 2",
		},
		{
			name:       "not enough observations of a request to agree on request type",
			requestIDs: []string{"request_1", "request_2"},
			nodesObservations: []map[string]*types.RequestObservation{
				{
					// node1
					"request_1": &types.RequestObservation{Observation: &types.RequestObservation_EventuallyConsistent{}},
				},
				{
					// node2
					"request_1": &types.RequestObservation{Observation: &types.RequestObservation_EventuallyConsistent{}},
				},
				{
					// node3
					"request_2": &types.RequestObservation{Observation: &types.RequestObservation_EventuallyConsistent{}},
				},
			},
			expectedOutcome: &types.Outcome{ChainHeight: chainHeight},
			expectedLogs:    []string{"Could not determine request type"},
		},
		{
			name:       "fails to determine request value",
			requestIDs: []string{"request_1"},
			nodesObservations: []map[string]*types.RequestObservation{
				{
					// node1
					"request_1": &types.RequestObservation{Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value1")}},
				},
				{
					// node2
					"request_1": &types.RequestObservation{Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value2")}},
				},
				{
					// node3
					"request_1": &types.RequestObservation{Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value3")}},
				},
			},
			expectedOutcome: &types.Outcome{ChainHeight: chainHeight},
			expectedLogs:    []string{"Could not determine request value"},
		},
		{
			name:       "returns error on unsupported request type",
			requestIDs: []string{"request_1"},
			nodesObservations: []map[string]*types.RequestObservation{
				{
					// node1
					"request_1": &types.RequestObservation{},
				},
				{
					// node2
					"request_1": &types.RequestObservation{},
				},
				{
					// node3
					"request_1": &types.RequestObservation{},
				},
			},
			expectedError: "unsupported request type: REQUEST_TYPE_UNKNOWN",
		},
		{
			name:       "happy path",
			requestIDs: []string{"request_with_common_value", "request_without_common_value", "lockable_request", "request_known_to_insufficient_number_of_nodes"},
			nodesObservations: []map[string]*types.RequestObservation{
				{
					// node1
					"request_with_common_value":                     &types.RequestObservation{Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value1")}},
					"request_without_common_value":                  &types.RequestObservation{Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value1")}},
					"lockable_request":                              &types.RequestObservation{Observation: &types.RequestObservation_LockableToBlock{LockableToBlock: &emptypb.Empty{}}},
					"request_known_to_insufficient_number_of_nodes": &types.RequestObservation{Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value1")}},
				},
				{
					// node2
					"request_with_common_value":                     &types.RequestObservation{Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value1")}},
					"request_without_common_value":                  &types.RequestObservation{Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value2")}},
					"lockable_request":                              &types.RequestObservation{Observation: &types.RequestObservation_LockableToBlock{LockableToBlock: &emptypb.Empty{}}},
					"request_known_to_insufficient_number_of_nodes": &types.RequestObservation{Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value1")}},
				},
				{
					// node3
					"request_with_common_value":    &types.RequestObservation{Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value1")}},
					"request_without_common_value": &types.RequestObservation{Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value3")}},
					"lockable_request":             &types.RequestObservation{Observation: &types.RequestObservation_LockableToBlock{LockableToBlock: &emptypb.Empty{}}},
				},
			},
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
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			lggr, observed := logger.TestObserved(t, zapcore.DebugLevel)
			plugin := newReportingPlugin(Config{ReportingPluginConfig: ocr3types.ReportingPluginConfig{F: 1, N: 4}}, logger.Sugared(lggr), nil, nil)
			var rawAOs []ocrtypes.AttributedObservation
			for _, nodesObservations := range tc.nodesObservations {
				rawObservation, err := proto.Marshal(&types.Observation{ChainHeight: chainHeight, Observations: nodesObservations})
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

func TestAgreeOnRequestValue(t *testing.T) {
	const id = "request_1"
	testCases := []struct {
		name              string
		nodesObservations []attributedObservation
		expectedError     string
		expectedValue     []byte
	}{
		{
			name: "insufficient total number of observations",
			nodesObservations: []attributedObservation{
				{
					Observer: 1,
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value1")}},
						},
					},
				},
				{
					Observer: 2,
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value1")}},
						},
					},
				},
			},
			expectedError: "insufficient number of observations: expected 3, got 2",
		},
		{
			name: "insufficient number of identical observations",
			nodesObservations: []attributedObservation{
				{
					Observer: 1,
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value1")}},
						},
					},
				},
				{
					Observer: 2,
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value2")}},
						},
					},
				},
				{
					Observer: 3,
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value3")}},
						},
					},
				},
				{
					Observer: 4,
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value4")}},
						},
					},
				},
			},
			expectedError: "insufficient number of identical observations: expected 2, got 1",
		},
		{
			name: "prefer value observed by oracle with lowest id",
			nodesObservations: []attributedObservation{
				{
					Observer: 1,
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value1")}},
						},
					},
				},
				{
					Observer: 2,
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value2")}},
						},
					},
				},
				{
					Observer: 3,
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value2")}},
						},
					},
				},
				{
					Observer: 4,
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value1")}},
						},
					},
				},
			},
			expectedValue: []byte("value1"),
		},
		{
			name: "happy path",
			nodesObservations: []attributedObservation{
				{
					Observer: 1,
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("invalid_vale")}},
						},
					},
				},
				{
					Observer: 2,
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value2")}},
						},
					},
				},
				{
					Observer: 3,
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value2")}},
						},
					},
				},
				{
					Observer: 4,
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("value4")}},
						},
					},
				},
			},
			expectedValue: []byte("value2"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			plugin := newReportingPlugin(Config{ReportingPluginConfig: ocr3types.ReportingPluginConfig{F: 1, N: 4}}, logger.Sugared(logger.Test(t)), nil, nil)
			value, err := plugin.agreeOnEventuallyConsistentValue(id, tc.nodesObservations)
			if tc.expectedError == "" {
				require.NoError(t, err)
				require.Equal(t, tc.expectedValue, value)
			} else {
				require.EqualError(t, err, tc.expectedError)
			}
		})
	}
}

func TestAgreeOnRequestType(t *testing.T) {
	const id = "request_1"
	testCases := []struct {
		name              string
		nodesObservations []attributedObservation
		expectedError     string
		expectedValue     types.RequestType
	}{
		{
			name: "insufficient total number of observations",
			nodesObservations: []attributedObservation{
				{
					Observer: 1,
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: {Observation: &types.RequestObservation_EventuallyConsistent{}},
						},
					},
				},
				{
					Observer: 2,
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: {Observation: &types.RequestObservation_EventuallyConsistent{}},
						},
					},
				},
			},
			expectedError: "insufficient number of observations: expected 3, got 2",
		},
		{
			name: "insufficient number of identical observations",
			nodesObservations: []attributedObservation{
				{
					Observer: 1,
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: {Observation: &types.RequestObservation_EventuallyConsistent{}},
						},
					},
				},
				{
					Observer: 2,
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: {Observation: &types.RequestObservation_LockableToBlock{}},
						},
					},
				},
				{
					Observer: 3,
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: {Observation: &types.RequestObservation_Aggregatable{}},
						},
					},
				},
			},
			expectedError: "insufficient number of identical observations: expected 2, got 1",
		},
		{
			name: "prefer value observed by oracle with lowest id",
			nodesObservations: []attributedObservation{
				{
					Observer: 1,
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: {Observation: &types.RequestObservation_EventuallyConsistent{}},
						},
					},
				},
				{
					Observer: 2,
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: {Observation: &types.RequestObservation_LockableToBlock{}},
						},
					},
				},
				{
					Observer: 3,
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: {Observation: &types.RequestObservation_LockableToBlock{}},
						},
					},
				},
				{
					Observer: 4,
					Observation: &types.Observation{
						Observations: map[string]*types.RequestObservation{
							id: {Observation: &types.RequestObservation_EventuallyConsistent{}},
						},
					},
				},
			},
			expectedValue: types.RequestType_REQUEST_TYPE_EVENTUALLY_CONSISTENT,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			plugin := newReportingPlugin(Config{ReportingPluginConfig: ocr3types.ReportingPluginConfig{F: 1, N: 4}}, logger.Sugared(logger.Test(t)), nil, nil)
			value, err := plugin.agreeOnRequestType(id, tc.nodesObservations)
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
	testCases := []struct {
		name            string
		outcome         *types.Outcome
		expectedError   string
		expectedReports []ocr3types.ReportPlus[[]byte]
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
					},
				},
				{
					ReportWithInfo: ocr3types.ReportWithInfo[[]byte]{
						Report: mustMarshalProto(&types.RequestReport{
							RequestID: "request_2",
							Report:    &types.RequestReport_LockableToBlock{LockableToBlock: &types.ChainHeight{Latest: 15, Safe: 10, Finalized: 8}},
						}),
					},
				},
			},
		},
		{
			name: "unsupported request type",
			outcome: &types.Outcome{
				Outcomes: []*types.RequestOutcome{
					{
						RequestID: "invalid_request",
						Outcome:   nil,
					},
				},
			},
			expectedError: "unsupported request type: <nil>",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			rp := newReportingPlugin(Config{}, logger.Sugared(logger.Test(t)), nil, nil)

			reports, err := rp.Reports(t.Context(), 1, mustMarshalProto(tc.outcome))
			if tc.expectedError != "" {
				require.EqualError(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Len(t, reports, len(tc.expectedReports))
				for i := range reports {
					require.Equal(t, tc.expectedReports[i].ReportWithInfo.Report, reports[i].ReportWithInfo.Report)
				}
			}
		})
	}
}

func mustMarshalProto(v proto.Message) []byte {
	result, err := proto.Marshal(v)
	if err != nil {
		panic(err)
	}
	return result
}
