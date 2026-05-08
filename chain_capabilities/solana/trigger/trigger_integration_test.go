package trigger

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/chain_capabilities/common/test"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/contracts"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/monitoring"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/utils"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	solanacappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/solana"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/sqlutil/sqltest"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	logreadtest "github.com/smartcontractkit/chainlink-solana/contracts/generated/log_read_test"
	relayer "github.com/smartcontractkit/chainlink-solana/pkg/solana"
	"github.com/smartcontractkit/chainlink-solana/pkg/solana/client"
	"github.com/smartcontractkit/chainlink-solana/pkg/solana/config"
	"github.com/smartcontractkit/chainlink-solana/pkg/solana/logpoller"
	lptypes "github.com/smartcontractkit/chainlink-solana/pkg/solana/logpoller/types"
	solanamocks "github.com/smartcontractkit/chainlink-solana/pkg/solana/mocks"
	solanatesting "github.com/smartcontractkit/chainlink-solana/pkg/solana/testing"
)

func TestSolanaLogTrigger(t *testing.T) {
	dbURL := sqltest.TestURL(t)
	db := sqltest.NewDB(t, dbURL)
	lggr := logger.Test(t)

	cfg := config.NewDefault()
	rpcURL, programID := setupValidatorAndTestContract(t)
	sc, err := client.NewClient(rpcURL, cfg, 5*time.Second, lggr)
	require.NoError(t, err)

	mc := client.NewMultiClient(func(context.Context) (client.ReaderWriter, error) {
		return sc, nil
	})

	chainID, err := mc.ChainID(t.Context())
	require.NoError(t, err)
	orm := logpoller.NewORM(chainID.String(), db, lggr)
	lp, err := logpoller.New(logger.Sugared(lggr), orm, mc, config.NewDefault(), chainID.String())
	require.NoError(t, err)

	require.NoError(t, lp.Start(t.Context()))

	triggerStore := NewSolanaLogTriggerStore()

	chain := newMockChain(t, lp, sc)
	relayer := relayer.NewRelayer(lggr, chain, nil)

	triggerSvc, err := NewLogTriggerService(LogTriggerServiceOpts{
		SolanaService:                   relayer,
		Logger:                          lggr,
		Triggers:                        triggerStore,
		LogTriggerPollInterval:          1 * time.Second,
		LogTriggerSendChannelBufferSize: 100,
		Retention:                       24 * time.Hour,
		MaxLogsKept:                     1000,
		LimitsFactory:                   limits.Factory{Logger: lggr},
		BeholderProcessor:               test.NopBeholderProcessor{},
		MessageBuilder:                  monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
	})
	require.NoError(t, err)

	require.NoError(t, triggerSvc.Start(t.Context()))

	idl, err := loadContractIDLJson()
	require.NoError(t, err)

	address, err := solana.PublicKeyFromBase58(programID)
	require.NoError(t, err)

	fmt.Println("Contract Address: ", address)

	filterRequest := &solanacappb.FilterLogTriggerRequest{
		Name:            "test_trigger",
		Address:         address[:],
		EventName:       "TestEvent",
		ContractIdlJson: []byte(idl),
	}

	meta := capabilities.RequestMetadata{
		WorkflowID:    "integration-test-workflow",
		WorkflowOwner: "integration-test-owner",
	}
	logCh, capErr := triggerSvc.RegisterLogTrigger(t.Context(), "test_trigger_id", meta, filterRequest)
	require.NoError(t, capErr)
	require.NotNil(t, logCh)

	err = triggerSvc.Ready()
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	t.Logf("Emitting test event from program: %s", programID)

	signerKeypair, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	signer := signerKeypair

	t.Logf("Funding signer account: %s", signer.PublicKey())

	rpcClient := rpc.New(rpcURL)
	utils.FundAccounts(t, []solana.PrivateKey{signer}, rpcClient)

	time.Sleep(1 * time.Second)

	_, err = emitLogReadTestEvent(t, sc, programID, signer, 42)
	require.NoError(t, err, "emit test event should succeed with funded account")

	select {
	case response := <-logCh:
		// Verify the received event
		require.NotNil(t, response.Trigger)
		require.Equal(t, programID, solana.PublicKey(response.Trigger.Address).String())
		require.Contains(t, string(response.Trigger.Data), "Hello, World!")
		t.Logf("Successfully received event: %+v", response.Trigger)
	case <-time.After(30 * time.Second):
		t.Fatal("Timeout waiting for event - transaction was sent but event was not received by trigger")
	}

	capErr = triggerSvc.UnregisterLogTrigger(t.Context(), "test_trigger_id", meta, filterRequest)
	require.NoError(t, capErr)

	// Clean up
	require.NoError(t, triggerSvc.Close())
	_ = lp.Close()
}

