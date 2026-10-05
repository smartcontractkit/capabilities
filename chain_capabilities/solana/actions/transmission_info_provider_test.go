package actions

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	soltypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/solana"
	"github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	solprimitives "github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives/solana"

	"github.com/smartcontractkit/chainlink-solana/contracts"
	lptypes "github.com/smartcontractkit/chainlink-solana/pkg/solana/logpoller/types"
)

var (
	testSigInProgress = soltypes.EventSignature(lptypes.NewEventSignatureFromName(eventReportInProgress))
	testSigProcessed  = soltypes.EventSignature(lptypes.NewEventSignatureFromName(eventReportProcessed))
)

// expectProviderFilterRegistrations mocks the log filter lifecycle the provider sets up on creation:
// the ReportInProgress filter (reverted txs included), removal of the legacy filter name and the
// ReportProcessed filter (successful txs only).
func expectProviderFilterRegistrations(svc *mocks.SolanaService, programID solana.PublicKey) {
	idlJSON := []byte(contracts.FetchForwarderIDL())
	subkeyPaths := [][]string{transmissionLogSubkeyPath, stateSubkeyPath}

	svc.EXPECT().RegisterLogTracking(mock.Anything, mock.MatchedBy(func(q soltypes.LPFilterQuery) bool {
		return q.Name == eventReportInProgress+"_"+programID.String()+"_v2" &&
			q.EventName == eventReportInProgress &&
			q.EventSig == testSigInProgress &&
			bytes.Equal(q.ContractIdlJSON, idlJSON) &&
			len(q.SubkeyPaths) == 2 &&
			slices.Equal(q.SubkeyPaths[0], subkeyPaths[0]) &&
			slices.Equal(q.SubkeyPaths[1], subkeyPaths[1]) &&
			q.IncludeReverted
	})).Return(nil).Once()

	svc.EXPECT().UnregisterLogTracking(mock.Anything, eventReportInProgress+"_"+programID.String()).
		Return(nil).Once()

	svc.EXPECT().RegisterLogTracking(mock.Anything, mock.MatchedBy(func(q soltypes.LPFilterQuery) bool {
		return q.Name == eventReportProcessed+"_"+programID.String()+"_v2" &&
			q.EventName == eventReportProcessed &&
			q.EventSig == testSigProcessed &&
			bytes.Equal(q.ContractIdlJSON, idlJSON) &&
			len(q.SubkeyPaths) == 2 &&
			slices.Equal(q.SubkeyPaths[0], subkeyPaths[0]) &&
			slices.Equal(q.SubkeyPaths[1], subkeyPaths[1]) &&
			!q.IncludeReverted
	})).Return(nil).Once()
}

func newTestProvider(t *testing.T) (*mocks.SolanaService, *OnChainTransmissionInfoProvider, solana.PublicKey, solana.PublicKey) {
	t.Helper()

	svc := mocks.NewSolanaService(t)
	programID := solana.NewWallet().PublicKey()
	forwarderState := solana.NewWallet().PublicKey()
	expectProviderFilterRegistrations(svc, programID)

	p, err := newOnChainTransmissionInfoProvider(t.Context(), programID, forwarderState, mocks.WrapSolanaService(svc))
	require.NoError(t, err)
	provider, ok := p.(*OnChainTransmissionInfoProvider)
	require.True(t, ok)

	return svc, provider, programID, forwarderState
}

