package actions

import (
	"fmt"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	stellartypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/stellar"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
)

const reportProcessedTopicPrefix = "forwarder_ReportProcessed"

// CREForwarderCodec encodes and decodes Stellar CRE forwarder contract calls.
type CREForwarderCodec interface {
	EncodeReport(transmitter, receiver string, report *sdk.ReportResponse) ([]stellartypes.ScVal, error)
	EncodeQueryTransmissionInputs(transmissionID TransmissionID) ([]stellartypes.ScVal, error)
	DecodeQueryTransmissionInfo(returnValueXDR string, ledgerSequence uint32) (TransmissionInfo, error)
	EncodeReportProcessedTopicFilter(transmissionID TransmissionID) (stellartypes.TopicFilter, error)
}

type creForwarderCodecImpl struct{}

func NewCREForwarderCodec() CREForwarderCodec {
	return &creForwarderCodecImpl{}
}

// EncodeReport constructs ScVal arguments for the forwarder report() function.
//
// Arg order matches the on-chain Rust signature:
//
//	report(transmitter: Address, receiver: Address, raw_report: Bytes, report_context: Bytes, signatures: Vec<BytesN<65>>)
func (c *creForwarderCodecImpl) EncodeReport(transmitter, receiver string, report *sdk.ReportResponse) ([]stellartypes.ScVal, error) {
	transmitterVal, err := accountAddressToScVal(transmitter)
	if err != nil {
		return nil, fmt.Errorf("transmitter: %w", err)
	}

	receiverVal, err := contractAddressToScVal(receiver)
	if err != nil {
		return nil, err
	}

	rawReport := report.GetRawReport()
	if rawReport == nil {
		rawReport = []byte{}
	}
	reportContext := report.GetReportContext()
	if reportContext == nil {
		reportContext = []byte{}
	}

	rawReportVal := stellartypes.ScVal{Type: stellartypes.ScValTypeBytes, Bytes: rawReport}
	reportContextVal := stellartypes.ScVal{Type: stellartypes.ScValTypeBytes, Bytes: reportContext}

	sigs := report.GetSigs()
	sigVals := make([]*stellartypes.ScVal, len(sigs))
	for i, sig := range sigs {
		sigBytes := sig.GetSignature()
		if sigBytes == nil {
			sigBytes = []byte{}
		}
		s := stellartypes.ScVal{Type: stellartypes.ScValTypeBytes, Bytes: sigBytes}
		sigVals[i] = &s
	}
	sigsVal := stellartypes.ScVal{
		Type: stellartypes.ScValTypeVec,
		Vec:  &stellartypes.ScVec{Values: sigVals},
	}

	return []stellartypes.ScVal{transmitterVal, receiverVal, rawReportVal, reportContextVal, sigsVal}, nil
}

func (c *creForwarderCodecImpl) EncodeQueryTransmissionInputs(transmissionID TransmissionID) ([]stellartypes.ScVal, error) {
	receiverVal, err := contractAddressToScVal(transmissionID.Receiver)
	if err != nil {
		return nil, err
	}
	return []stellartypes.ScVal{
		receiverVal,
		{Type: stellartypes.ScValTypeBytes, Bytes: transmissionID.WorkflowExecutionID[:]},
		{Type: stellartypes.ScValTypeBytes, Bytes: transmissionID.ReportID[:]},
	}, nil
}

func (c *creForwarderCodecImpl) DecodeQueryTransmissionInfo(returnValueXDR string, ledgerSequence uint32) (TransmissionInfo, error) {
	var sv xdr.ScVal
	if err := xdr.SafeUnmarshalBase64(returnValueXDR, &sv); err != nil {
		return TransmissionInfo{}, fmt.Errorf("decode transmission info result XDR: %w", err)
	}

	state, transmitter, err := decodeContractTransmissionInfo(sv)
	if err != nil {
		return TransmissionInfo{}, err
	}

	return TransmissionInfo{
		State:           state,
		Transmitter:     transmitter,
		LedgerSequence:  ledgerSequence,
		Success:         state == TransmissionStateSucceeded,
		InvalidReceiver: state == TransmissionStateInvalidReceiver,
	}, nil
}