func TestSolanaLogTriggerWithSubkeyPaths(t *testing.T) {
	dbURL := sqltest.TestURL(t)
	db := sqltest.NewDB(t, dbURL)
	lggr := logger.Test(t)

	cfg := config.NewDefault()
	rpcURL, programID := setupValidatorAndTestContract(t)
	sc, err := client.NewClient(rpcURL, cfg, 5*time.Second, lggr)
	require.NoError(t, err)

	mc := client.NewMultiClient(func(context.Context) (client.ReaderWriter, error) {
		return sc, nil
	})

	chainID, err := mc.ChainID(t.Context())
	require.NoError(t, err)
	orm := logpoller.NewORM(chainID.String(), db, lggr)
	lp, err := logpoller.New(logger.Sugared(lggr), orm, mc, config.NewDefault(), chainID.String())
	require.NoError(t, err)

	require.NoError(t, lp.Start(t.Context()))

	triggerStore := NewSolanaLogTriggerStore()

	chain := newMockChain(t, lp, sc)
	relayer := relayer.NewRelayer(lggr, chain, nil)

	triggerSvc, err := NewLogTriggerService(LogTriggerServiceOpts{
		SolanaService:                   relayer,
		Logger:                          lggr,
		Triggers:                        triggerStore,
		LogTriggerPollInterval:          1 * time.Second,
		LogTriggerSendChannelBufferSize: 100,
		Retention:                       24 * time.Hour,
		MaxLogsKept:                     1000,
		LimitsFactory:                   limits.Factory{Logger: lggr},
		BeholderProcessor:               test.NopBeholderProcessor{},
		MessageBuilder:                  monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
	})
	require.NoError(t, err)

	require.NoError(t, triggerSvc.Start(t.Context()))

	idl, err := loadContractIDLJson()
	require.NoError(t, err)

	address, err := solana.PublicKeyFromBase58(programID)
	require.NoError(t, err)

	signerKeypair, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	signer := signerKeypair

	rpcClient := rpc.New(rpcURL)
	utils.FundAccounts(t, []solana.PrivateKey{signer}, rpcClient)

	time.Sleep(1 * time.Second)

	// Filter for u64_value >= 50
	valueThreshold := uint64(50)
	valueBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(valueBytes, valueThreshold)

	filterRequest := &solanacappb.FilterLogTriggerRequest{
		Name:            "test_trigger_subkey",
		Address:         address[:],
		EventName:       "TestEvent",
		ContractIdlJson: []byte(idl),
		Subkeys: []*solanacappb.SubkeyConfig{
			{Path: []string{"StrVal"}},
			{
				Path: []string{"U64Value"},
				Comparers: []*solanacappb.ValueComparator{
					{
						Value:    valueBytes,
						Operator: solanacappb.ComparisonOperator_COMPARISON_OPERATOR_GTE,
					},
				},
			},
		},
	}

	meta := capabilities.RequestMetadata{
		WorkflowID:    "integration-test-workflow",
		WorkflowOwner: "integration-test-owner",
	}
	logCh, capErr := triggerSvc.RegisterLogTrigger(t.Context(), "test_trigger_subkey_id", meta, filterRequest)
	require.NoError(t, capErr)
	require.NotNil(t, logCh)

	err = triggerSvc.Ready()
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	// Emit event with value below threshold (should be filtered out)
	_, err = emitLogReadTestEvent(t, sc, programID, signer, 30)
	require.NoError(t, err)

	time.Sleep(2 * time.Second)

	// Emit event with value above threshold (should pass filter)
	_, err = emitLogReadTestEvent(t, sc, programID, signer, 100)
	require.NoError(t, err)

	select {
	case response := <-logCh:
		require.NotNil(t, response.Trigger)
		require.Equal(t, programID, solana.PublicKey(response.Trigger.Address).String())
		require.Contains(t, string(response.Trigger.Data), "Hello, World!")
	case <-time.After(30 * time.Second):
		t.Fatal("Timeout waiting for filtered event")
	}

	select {
	case unexpectedLog := <-logCh:
		t.Fatalf("Received unexpected event that should have been filtered: %+v", unexpectedLog)
	case <-time.After(3 * time.Second):
	}

	capErr = triggerSvc.UnregisterLogTrigger(t.Context(), "test_trigger_subkey_id", meta, filterRequest)
	require.NoError(t, capErr)

	require.NoError(t, triggerSvc.Close())
	_ = lp.Close()
}