// forwarderLogQueryMatcher matches QueryTrackedLogs expressions built for a forwarder event:
// event signature, forwarder program address, transmission id subkey and forwarder state subkey.
func forwarderLogQueryMatcher(sig soltypes.EventSignature, transmissionID [32]byte, programID, forwarderState solana.PublicKey) interface{} {
	return mock.MatchedBy(func(exprs []query.Expression) bool {
		if len(exprs) != 4 {
			return false
		}
		eventSig, ok := exprs[0].Primitive.(*solprimitives.EventSig)
		if !ok || eventSig.Sig != sig {
			return false
		}
		address, ok := exprs[1].Primitive.(*solprimitives.Address)
		if !ok || address.PubKey != soltypes.PublicKey(programID) {
			return false
		}
		transmissionFilter, ok := exprs[2].Primitive.(*solprimitives.EventBySubkey)
		if !ok || transmissionFilter.SubKeyIndex != 0 || len(transmissionFilter.ValueComparers) != 1 {
			return false
		}
		if !bytes.Equal(transmissionFilter.ValueComparers[0].Value, transmissionID[:]) ||
			transmissionFilter.ValueComparers[0].Operator != primitives.Eq {
			return false
		}
		stateFilter, ok := exprs[3].Primitive.(*solprimitives.EventBySubkey)
		if !ok || stateFilter.SubKeyIndex != 1 || len(stateFilter.ValueComparers) != 1 {
			return false
		}
		if !bytes.Equal(stateFilter.ValueComparers[0].Value, forwarderState.Bytes()) ||
			stateFilter.ValueComparers[0].Operator != primitives.Eq {
			return false
		}
		return true
	})
}

func testLog(sig solana.Signature, blockNumber, logIndex int64) *soltypes.Log {
	return &soltypes.Log{
		TxHash:      soltypes.Signature(sig),
		BlockNumber: blockNumber,
		LogIndex:    logIndex,
	}
}

func TestNewOnChainTransmissionInfoProvider(t *testing.T) {
	t.Parallel()

	t.Run("registers forwarder log filters", func(t *testing.T) {
		t.Parallel()
		// Filter registrations are asserted through the Once() expectations
		// and the mock's AssertExpectations cleanup.
		_, _, _, _ = newTestProvider(t)
	})

	t.Run("report in progress filter registration fails", func(t *testing.T) {
		t.Parallel()
		svc := mocks.NewSolanaService(t)
		expectedErr := errors.New("registration failed")

		svc.EXPECT().RegisterLogTracking(mock.Anything, mock.Anything).Return(expectedErr).Once()

		_, err := newOnChainTransmissionInfoProvider(t.Context(),
			solana.NewWallet().PublicKey(), solana.NewWallet().PublicKey(), mocks.WrapSolanaService(svc))
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to register ReportInProgress log filter")
	})

	t.Run("legacy filter unregistration fails", func(t *testing.T) {
		t.Parallel()
		svc := mocks.NewSolanaService(t)
		expectedErr := errors.New("unregistration failed")

		svc.EXPECT().RegisterLogTracking(mock.Anything, mock.Anything).Return(nil).Once()
		svc.EXPECT().UnregisterLogTracking(mock.Anything, mock.Anything).Return(expectedErr).Once()

		_, err := newOnChainTransmissionInfoProvider(t.Context(),
			solana.NewWallet().PublicKey(), solana.NewWallet().PublicKey(), mocks.WrapSolanaService(svc))
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to unregister legacy ReportInProgress log filter")
	})

	t.Run("report processed filter registration fails", func(t *testing.T) {
		t.Parallel()
		svc := mocks.NewSolanaService(t)
		expectedErr := errors.New("registration failed")

		svc.EXPECT().RegisterLogTracking(mock.Anything, mock.Anything).Return(nil).Once()
		svc.EXPECT().UnregisterLogTracking(mock.Anything, mock.Anything).Return(nil).Once()
		svc.EXPECT().RegisterLogTracking(mock.Anything, mock.Anything).Return(expectedErr).Once()

		_, err := newOnChainTransmissionInfoProvider(t.Context(),
			solana.NewWallet().PublicKey(), solana.NewWallet().PublicKey(), mocks.WrapSolanaService(svc))
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to register ReportProcessed log filter")
	})
}

