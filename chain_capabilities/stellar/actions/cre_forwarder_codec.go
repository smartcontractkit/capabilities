package actions

import (
	"fmt"
	"strings"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	stellartypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/stellar"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
)

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

	info := TransmissionInfo{State: TransmissionStateNotAttempted, LedgerSequence: ledgerSequence}
	parseFieldsIntoTransmissionInfo(&info, sv)
	info.Success = info.State == TransmissionStateSucceeded
	info.InvalidReceiver = info.State == TransmissionStateInvalidReceiver
	return info, nil
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

func parseFieldsIntoTransmissionInfo(info *TransmissionInfo, sv xdr.ScVal) {
	switch sv.Type {
	case xdr.ScValTypeScvU32:
		if sv.U32 != nil {
			info.State = TransmissionState(*sv.U32)
		}
	case xdr.ScValTypeScvI32:
		if sv.I32 != nil && *sv.I32 >= 0 {
			info.State = TransmissionState(*sv.I32)
		}
	case xdr.ScValTypeScvVec:
		if sv.Vec == nil || *sv.Vec == nil {
			return
		}
		vec := **sv.Vec
		if len(vec) > 0 {
			parseFieldsIntoTransmissionInfo(info, vec[0])
		}
		if len(vec) > 1 {
			if txr, ok := extractAddressString(vec[1]); ok {
				info.Transmitter = txr
			}
		}
	case xdr.ScValTypeScvMap:
		if sv.Map == nil || *sv.Map == nil {
			return
		}
		for _, entry := range **sv.Map {
			key, ok := extractStringish(entry.Key)
			if !ok {
				continue
			}
			switch strings.ToLower(key) {
			case "state":
				parseFieldsIntoTransmissionInfo(info, entry.Val)
			case "transmitter":
				if txr, ok := extractAddressString(entry.Val); ok {
					info.Transmitter = txr
				}
			case "success":
				if b := extractBool(entry.Val); b != nil {
					info.Success = *b
				}
			case "invalid_receiver", "invalidreceiver":
				if b := extractBool(entry.Val); b != nil {
					info.InvalidReceiver = *b
				}
			}
		}
	default:
	}
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

func extractStringish(sv xdr.ScVal) (string, bool) {
	switch sv.Type {
	case xdr.ScValTypeScvSymbol:
		if sv.Sym == nil {
			return "", false
		}
		return string(*sv.Sym), true
	case xdr.ScValTypeScvString:
		if sv.Str == nil {
			return "", false
		}
		return string(*sv.Str), true
	default:
		return "", false
	}
}

func extractAddressString(sv xdr.ScVal) (string, bool) {
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

func extractBool(sv xdr.ScVal) *bool {
	if sv.Type != xdr.ScValTypeScvBool || sv.B == nil {
		return nil
	}
	b := *sv.B
	return &b
}
