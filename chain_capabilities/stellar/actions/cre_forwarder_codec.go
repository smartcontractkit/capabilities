package actions

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
	"slices"

	stellartypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/stellar"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
)

const (
	reportProcessedTopicPrefix = "forwarder_ReportProcessed"
	ocrReportContextLen        = 96
	ed25519OCRSigLen           = ed25519.PublicKeySize + ed25519.SignatureSize
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
func (creForwarderCodecImpl) EncodeReport(transmitter string, receiver string, report *sdk.ReportResponse) ([]stellartypes.ScVal, error) {
	if report == nil {
		return nil, fmt.Errorf("report is nil")
	}

	transmitterVal, err := accountAddressToScVal(transmitter)
	if err != nil {
		return nil, fmt.Errorf("transmitter: %w", err)
	}

	receiverVal, err := contractAddressToScVal(receiver)
	if err != nil {
		return nil, fmt.Errorf("receiver: %w", err)
	}

	rawReport := report.GetRawReport()
	if len(rawReport) == 0 {
		return nil, fmt.Errorf("raw report is empty")
	}
	reportContext := report.GetReportContext()
	if len(reportContext) != ocrReportContextLen {
		return nil, fmt.Errorf("report context: expected %d bytes, got %d", ocrReportContextLen, len(reportContext))
	}

	signatures := report.GetSigs()
	if len(signatures) == 0 {
		return nil, fmt.Errorf("report contains no signatures")
	}

	rawSignatures := make([][]byte, len(signatures))
	for i, attributedSig := range signatures {
		sig := attributedSig.GetSignature()
		if len(sig) != ed25519OCRSigLen {
			return nil, fmt.Errorf(
				"signature %d: expected %d bytes (%d-byte public key || %d-byte signature), got %d",
				i,
				ed25519OCRSigLen,
				ed25519.PublicKeySize,
				ed25519.SignatureSize,
				len(sig),
			)
		}
		rawSignatures[i] = sig
	}

	slices.SortFunc(rawSignatures, func(a, b []byte) int { return bytes.Compare(a[:ed25519.PublicKeySize], b[:ed25519.PublicKeySize]) })

	for i := 1; i < len(rawSignatures); i++ {
		previous := rawSignatures[i-1][:ed25519.PublicKeySize]
		current := rawSignatures[i][:ed25519.PublicKeySize]

		if bytes.Equal(previous, current) {
			return nil, fmt.Errorf("signature %d: duplicate signer public key", i)
		}
	}

	signatureVals := make([]*stellartypes.ScVal, len(rawSignatures))
	for i, sig := range rawSignatures {
		signatureVals[i] = encodeEd25519Signature(sig[:ed25519.PublicKeySize], sig[ed25519.PublicKeySize:])
	}

	return []stellartypes.ScVal{
		transmitterVal,
		receiverVal,
		{Type: stellartypes.ScValTypeBytes, Bytes: rawReport},
		{Type: stellartypes.ScValTypeBytes, Bytes: reportContext},
		{Type: stellartypes.ScValTypeVec, Vec: &stellartypes.ScVec{Values: signatureVals}}}, nil
}

func encodeEd25519Signature(publicKey, signature []byte) *stellartypes.ScVal {
	return &stellartypes.ScVal{
		Type: stellartypes.ScValTypeMap,
		Map: &stellartypes.ScMap{
			Entries: []stellartypes.ScMapEntry{
				{
					Key: &stellartypes.ScVal{
						Type:   stellartypes.ScValTypeSymbol,
						Symbol: new("public_key"),
					},
					Val: &stellartypes.ScVal{
						Type:  stellartypes.ScValTypeBytes,
						Bytes: publicKey,
					},
				},
				{
					Key: &stellartypes.ScVal{
						Type:   stellartypes.ScValTypeSymbol,
						Symbol: new("signature"),
					},
					Val: &stellartypes.ScVal{
						Type:  stellartypes.ScValTypeBytes,
						Bytes: signature,
					},
				},
			},
		},
	}
}

func (creForwarderCodecImpl) EncodeQueryTransmissionInputs(transmissionID TransmissionID) ([]stellartypes.ScVal, error) {
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

func (creForwarderCodecImpl) DecodeQueryTransmissionInfo(returnValueXDR string, ledgerSequence uint32) (TransmissionInfo, error) {
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

func (creForwarderCodecImpl) EncodeReportProcessedTopicFilter(transmissionID TransmissionID) (stellartypes.TopicFilter, error) {
	receiverVal, err := contractAddressToScVal(transmissionID.Receiver)
	if err != nil {
		return stellartypes.TopicFilter{}, err
	}
	return stellartypes.TopicFilter{
		Segments: []stellartypes.TopicSegment{
			{Value: &stellartypes.ScVal{Type: stellartypes.ScValTypeSymbol, Symbol: new(reportProcessedTopicPrefix)}},
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
