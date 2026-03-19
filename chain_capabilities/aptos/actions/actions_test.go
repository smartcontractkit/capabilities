package actions

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	typesmocks "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	p2ptypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/config"
	"github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
)

func TestNewAptos_RequiresDependencies(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	cfg := &config.Config{CREForwarderAddress: [32]byte(testForwarderAddr)}

	_, err := NewAptos(cfg, nil, nil, &testConsensusHandler{}, lggr, limits.Factory{Logger: lggr}, transmission_schedule.TransmissionScheduler{}, testChainSelector)
	require.Error(t, err)
	require.Contains(t, err.Error(), "aptos service is required")

	_, err = NewAptos(cfg, nil, typesmocks.NewAptosService(t), nil, lggr, limits.Factory{Logger: lggr}, transmission_schedule.TransmissionScheduler{}, testChainSelector)
	require.Error(t, err)
	require.Contains(t, err.Error(), "consensus handler is required")
}

func TestNewAptos_SuccessAndUnimplementedMethods(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	service := typesmocks.NewAptosService(t)
	peerID := p2ptypes.PeerID{1}
	scheduler := transmission_schedule.NewTransmissionScheduler(peerID, []p2ptypes.PeerID{peerID}, time.Second, 0, lggr)

	a, err := NewAptos(
		&config.Config{CREForwarderAddress: [32]byte(testForwarderAddr)},
		map[string]string{},
		service,
		&testConsensusHandler{},
		lggr,
		limits.Factory{Logger: lggr},
		scheduler,
		testChainSelector,
	)
	require.NoError(t, err)
	require.NotNil(t, a)
	require.Equal(t, testChainSelector, a.chainSelector)
	require.EqualValues(t, testForwarderAddr, a.forwarderAddress)

	meta := capabilities.RequestMetadata{WorkflowExecutionID: "weid", ReferenceID: "step-id"}

	_, capErr := a.AccountAPTBalance(t.Context(), meta, nil)
	require.Error(t, capErr)
	require.Contains(t, capErr.Error(), "unimplemented")

	_, capErr = a.TransactionByHash(t.Context(), meta, nil)
	require.Error(t, capErr)
	require.Contains(t, capErr.Error(), "unimplemented")

	_, capErr = a.AccountTransactions(t.Context(), meta, nil)
	require.Error(t, capErr)
	require.Contains(t, capErr.Error(), "unimplemented")

	info, infoErr := a.Info()
	require.NoError(t, infoErr)
	require.Equal(t, capabilities.CapabilityInfo{}, info)
	require.NoError(t, a.Close())
}