func TestSolanaLogTrigger_UnhappyPaths(t *testing.T) {
	dbURL := sqltest.TestURL(t)
	db := sqltest.NewDB(t, dbURL)
	lggr := logger.Test(t)

	cfg := config.NewDefault()
	rpcURL, programID := setupValidatorAndTestContract(t)
	sc, err := client.NewClient(rpcURL, cfg, 5*time.Second, lggr)
	require.NoError(t, err)

	mc := client.NewMultiClient(func(context.Context) (client.ReaderWriter, error) {
		return sc, nil
	})

	chainID, err := mc.ChainID(t.Context())
	require.NoError(t, err)
	orm := logpoller.NewORM(chainID.String(), db, lggr)
	lp, err := logpoller.New(logger.Sugared(lggr), orm, mc, config.NewDefault(), chainID.String())
	require.NoError(t, err)

	require.NoError(t, lp.Start(t.Context()))

	triggerStore := NewSolanaLogTriggerStore()

	chain := newMockChain(t, lp, sc)
	relayer := relayer.NewRelayer(lggr, chain, nil)

	triggerSvc, err := NewLogTriggerService(LogTriggerServiceOpts{
		SolanaService:                   relayer,
		Logger:                          lggr,
		Triggers:                        triggerStore,
		LogTriggerPollInterval:          1 * time.Second,
		LogTriggerSendChannelBufferSize: 100,
		Retention:                       24 * time.Hour,
		MaxLogsKept:                     1000,
		LimitsFactory:                   limits.Factory{Logger: lggr},
		BeholderProcessor:               test.NopBeholderProcessor{},
		MessageBuilder:                  monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
	})
	require.NoError(t, err)

	require.NoError(t, triggerSvc.Start(t.Context()))
	defer func() {
		require.NoError(t, triggerSvc.Close())
		_ = lp.Close()
	}()

	idl, err := loadContractIDLJson()
	require.NoError(t, err)

	address, err := solana.PublicKeyFromBase58(programID)
	require.NoError(t, err)

	meta := capabilities.RequestMetadata{
		WorkflowID:    "integration-test-workflow",
		WorkflowOwner: "integration-test-owner",
	}

	t.Run("empty trigger ID", func(t *testing.T) {
		filterRequest := &solanacappb.FilterLogTriggerRequest{
			Name:            "test_trigger",
			Address:         address[:],
			EventName:       "TestEvent",
			ContractIdlJson: []byte(idl),
		}

		_, capErr := triggerSvc.RegisterLogTrigger(t.Context(), "", meta, filterRequest)
		require.Error(t, capErr)
		require.Contains(t, capErr.Error(), "no triggerID provided")
	})

	t.Run("duplicate trigger registration", func(t *testing.T) {
		filterRequest := &solanacappb.FilterLogTriggerRequest{
			Name:            "test_trigger_dup",
			Address:         address[:],
			EventName:       "TestEvent",
			ContractIdlJson: []byte(idl),
		}

		// First registration should succeed
		logCh, capErr := triggerSvc.RegisterLogTrigger(t.Context(), "duplicate_test_id", meta, filterRequest)
		require.NoError(t, capErr)
		require.NotNil(t, logCh)

		time.Sleep(50 * time.Millisecond)

		// Second registration with same ID should fail
		_, capErr = triggerSvc.RegisterLogTrigger(t.Context(), "duplicate_test_id", meta, filterRequest)
		require.Error(t, capErr)
		require.Contains(t, capErr.Error(), "is already registered")

		// Cleanup
		capErr = triggerSvc.UnregisterLogTrigger(t.Context(), "duplicate_test_id", meta, filterRequest)
		require.NoError(t, capErr)
	})

	t.Run("unregister non-existent trigger", func(t *testing.T) {
		filterRequest := &solanacappb.FilterLogTriggerRequest{}
		capErr := triggerSvc.UnregisterLogTrigger(t.Context(), "non_existent_trigger_id", meta, filterRequest)
		require.Error(t, capErr)
		require.Contains(t, capErr.Error(), "no active trigger found")
	})

	t.Run("unregister with empty trigger ID", func(t *testing.T) {
		filterRequest := &solanacappb.FilterLogTriggerRequest{}
		capErr := triggerSvc.UnregisterLogTrigger(t.Context(), "", meta, filterRequest)
		require.Error(t, capErr)
		require.Contains(t, capErr.Error(), "no triggerID provided")
	})

	t.Run("register with invalid address length", func(t *testing.T) {
		filterRequest := &solanacappb.FilterLogTriggerRequest{
			Name:            "test_trigger_invalid_addr",
			Address:         []byte("too_short"), // Invalid address length
			EventName:       "TestEvent",
			ContractIdlJson: []byte(idl),
		}

		// Registration may succeed but with zero-valued address
		logCh, capErr := triggerSvc.RegisterLogTrigger(t.Context(), "invalid_addr_test", meta, filterRequest)
		if capErr == nil {
			require.NotNil(t, logCh)
			// Cleanup
			_ = triggerSvc.UnregisterLogTrigger(t.Context(), "invalid_addr_test", meta, filterRequest)
		}
		// Either error or graceful handling is acceptable
	})

	t.Run("register with empty event IDL", func(t *testing.T) {
		filterRequest := &solanacappb.FilterLogTriggerRequest{
			Name:            "test_trigger_empty_idl",
			Address:         address[:],
			EventName:       "TestEvent",
			ContractIdlJson: []byte{}, // Empty IDL
		}

		logCh, capErr := triggerSvc.RegisterLogTrigger(t.Context(), "empty_idl_test", meta, filterRequest)
		if capErr == nil {
			require.NotNil(t, logCh)
			// Cleanup
			_ = triggerSvc.UnregisterLogTrigger(t.Context(), "empty_idl_test", meta, filterRequest)
		}
		// Either error or graceful handling is acceptable
	})

	t.Run("register with invalid event signature length", func(t *testing.T) {
		filterRequest := &solanacappb.FilterLogTriggerRequest{
			Name:            "test_trigger_invalid_sig",
			Address:         address[:],
			EventName:       "TestEvent",
			ContractIdlJson: []byte(idl),
		}

		logCh, capErr := triggerSvc.RegisterLogTrigger(t.Context(), "invalid_sig_test", meta, filterRequest)
		if capErr == nil {
			require.NotNil(t, logCh)
			// Cleanup
			_ = triggerSvc.UnregisterLogTrigger(t.Context(), "invalid_sig_test", meta, filterRequest)
		}
		// Either error or graceful handling is acceptable
	})

	t.Run("double unregister same trigger", func(t *testing.T) {
		filterRequest := &solanacappb.FilterLogTriggerRequest{
			Name:            "test_trigger_double_unreg",
			Address:         address[:],
			EventName:       "TestEvent",
			ContractIdlJson: []byte(idl),
		}

		// Register
		logCh, capErr := triggerSvc.RegisterLogTrigger(t.Context(), "double_unreg_test", meta, filterRequest)
		require.NoError(t, capErr)
		require.NotNil(t, logCh)

		time.Sleep(50 * time.Millisecond)

		// First unregister should succeed
		capErr = triggerSvc.UnregisterLogTrigger(t.Context(), "double_unreg_test", meta, filterRequest)
		require.NoError(t, capErr)

		// Second unregister should fail
		capErr = triggerSvc.UnregisterLogTrigger(t.Context(), "double_unreg_test", meta, filterRequest)
		require.Error(t, capErr)
		require.Contains(t, capErr.Error(), "no active trigger found")
	})
}

