package actions

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"

	"github.com/gagliardetto/solana-go"

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"

	solcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/solana"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	soltypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/solana"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"

	ocr3types "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	ks_forwarder "github.com/smartcontractkit/chainlink-solana/contracts/generated/keystone_forwarder"
)

type forwarderClient struct {
	types.SolanaService
	lggr               logger.Logger
	forwarderProgramID solana.PublicKey
	forwarderState     solana.PublicKey
	transmitter        solana.PublicKey
}

func newForwarderClient(solService types.SolanaService, lggr logger.Logger, forwarderProgramID, forwarderState, transmitter solana.PublicKey) CREForwarderClient {
	ks_forwarder.ProgramID = forwarderProgramID
	return &forwarderClient{
		lggr:               lggr,
		SolanaService:      solService,
		forwarderProgramID: forwarderProgramID,
		forwarderState:     forwarderState,
		transmitter:        transmitter,
	}
}

func (fc *forwarderClient) InvokeOnReport(ctx context.Context, receiver solana.PublicKey, meta []*solcap.AccountMeta,
	report *sdk.ReportResponse, gasConfig *solcap.ComputeConfig) (*soltypes.SubmitTransactionReply, error) {
	if len(meta) < 2 {
		return nil, fmt.Errorf("expected accounts meta length > 2, got: %d", len(meta))
	}
	reportMetadata, _, err := ocr3types.Decode(report.RawReport)
	if err != nil {
		return nil, fmt.Errorf("failed to decode report metadata: %w", err)
	}

	var configPDA solana.PublicKey
	configPDA, err = capcommon.WithQuickRetry(ctx, fc.lggr, func(ctx context.Context) (solana.PublicKey, error) {
		return fc.getOracleConfigPDA(ctx, reportMetadata.DONID, reportMetadata.DONConfigVersion)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get oracle config PDA: %w", err)
	}

	transmisisonID, err := extractTransmissionID(receiver, report)
	if err != nil {
		return nil, fmt.Errorf("failed to extract transmissionID: %w", err)
	}
	executionState, err := fc.deriveExecutionState(transmisisonID)
	if err != nil {
		return nil, fmt.Errorf("failed to derive execution state: %w", err)
	}
	authority, err := fc.deriveForwarderAuthority(receiver)
	if err != nil {
		return nil, fmt.Errorf("failed to derive forwarder authority: %w", err)
	}

	ix, err := ks_forwarder.NewReportInstruction(
		toPayload(report),
		fc.forwarderState,
		configPDA,
		fc.transmitter,
		authority,
		executionState,
		receiver,
		solana.SystemProgramID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build report instruction: %w", err)
	}

	// meta[0] - forwarderState, meta[1] - executionState are already included in the instruction
	converted, convErr := convertMetaPB(meta)
	if convErr != nil {
		return nil, fmt.Errorf("invalid remaining account metas: %w", convErr)
	}
	genericIX, ok := ix.(*solana.GenericInstruction)
	if !ok {
		return nil, fmt.Errorf("expected *solana.GenericInstruction from NewReportInstruction, got %T", ix)
	}
	for _, acc := range converted[2:] {
		genericIX.AccountValues = append(genericIX.AccountValues, acc)
	}

	// we can encode with empty block hash here, it will be updated with recent blockhash later
	tx, err := solana.NewTransaction([]solana.Instruction{ix}, solana.Hash{}, solana.TransactionPayer(fc.transmitter))
	if err != nil {
		return nil, fmt.Errorf("failed to create report transaction: %w", err)
	}

	encodedTX, bErr := tx.ToBase64()
	if bErr != nil {
		return nil, fmt.Errorf("failed to encode tx to string: %w", bErr)
	}

	var resolvedComputeConfig *soltypes.ComputeConfig
	if gasConfig != nil {
		resolvedComputeConfig = &soltypes.ComputeConfig{
			ComputeLimit:    &gasConfig.ComputeLimit,
			ComputeMaxPrice: &gasConfig.ComputeMaxPrice,
		}
	}

	reply, sendErr := fc.SubmitTransaction(ctx, soltypes.SubmitTransactionRequest{
		EncodedTransaction: encodedTX,
		Receiver:           soltypes.PublicKey(receiver),
		Cfg:                resolvedComputeConfig,
	})
	if sendErr != nil {
		return nil, fmt.Errorf("failed to submit transactions: %w", sendErr)
	}

	return reply, nil
}

func (fc *forwarderClient) deriveForwarderAuthority(receiverProgram solana.PublicKey) (solana.PublicKey, error) {
	seeds := [][]byte{
		[]byte("forwarder"),
		fc.forwarderState[:],
		receiverProgram[:],
	}
	ret, _, err := solana.FindProgramAddress(seeds, fc.forwarderProgramID)

	return ret, err
}

func (fc *forwarderClient) getOracleConfigPDA(ctx context.Context, workflowDonID, configVersion uint32) (solana.PublicKey, error) {
	oracleConfigPDA, err := getConfigPDA(fc.forwarderState, workflowDonID, configVersion, fc.forwarderProgramID)
	if err != nil {
		return solana.PublicKey{}, fmt.Errorf("failed to calculate oracle config PDA: %w", err)
	}

	oracleConfigAccount, err := fc.GetAccountInfoWithOpts(ctx, soltypes.GetAccountInfoRequest{
		Account: soltypes.PublicKey(oracleConfigPDA),
		Opts: &soltypes.GetAccountInfoOpts{
			Commitment: soltypes.CommitmentProcessed,
		},
	})
	if err != nil {
		return oracleConfigPDA, fmt.Errorf("error fetching cache state account %v; err: %w", oracleConfigPDA, err)
	}

	if oracleConfigAccount.Value == nil {
		return oracleConfigPDA, fmt.Errorf("cache state account does not exist %v", oracleConfigPDA)
	}

	return oracleConfigPDA, nil
}

func toPayload(report *sdk.ReportResponse) []byte {
	var ret []byte

	// 1. data_size ret[0]
	ret = append(ret, byte(len(report.Sigs)))

	// 2. add N signatures
	for _, sig := range report.Sigs {
		ret = append(ret, sig.Signature...)
	}

	// 3. add raw report
	ret = append(ret, report.RawReport...)

	// 4. add context
	ret = append(ret, report.ReportContext...)

	return ret
}

func getConfigPDA(statePubkey solana.PublicKey, donID uint32, configVersion uint32, programID solana.PublicKey) (solana.PublicKey, error) {
	configID := getConfigID(donID, configVersion)
	reqIDBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(reqIDBytes, configID)

	seeds := [][]byte{
		[]byte("config"),
		statePubkey.Bytes(),
		reqIDBytes,
	}

	addr, _, err := solana.FindProgramAddress(seeds, programID)
	return addr, err
}

func getConfigID(donID uint32, configVersion uint32) uint64 {
	return (uint64(donID) << 32) | uint64(configVersion)
}

// validateRemainingAccountMetas ensures each account meta has a 32-byte public key so that
// solana.PublicKey conversion cannot panic on short input.
func validateRemainingAccountMetas(accounts []*solcap.AccountMeta) error {
	for i, acc := range accounts {
		if acc == nil {
			return fmt.Errorf("remaining account %d: nil account meta", i)
		}
		pk := acc.GetPublicKey()
		if len(pk) != solana.PublicKeyLength {
			return fmt.Errorf("remaining account %d: public key must be exactly %d bytes, got %d (hex: %s)", i, solana.PublicKeyLength, len(pk), hex.EncodeToString(pk))
		}
	}
	return nil
}

func convertMetaPB(m []*solcap.AccountMeta) ([]*solana.AccountMeta, error) {
	if err := validateRemainingAccountMetas(m); err != nil {
		return nil, err
	}
	ret := make([]*solana.AccountMeta, 0, len(m))
	for _, acc := range m {
		ret = append(ret, &solana.AccountMeta{
			PublicKey:  solana.PublicKey(acc.PublicKey),
			IsWritable: acc.IsWritable,
		})
	}
	return ret, nil
}

func (fc *forwarderClient) deriveExecutionState(transmissionID [32]byte) (solana.PublicKey, error) {
	seeds := [][]byte{
		[]byte("execution_state"),
		fc.forwarderState.Bytes(),
		transmissionID[:],
	}

	ret, _, err := solana.FindProgramAddress(seeds, fc.forwarderProgramID)

	return ret, err
}