// decodeContractTransmissionInfo decodes the TransmissionInfo struct returned by
// get_transmission_info:
//
//	struct TransmissionInfo {
//	    state: TransmissionState,       // u32 enum
//	    transmitter: Option<Address>,
//	}
func decodeContractTransmissionInfo(sv xdr.ScVal) (TransmissionState, string, error) {
	if sv.Type != xdr.ScValTypeScvMap || sv.Map == nil || *sv.Map == nil {
		return 0, "", fmt.Errorf("transmission info: expected struct map, got %v", sv.Type)
	}

	var (
		state          TransmissionState
		transmitter    string
		hasState       bool
		hasTransmitter bool
	)

	for _, entry := range **sv.Map {
		keySym, ok := entry.Key.GetSym()
		if !ok {
			return 0, "", fmt.Errorf("transmission info: map key is not symbol")
		}

		switch string(keySym) {
		case "state":
			if hasState {
				return 0, "", fmt.Errorf("transmission info: duplicate state field")
			}
			hasState = true
			u32, ok := entry.Val.GetU32()
			if !ok {
				return 0, "", fmt.Errorf("transmission info: state is not u32")
			}
			if uint32(u32) > uint32(TransmissionStateFailed) {
				return 0, "", fmt.Errorf("transmission info: invalid state %d", u32)
			}
			state = TransmissionState(u32)
		case "transmitter":
			if hasTransmitter {
				return 0, "", fmt.Errorf("transmission info: duplicate transmitter field")
			}
			hasTransmitter = true
			if entry.Val.Type == xdr.ScValTypeScvVoid {
				continue
			}
			txr, ok := addressFromScVal(entry.Val)
			if !ok {
				return 0, "", fmt.Errorf("transmission info: transmitter is not void or address")
			}
			transmitter = txr
		default:
			return 0, "", fmt.Errorf("transmission info: unexpected field %q", keySym)
		}
	}

	if !hasState {
		return 0, "", fmt.Errorf("transmission info: missing state field")
	}
	if !hasTransmitter {
		return 0, "", fmt.Errorf("transmission info: missing transmitter field")
	}

	return state, transmitter, nil
}

func (c *creForwarderCodecImpl) EncodeReportProcessedTopicFilter(transmissionID TransmissionID) (stellartypes.TopicFilter, error) {
	eventName := reportProcessedTopicPrefix
	receiverVal, err := contractAddressToScVal(transmissionID.Receiver)
	if err != nil {
		return stellartypes.TopicFilter{}, err
	}
	return stellartypes.TopicFilter{
		Segments: []stellartypes.TopicSegment{
			{Value: &stellartypes.ScVal{Type: stellartypes.ScValTypeSymbol, Symbol: &eventName}},
			{Value: &receiverVal},
			{Value: &stellartypes.ScVal{Type: stellartypes.ScValTypeBytes, Bytes: transmissionID.WorkflowExecutionID[:]}},
			{Value: &stellartypes.ScVal{Type: stellartypes.ScValTypeBytes, Bytes: transmissionID.ReportID[:]}},
		},
	}, nil
}

func contractAddressToScVal(contractID string) (stellartypes.ScVal, error) {
	contractBytes, err := strkey.Decode(strkey.VersionByteContract, contractID)
	if err != nil {
		return stellartypes.ScVal{}, fmt.Errorf("%s invalid contract address %q: %w", capcommon.UserError, contractID, err)
	}
	if len(contractBytes) != 32 {
		return stellartypes.ScVal{}, fmt.Errorf("%s contract address must decode to 32 bytes, got %d", capcommon.UserError, len(contractBytes))
	}
	return stellartypes.ScVal{
		Type: stellartypes.ScValTypeAddress,
		Address: &stellartypes.ScAddress{
			Type:       stellartypes.ScAddressTypeContractID,
			ContractID: contractBytes,
		},
	}, nil
}

func accountAddressToScVal(accountID string) (stellartypes.ScVal, error) {
	accountBytes, err := strkey.Decode(strkey.VersionByteAccountID, accountID)
	if err != nil {
		return stellartypes.ScVal{}, fmt.Errorf("invalid account address %q: %w", accountID, err)
	}
	if len(accountBytes) != 32 {
		return stellartypes.ScVal{}, fmt.Errorf("account address must decode to 32 bytes, got %d", len(accountBytes))
	}
	return stellartypes.ScVal{
		Type: stellartypes.ScValTypeAddress,
		Address: &stellartypes.ScAddress{
			Type:      stellartypes.ScAddressTypeAccountID,
			AccountID: accountBytes,
		},
	}, nil
}

func addressFromScVal(sv xdr.ScVal) (string, bool) {
	if sv.Type != xdr.ScValTypeScvAddress || sv.Address == nil {
		return "", false
	}
	switch sv.Address.Type {
	case xdr.ScAddressTypeScAddressTypeAccount:
		if sv.Address.AccountId == nil {
			return "", false
		}
		raw := sv.Address.AccountId.Ed25519
		out, err := strkey.Encode(strkey.VersionByteAccountID, raw[:])
		return out, err == nil
	case xdr.ScAddressTypeScAddressTypeContract:
		if sv.Address.ContractId == nil {
			return "", false
		}
		raw := *sv.Address.ContractId
		out, err := strkey.Encode(strkey.VersionByteContract, raw[:])
		return out, err == nil
	default:
		return "", false
	}
}