func TestSolanaLogTrigger_NoEventsReceived(t *testing.T) {
	dbURL := sqltest.TestURL(t)
	db := sqltest.NewDB(t, dbURL)
	lggr := logger.Test(t)

	cfg := config.NewDefault()
	rpcURL, programID := setupValidatorAndTestContract(t)
	sc, err := client.NewClient(rpcURL, cfg, 5*time.Second, lggr)
	require.NoError(t, err)

	mc := client.NewMultiClient(func(context.Context) (client.ReaderWriter, error) {
		return sc, nil
	})

	chainID, err := mc.ChainID(t.Context())
	require.NoError(t, err)
	orm := logpoller.NewORM(chainID.String(), db, lggr)
	lp, err := logpoller.New(logger.Sugared(lggr), orm, mc, config.NewDefault(), chainID.String())
	require.NoError(t, err)

	require.NoError(t, lp.Start(t.Context()))

	triggerStore := NewSolanaLogTriggerStore()

	chain := newMockChain(t, lp, sc)
	relayer := relayer.NewRelayer(lggr, chain, nil)

	triggerSvc, err := NewLogTriggerService(LogTriggerServiceOpts{
		SolanaService:                   relayer,
		Logger:                          lggr,
		Triggers:                        triggerStore,
		LogTriggerPollInterval:          500 * time.Millisecond,
		LogTriggerSendChannelBufferSize: 100,
		Retention:                       24 * time.Hour,
		MaxLogsKept:                     1000,
		BeholderProcessor:               test.NopBeholderProcessor{},
		MessageBuilder:                  monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
	})
	require.NoError(t, err)

	require.NoError(t, triggerSvc.Start(t.Context()))
	defer func() {
		require.NoError(t, triggerSvc.Close())
		_ = lp.Close()
	}()

	address, err := solana.PublicKeyFromBase58(programID)
	require.NoError(t, err)

	idl, err := loadContractIDLJson()
	require.NoError(t, err)

	// Register for a non-existent event name
	filterRequest := &solanacappb.FilterLogTriggerRequest{
		Name:            "test_trigger_no_events",
		Address:         address[:],
		EventName:       "TestEvent",
		ContractIdlJson: []byte(idl),
	}

	meta := capabilities.RequestMetadata{
		WorkflowID:    "integration-test-workflow",
		WorkflowOwner: "integration-test-owner",
	}
	logCh, capErr := triggerSvc.RegisterLogTrigger(t.Context(), "no_events_test", meta, filterRequest)
	require.NoError(t, capErr)
	require.NotNil(t, logCh)

	// Wait for a bit - should not receive any events
	select {
	case log := <-logCh:
		t.Fatalf("Should not have received any events, got: %+v", log)
	case <-time.After(3 * time.Second):
		// Expected - no events received
	}

	capErr = triggerSvc.UnregisterLogTrigger(t.Context(), "no_events_test", meta, filterRequest)
	require.NoError(t, capErr)
}