func TestOnChainTransmissionInfoProvider_GetTransmissionInfo(t *testing.T) {
	t.Parallel()

	t.Run("no forwarder logs - not attempted", func(t *testing.T) {
		t.Parallel()
		svc, provider, programID, forwarderState := newTestProvider(t)
		transmissionID := [32]byte{1, 2, 3}

		svc.EXPECT().
			QueryTrackedLogs(mock.Anything, forwarderLogQueryMatcher(testSigInProgress, transmissionID, programID, forwarderState), mock.Anything).
			Return([]*soltypes.Log{}, nil).
			Once()
		// No ReportProcessed query is expected: the provider short-circuits.

		info, err := provider.GetTransmissionInfo(t.Context(), transmissionID)
		require.NoError(t, err)
		require.Equal(t, TransmissionInfo{State: TransmissionStateNotAttempted}, info)
	})

	t.Run("report processed - succeeded with successful tx signature", func(t *testing.T) {
		t.Parallel()
		svc, provider, programID, forwarderState := newTestProvider(t)
		transmissionID := [32]byte{9}

		revertedSig := solana.Signature{1}
		successfulSig := solana.Signature{2}

		svc.EXPECT().
			QueryTrackedLogs(mock.Anything, forwarderLogQueryMatcher(testSigInProgress, transmissionID, programID, forwarderState), mock.Anything).
			Return([]*soltypes.Log{testLog(revertedSig, 100, 0)}, nil).
			Once()
		svc.EXPECT().
			QueryTrackedLogs(mock.Anything, forwarderLogQueryMatcher(testSigProcessed, transmissionID, programID, forwarderState), mock.Anything).
			Return([]*soltypes.Log{testLog(successfulSig, 200, 0)}, nil).
			Once()

		info, err := provider.GetTransmissionInfo(t.Context(), transmissionID)
		require.NoError(t, err)
		require.Equal(t, TransmissionStateSucceeded, info.State)
		require.Equal(t, successfulSig, info.Signature)
	})

	t.Run("only reverted attempts - failed with earliest attempted signature", func(t *testing.T) {
		t.Parallel()
		svc, provider, programID, forwarderState := newTestProvider(t)
		transmissionID := [32]byte{7}

		earliestSig := solana.Signature{4}

		svc.EXPECT().
			QueryTrackedLogs(mock.Anything, forwarderLogQueryMatcher(testSigInProgress, transmissionID, programID, forwarderState), mock.Anything).
			Return([]*soltypes.Log{
				testLog(earliestSig, 200, 5),
			}, nil).
			Once()
		svc.EXPECT().
			QueryTrackedLogs(mock.Anything, forwarderLogQueryMatcher(testSigProcessed, transmissionID, programID, forwarderState), mock.Anything).
			Return([]*soltypes.Log{}, nil).
			Once()

		info, err := provider.GetTransmissionInfo(t.Context(), transmissionID)
		require.NoError(t, err)
		require.Equal(t, TransmissionStateFailed, info.State)
		require.Equal(t, earliestSig, info.Signature)
	})

	t.Run("report in progress query fails", func(t *testing.T) {
		t.Parallel()
		svc, provider, programID, forwarderState := newTestProvider(t)
		transmissionID := [32]byte{5}
		expectedErr := errors.New("rpc unavailable")

		svc.EXPECT().
			QueryTrackedLogs(mock.Anything, forwarderLogQueryMatcher(testSigInProgress, transmissionID, programID, forwarderState), mock.Anything).
			Return(nil, expectedErr).
			Once()

		_, err := provider.GetTransmissionInfo(t.Context(), transmissionID)
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to request ReportInProgress events")
	})

	t.Run("report processed query fails", func(t *testing.T) {
		t.Parallel()
		svc, provider, programID, forwarderState := newTestProvider(t)
		transmissionID := [32]byte{6}
		expectedErr := errors.New("rpc unavailable")

		svc.EXPECT().
			QueryTrackedLogs(mock.Anything, forwarderLogQueryMatcher(testSigInProgress, transmissionID, programID, forwarderState), mock.Anything).
			Return([]*soltypes.Log{testLog(solana.Signature{1}, 100, 0)}, nil).
			Once()
		svc.EXPECT().
			QueryTrackedLogs(mock.Anything, forwarderLogQueryMatcher(testSigProcessed, transmissionID, programID, forwarderState), mock.Anything).
			Return(nil, expectedErr).
			Once()

		_, err := provider.GetTransmissionInfo(t.Context(), transmissionID)
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to request ReportProcessed events")
	})
}