func TestSolanaLogTrigger_CPIEvent(t *testing.T) {
	dbURL := sqltest.TestURL(t)
	db := sqltest.NewDB(t, dbURL)
	lggr := logger.Test(t)

	cfg := config.NewDefault()
	rpcURL, programID := setupValidatorWithLocalContract(t)
	sc, err := client.NewClient(rpcURL, cfg, 5*time.Second, lggr)
	require.NoError(t, err)

	mc := client.NewMultiClient(func(context.Context) (client.ReaderWriter, error) {
		return sc, nil
	})

	chainID, err := mc.ChainID(t.Context())
	require.NoError(t, err)
	orm := logpoller.NewORM(chainID.String(), db, lggr)
	lp, err := logpoller.New(logger.Sugared(lggr), orm, mc, config.NewDefault(), chainID.String())
	require.NoError(t, err)

	require.NoError(t, lp.Start(t.Context()))

	triggerStore := NewSolanaLogTriggerStore()

	chain := newMockChain(t, lp, sc)
	rel := relayer.NewRelayer(lggr, chain, nil)

	triggerSvc, err := NewLogTriggerService(LogTriggerServiceOpts{
		SolanaService:                   rel,
		Logger:                          lggr,
		Triggers:                        triggerStore,
		LogTriggerPollInterval:          1 * time.Second,
		LogTriggerSendChannelBufferSize: 100,
		Retention:                       24 * time.Hour,
		MaxLogsKept:                     1000,
		LimitsFactory:                   limits.Factory{Logger: lggr},
		BeholderProcessor:               test.NopBeholderProcessor{},
		MessageBuilder:                  monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
	})
	require.NoError(t, err)

	require.NoError(t, triggerSvc.Start(t.Context()))

	contractIDL, err := contracts.LoadLogReadTestIDL()
	require.NoError(t, err)

	address, err := solana.PublicKeyFromBase58(programID)
	require.NoError(t, err)

	filterRequest := &solanacappb.FilterLogTriggerRequest{
		Name:            "test_trigger_cpi",
		Address:         address[:],
		EventName:       "TestEvent",
		ContractIdlJson: []byte(contractIDL),
		CpiFilterConfig: &solanacappb.CPIFilterConfig{
			DestAddress: address[:],
			MethodName:  []byte(lptypes.AnchorCPIMethodName),
		},
	}

	meta := capabilities.RequestMetadata{
		WorkflowID:    "integration-test-workflow-cpi",
		WorkflowOwner: "integration-test-owner",
	}
	logCh, capErr := triggerSvc.RegisterLogTrigger(t.Context(), "cpi_test", meta, filterRequest)
	require.NoError(t, capErr)
	require.NotNil(t, logCh)

	err = triggerSvc.Ready()
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	signerKeypair, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	signer := signerKeypair

	rpcClient := rpc.New(rpcURL)
	utils.FundAccounts(t, []solana.PrivateKey{signer}, rpcClient)

	time.Sleep(1 * time.Second)

	t.Logf("Emitting CPI event from program: %s", programID)
	_, err = emitLogReadTestCPIEvent(t, sc, programID, signer, 99)
	require.NoError(t, err, "emit CPI test event should succeed with funded account")

	select {
	case response := <-logCh:
		require.NotNil(t, response.Trigger)
		require.Equal(t, programID, solana.PublicKey(response.Trigger.Address).String())
		require.Contains(t, string(response.Trigger.Data), "Hello, CPI!")
		t.Logf("Successfully received CPI event: %+v", response.Trigger)
	case <-time.After(30 * time.Second):
		t.Fatal("Timeout waiting for CPI event")
	}

	capErr = triggerSvc.UnregisterLogTrigger(t.Context(), "cpi_test", meta, filterRequest)
	require.NoError(t, capErr)

	require.NoError(t, triggerSvc.Close())
	_ = lp.Close()
}

func TestSolanaLogTrigger_FilterExcludesAllEvents(t *testing.T) {
	dbURL := sqltest.TestURL(t)
	db := sqltest.NewDB(t, dbURL)
	lggr := logger.Test(t)

	cfg := config.NewDefault()
	rpcURL, programID := setupValidatorAndTestContract(t)
	sc, err := client.NewClient(rpcURL, cfg, 5*time.Second, lggr)
	require.NoError(t, err)

	mc := client.NewMultiClient(func(context.Context) (client.ReaderWriter, error) {
		return sc, nil
	})

	chainID, err := mc.ChainID(t.Context())
	require.NoError(t, err)
	orm := logpoller.NewORM(chainID.String(), db, lggr)
	lp, err := logpoller.New(logger.Sugared(lggr), orm, mc, config.NewDefault(), chainID.String())
	require.NoError(t, err)

	require.NoError(t, lp.Start(t.Context()))

	triggerStore := NewSolanaLogTriggerStore()

	chain := newMockChain(t, lp, sc)
	relayer := relayer.NewRelayer(lggr, chain, nil)

	triggerSvc, err := NewLogTriggerService(LogTriggerServiceOpts{
		SolanaService:                   relayer,
		Logger:                          lggr,
		Triggers:                        triggerStore,
		LogTriggerPollInterval:          500 * time.Millisecond,
		LogTriggerSendChannelBufferSize: 100,
		Retention:                       24 * time.Hour,
		MaxLogsKept:                     1000,
		BeholderProcessor:               test.NopBeholderProcessor{},
		MessageBuilder:                  monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
	})
	require.NoError(t, err)

	require.NoError(t, triggerSvc.Start(t.Context()))
	defer func() {
		require.NoError(t, triggerSvc.Close())
		_ = lp.Close()
	}()

	idl, err := loadContractIDLJson()
	require.NoError(t, err)

	address, err := solana.PublicKeyFromBase58(programID)
	require.NoError(t, err)

	signerKeypair, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	signer := signerKeypair

	rpcClient := rpc.New(rpcURL)
	utils.FundAccounts(t, []solana.PrivateKey{signer}, rpcClient)

	time.Sleep(1 * time.Second)

	// Create a filter that will exclude all events (u64_value must be > 999999999)
	impossibleValueBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(impossibleValueBytes, 999999999)

	filterRequest := &solanacappb.FilterLogTriggerRequest{
		Name:            "test_trigger_filter_all",
		Address:         address[:],
		EventName:       "TestEvent",
		ContractIdlJson: []byte(idl),
		Subkeys: []*solanacappb.SubkeyConfig{
			{
				Path: []string{"U64Value"},
				Comparers: []*solanacappb.ValueComparator{
					{
						Value:    impossibleValueBytes,
						Operator: solanacappb.ComparisonOperator_COMPARISON_OPERATOR_GT,
					},
				},
			},
		},
	}

	meta := capabilities.RequestMetadata{
		WorkflowID:    "integration-test-workflow",
		WorkflowOwner: "integration-test-owner",
	}
	logCh, capErr := triggerSvc.RegisterLogTrigger(t.Context(), "filter_all_test", meta, filterRequest)
	require.NoError(t, capErr)
	require.NotNil(t, logCh)

	time.Sleep(100 * time.Millisecond)

	// Emit an event with value that doesn't match the filter
	_, err = emitLogReadTestEvent(t, sc, programID, signer, 12345)
	require.NoError(t, err)

	// Should not receive any events due to filter
	select {
	case log := <-logCh:
		t.Fatalf("Should not have received filtered event, got: %+v", log)
	case <-time.After(5 * time.Second):
		// Expected - event was filtered out
	}

	capErr = triggerSvc.UnregisterLogTrigger(t.Context(), "filter_all_test", meta, filterRequest)
	require.NoError(t, capErr)
}

const logReadTestProgramID = "J1zQwrBNBngz26jRPNWsUSZMHJwBwpkoDitXRV95LdK4"

// defaultLogReadTestSoPath is the path to the locally built log_read_test.so when
// chainlink-solana is a sibling of capabilities (e.g. repos/{capabilities,chainlink-solana}).
const defaultLogReadTestSoPath = "/Users/silaslenihan/Desktop/repos/chainlink-solana/contracts/target/deploy/log_read_test.so"

func setupValidatorAndTestContract(t *testing.T) (string, string) {
	t.Helper()
	programPath := downloadLogReadTestProgram(t)
	return setupValidatorWithProgram(t, programPath)
}

func setupValidatorWithLocalContract(t *testing.T) (string, string) {
	t.Helper()
	programPath := os.Getenv("LOG_READ_TEST_SO_PATH")
	if programPath == "" {
		programPath = defaultLogReadTestSoPath
	}
	programPath = filepath.Clean(programPath)
	require.FileExists(t, programPath, "log_read_test.so not found at %s (build with: cd chainlink-solana && make build_contracts)", programPath)
	return setupValidatorWithProgram(t, programPath)
}

func setupValidatorWithProgram(t *testing.T, programPath string) (string, string) {
	t.Helper()
	flags := []string{
		"--warp-slot", "42",
		"--upgradeable-program", logReadTestProgramID, programPath, "11111111111111111111111111111112",
	}
	rpcURL, _ := solanatesting.SetupLocalSolNodeWithFlags(t, flags...)
	return rpcURL, logReadTestProgramID
}

// downloadLogReadTestProgram downloads the log_read_test.so artifact from
// chainlink-solana GitHub releases into a cached temp directory.
// Set SOLANA_ARTIFACTS_SHA to override the default release SHA.
// Mirrors chainlink/deployment/utils/solutils/artifacts.go
func downloadLogReadTestProgram(t *testing.T) string {
	t.Helper()

	// Temporary: prefer local test binary if present (until solana-artifacts release is ready).
	_, thisFile, _, _ := runtime.Caller(0)
	localPath := filepath.Join(filepath.Dir(thisFile), "test", "log_read_test.so")
	if _, err := os.Stat(localPath); err == nil {
		t.Logf("Using local log_read_test.so at %s", localPath)
		return localPath
	}

	sha := os.Getenv("SOLANA_ARTIFACTS_SHA")
	if sha == "" {
		sha = "81b124e1cab6"
	}
	cacheDir := filepath.Join(os.TempDir(), "chainlink-solana-artifacts-"+sha)
	programPath := filepath.Join(cacheDir, "log_read_test.so")

	if _, err := os.Stat(programPath); err == nil {
		t.Logf("Using cached log_read_test.so at %s", programPath)
		return programPath
	}

	require.NoError(t, os.MkdirAll(cacheDir, 0o755))

	err := downloadChainlinkSolanaProgramArtifacts(t.Context(), cacheDir, sha)
	require.NoError(t, err, "failed to download chainlink-solana program artifacts")

	require.FileExists(t, programPath, "log_read_test.so not found in downloaded artifacts")
	t.Logf("Cached log_read_test.so at %s", programPath)
	return programPath
}

func downloadChainlinkSolanaProgramArtifacts(ctx context.Context, targetDir string, sha string) error {
	const (
		owner = "smartcontractkit"
		repo  = "chainlink-solana"
		name  = "artifacts.tar.gz"
	)

	tag := "solana-artifacts-localtest-" + sha
	url := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s", owner, repo, tag, name)

	return downloadProgramArtifacts(ctx, url, targetDir)
}

func downloadProgramArtifacts(ctx context.Context, url string, targetDir string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	res, err := (&http.Client{}).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status %d - could not download tar.gz release artifact (url = '%s')", res.StatusCode, url)
	}

	gzipReader, err := gzip.NewReader(res.Body)
	if err != nil {
		return err
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)

	const (
		maxFiles     = 1000
		maxTotalSize = 500 * 1024 * 1024 // 500MB
	)
	var (
		fileCount int
		totalSize int64
	)

	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return err
		}

		if header.Typeflag != tar.TypeReg {
			continue
		}

		fileCount++
		if fileCount > maxFiles {
			return fmt.Errorf("archive contains too many files (limit: %d)", maxFiles)
		}

		if totalSize+header.Size > maxTotalSize {
			return fmt.Errorf("archive total size exceeds limit (limit: %d bytes)", maxTotalSize)
		}

		outPath := filepath.Join(targetDir, filepath.Base(header.Name))
		if err := os.MkdirAll(filepath.Dir(outPath), os.ModePerm); err != nil {
			return err
		}

		outFile, err := os.Create(outPath)
		if err != nil {
			return err
		}

		const maxFileSize = 100 * 1024 * 1024 // 100MB
		limitedReader := io.LimitReader(tarReader, maxFileSize)
		bytesWritten, err := io.Copy(outFile, limitedReader)
		if err != nil {
			outFile.Close()
			return err
		}

		totalSize += bytesWritten

		outFile.Close()
	}

	return nil
}

func emitLogReadTestEvent(t *testing.T, client *client.Client, programID string, signer solana.PrivateKey, value uint64) (solana.Signature, error) {
	instruction, err := logreadtest.NewCreateLogInstruction(
		value,
		signer.PublicKey(),
		solana.SystemProgramID,
	)
	if err != nil {
		return solana.Signature{}, fmt.Errorf("failed to build instruction: %w", err)
	}

	return sendInstruction(t, client, programID, signer, instruction, value)
}

func emitLogReadTestCPIEvent(t *testing.T, client *client.Client, programID string, signer solana.PrivateKey, value uint64) (solana.Signature, error) {
	programPubkey := solana.MustPublicKeyFromBase58(programID)

	eventAuthority, _, err := solana.FindProgramAddress(
		[][]byte{[]byte("__event_authority")},
		programPubkey,
	)
	if err != nil {
		return solana.Signature{}, fmt.Errorf("failed to derive event authority PDA: %w", err)
	}

	instruction, err := logreadtest.NewCreateLogCpiInstruction(
		value,
		signer.PublicKey(),
		solana.SystemProgramID,
		eventAuthority,
		programPubkey,
	)
	if err != nil {
		return solana.Signature{}, fmt.Errorf("failed to build CPI instruction: %w", err)
	}

	return sendInstruction(t, client, programID, signer, instruction, value)
}

func sendInstruction(t *testing.T, client *client.Client, programID string, signer solana.PrivateKey, instruction solana.Instruction, value uint64) (solana.Signature, error) {
	blockhash, err := client.LatestBlockhash(context.Background())
	if err != nil {
		return solana.Signature{}, fmt.Errorf("failed to get recent blockhash: %w", err)
	}

	tx, err := solana.NewTransaction(
		[]solana.Instruction{instruction},
		blockhash.Value.Blockhash,
		solana.TransactionPayer(signer.PublicKey()),
	)
	if err != nil {
		return solana.Signature{}, fmt.Errorf("failed to create transaction: %w", err)
	}

	_, err = tx.Sign(func(key solana.PublicKey) *solana.PrivateKey {
		if key.Equals(signer.PublicKey()) {
			return &signer
		}
		return nil
	})
	if err != nil {
		return solana.Signature{}, fmt.Errorf("failed to sign transaction: %w", err)
	}

	sig, err := client.SendTx(context.Background(), tx)
	if err != nil {
		return solana.Signature{}, fmt.Errorf("failed to send transaction: %w", err)
	}

	t.Logf("Sent transaction to emit log-read-test event (value=%d) from program %s, signature: %s", value, programID, sig.String())

	time.Sleep(2 * time.Second)

	return sig, nil
}

// loadContractIDLFromFile loads the contract IDL from the Anchor IDL JSON file
func loadContractIDLJson() (string, error) {
	idl, err := contracts.LoadLogReadTestIDL()
	if err != nil {
		return "", fmt.Errorf("unexpected error: invalid LogReadTest IDL, error: %w", err)
	}
	return idl, nil
}

func newMockChain(t *testing.T, lp *logpoller.Service, reader *client.Client) relayer.Chain {
	mockChain := solanamocks.NewChain(t)
	mockChain.EXPECT().LogPoller().Return(lp)
	mockChain.EXPECT().Reader().Return(reader, nil)
	return mockChain
}
