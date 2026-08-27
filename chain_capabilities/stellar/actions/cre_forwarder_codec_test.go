package actions

import (
	"bytes"
	"crypto/ed25519"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/require"

	stellartypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/stellar"
	workflowpb "github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
)

func TestDecodeQueryTransmissionInfo(t *testing.T) {
	t.Parallel()
	codec := NewCREForwarderCodec()

	cases := []struct {
		name          string
		state         TransmissionState
		wantSuccess   bool
		wantInvalidRx bool
	}{
		{"NotAttempted", TransmissionStateNotAttempted, false, false},
		{"Succeeded", TransmissionStateSucceeded, true, false},
		{"InvalidReceiver", TransmissionStateInvalidReceiver, false, true},
		{"Failed", TransmissionStateFailed, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			xdrResult := buildTransmissionInfoXDR(t, tc.state)
			info, err := codec.DecodeQueryTransmissionInfo(xdrResult, 42)
			require.NoError(t, err)
			require.Equal(t, tc.state, info.State)
			require.Equal(t, tc.wantSuccess, info.Success)
			require.Equal(t, tc.wantInvalidRx, info.InvalidReceiver)
			require.Equal(t, uint32(42), info.LedgerSequence)
		})
	}

	t.Run("invalid base64 returns error", func(t *testing.T) {
		t.Parallel()
		_, err := codec.DecodeQueryTransmissionInfo("not-valid-xdr!!!", 0)
		require.Error(t, err)
		require.Contains(t, err.Error(), "decode transmission info result XDR")
	})
}

func TestDecodeContractTransmissionInfo(t *testing.T) {
	t.Parallel()

	t.Run("not attempted with void transmitter", func(t *testing.T) {
		t.Parallel()
		xdrResult := marshalTransmissionInfoXDR(t, TransmissionStateNotAttempted, nil)
		state, transmitter, err := decodeContractTransmissionInfo(mustUnmarshalScVal(t, xdrResult))
		require.NoError(t, err)
		require.Equal(t, TransmissionStateNotAttempted, state)
		require.Empty(t, transmitter)
	})

	t.Run("succeeded with transmitter address", func(t *testing.T) {
		t.Parallel()
		txr := testNodeAddress
		xdrResult := marshalTransmissionInfoXDR(t, TransmissionStateSucceeded, &txr)
		state, transmitter, err := decodeContractTransmissionInfo(mustUnmarshalScVal(t, xdrResult))
		require.NoError(t, err)
		require.Equal(t, TransmissionStateSucceeded, state)
		require.Equal(t, testNodeAddress, transmitter)
	})

	t.Run("rejects bare u32 state encoding", func(t *testing.T) {
		t.Parallel()
		v := xdr.Uint32(TransmissionStateSucceeded)
		sv := xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &v}
		_, _, err := decodeContractTransmissionInfo(sv)
		require.Error(t, err)
		require.Contains(t, err.Error(), "expected struct map")
	})

	t.Run("rejects unexpected struct fields", func(t *testing.T) {
		t.Parallel()
		state := xdr.Uint32(TransmissionStateInvalidReceiver)
		stateVal := xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &state}
		stateSym := xdr.ScSymbol("state")
		stateKey := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &stateSym}
		invalid := true
		invalidVal := xdr.ScVal{Type: xdr.ScValTypeScvBool, B: &invalid}
		invalidSym := xdr.ScSymbol("invalid_receiver")
		invalidKey := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &invalidSym}
		voidVal := xdr.ScVal{Type: xdr.ScValTypeScvVoid}
		txrSym := xdr.ScSymbol("transmitter")
		txrKey := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &txrSym}
		scMap := xdr.ScMap{
			{Key: stateKey, Val: stateVal},
			{Key: invalidKey, Val: invalidVal},
			{Key: txrKey, Val: voidVal},
		}
		mapPtr := &scMap
		sv := xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &mapPtr}

		_, _, err := decodeContractTransmissionInfo(sv)
		require.Error(t, err)
		require.Contains(t, err.Error(), "unexpected field")
	})

	t.Run("rejects invalid state value", func(t *testing.T) {
		t.Parallel()
		xdrResult := marshalTransmissionInfoXDR(t, TransmissionState(99), nil)
		_, _, err := decodeContractTransmissionInfo(mustUnmarshalScVal(t, xdrResult))
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid state")
	})
}

func mustUnmarshalScVal(t *testing.T, b64 string) xdr.ScVal {
	t.Helper()
	var sv xdr.ScVal
	require.NoError(t, xdr.SafeUnmarshalBase64(b64, &sv))
	return sv
}

func TestEncodeReport_InvalidTransmitter(t *testing.T) {
	t.Parallel()
	codec := NewCREForwarderCodec()
	_, _, req := newWRReportFixture(t)
	_, err := codec.EncodeReport("not-a-valid-address", testReceiverAddress, req.Report)
	require.Error(t, err)
	require.Contains(t, err.Error(), "transmitter")
}

func TestEncodeReport_EncodesForwarderArguments(t *testing.T) {
	t.Parallel()
	codec := NewCREForwarderCodec()

	rawReport := []byte{0xAA}
	reportContext := make([]byte, ocrReportContextLen)
	reportContext[0] = 0xBB
	sig := make([]byte, ed25519OCRSigLen)
	sig[0] = 0x01
	sig[ed25519.PublicKeySize] = 0xCC

	report := &workflowpb.ReportResponse{
		RawReport:     rawReport,
		ReportContext: reportContext,
		Sigs:          []*workflowpb.AttributedSignature{{Signature: sig}},
	}
	args, err := codec.EncodeReport(testNodeAddress, testReceiverAddress, report)
	require.NoError(t, err)
	require.Len(t, args, 5)

	require.Equal(t, stellartypes.ScValTypeAddress, args[0].Type)
	require.Equal(t, stellartypes.ScValTypeAddress, args[1].Type)
	require.Equal(t, stellartypes.ScValTypeBytes, args[2].Type)
	require.Equal(t, rawReport, args[2].Bytes)
	require.Equal(t, stellartypes.ScValTypeBytes, args[3].Type)
	require.Equal(t, reportContext, args[3].Bytes)

	require.Equal(t, stellartypes.ScValTypeVec, args[4].Type)
	require.NotNil(t, args[4].Vec)
	require.Len(t, args[4].Vec.Values, 1)
	publicKey, signature := requireEncodedEd25519Signature(t, args[4].Vec.Values[0])
	require.Equal(t, sig[:ed25519.PublicKeySize], publicKey)
	require.Equal(t, sig[ed25519.PublicKeySize:], signature)
}

func TestEncodeReport_RejectsEmptyRawReport(t *testing.T) {
	t.Parallel()
	codec := NewCREForwarderCodec()

	report := &workflowpb.ReportResponse{
		ReportContext: make([]byte, ocrReportContextLen),
		Sigs:          wrTestSigs(),
	}
	_, err := codec.EncodeReport(testNodeAddress, testReceiverAddress, report)
	require.Error(t, err)
	require.Contains(t, err.Error(), "raw report is empty")
}

func TestEncodeReport_RejectsInvalidReportContextLength(t *testing.T) {
	t.Parallel()
	codec := NewCREForwarderCodec()

	report := &workflowpb.ReportResponse{
		RawReport:     []byte{0x01},
		ReportContext: make([]byte, ocrReportContextLen-1),
		Sigs:          wrTestSigs(),
	}
	_, err := codec.EncodeReport(testNodeAddress, testReceiverAddress, report)
	require.Error(t, err)
	require.Contains(t, err.Error(), "report context: expected 96 bytes")
}

func TestEncodeReport_RejectsInvalidSignatureLength(t *testing.T) {
	t.Parallel()
	codec := NewCREForwarderCodec()

	report := &workflowpb.ReportResponse{
		RawReport:     []byte{0x01},
		ReportContext: make([]byte, ocrReportContextLen),
		Sigs:          []*workflowpb.AttributedSignature{{Signature: make([]byte, 65)}}, // secp256k1 length, not ed25519
	}
	_, err := codec.EncodeReport(testNodeAddress, testReceiverAddress, report)
	require.Error(t, err)
	require.Contains(t, err.Error(), "expected 96 bytes")
}

// TestEncodeReport_SortsSignaturesByPublicKey verifies the forwarder's
// strictly-ascending-by-public-key requirement: signatures are emitted sorted by
// pubkey regardless of input order.
func TestEncodeReport_SortsSignaturesByPublicKey(t *testing.T) {
	t.Parallel()
	codec := NewCREForwarderCodec()

	sigHigh := make([]byte, ed25519OCRSigLen)
	sigHigh[0] = 0x02
	sigHigh[ed25519.PublicKeySize] = 0xA2
	sigLow := make([]byte, ed25519OCRSigLen)
	sigLow[0] = 0x01
	sigLow[ed25519.PublicKeySize] = 0xA1

	report := &workflowpb.ReportResponse{
		RawReport:     []byte{0x01},
		ReportContext: make([]byte, ocrReportContextLen),
		Sigs:          []*workflowpb.AttributedSignature{{Signature: sigHigh}, {Signature: sigLow}}, // out of order
	}
	args, err := codec.EncodeReport(testNodeAddress, testReceiverAddress, report)
	require.NoError(t, err)

	vec := args[4].Vec.Values
	require.Len(t, vec, 2)
	pk0, signature0 := requireEncodedEd25519Signature(t, vec[0])
	pk1, signature1 := requireEncodedEd25519Signature(t, vec[1])
	require.Equal(t, sigLow[:ed25519.PublicKeySize], pk0)
	require.Equal(t, sigHigh[:ed25519.PublicKeySize], pk1)
	require.Equal(t, sigLow[ed25519.PublicKeySize:], signature0)
	require.Equal(t, sigHigh[ed25519.PublicKeySize:], signature1)
}

func TestEncodeReport_RejectsDuplicateSignerPublicKey(t *testing.T) {
	t.Parallel()
	codec := NewCREForwarderCodec()

	sigA := make([]byte, ed25519OCRSigLen)
	sigB := make([]byte, ed25519OCRSigLen)
	copy(sigA[:ed25519.PublicKeySize], bytes.Repeat([]byte{0x01}, ed25519.PublicKeySize))
	copy(sigB[:ed25519.PublicKeySize], sigA[:ed25519.PublicKeySize])
	sigA[ed25519.PublicKeySize] = 0xA1
	sigB[ed25519.PublicKeySize] = 0xB1

	report := &workflowpb.ReportResponse{
		RawReport:     []byte{0x01},
		ReportContext: make([]byte, ocrReportContextLen),
		Sigs: []*workflowpb.AttributedSignature{
			{Signature: sigA},
			{Signature: sigB},
		},
	}
	_, err := codec.EncodeReport(testNodeAddress, testReceiverAddress, report)
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate signer public key")
}

func requireEncodedEd25519Signature(t *testing.T, value *stellartypes.ScVal) (publicKey, signature []byte) {
	t.Helper()

	require.NotNil(t, value)
	require.Equal(t, stellartypes.ScValTypeMap, value.Type)
	require.NotNil(t, value.Map)
	require.Len(t, value.Map.Entries, 2)

	publicKeyEntry := value.Map.Entries[0]
	require.NotNil(t, publicKeyEntry.Key)
	require.NotNil(t, publicKeyEntry.Key.Symbol)
	require.Equal(t, "public_key", *publicKeyEntry.Key.Symbol)
	require.NotNil(t, publicKeyEntry.Val)
	require.Equal(t, stellartypes.ScValTypeBytes, publicKeyEntry.Val.Type)
	require.Len(t, publicKeyEntry.Val.Bytes, ed25519.PublicKeySize)

	signatureEntry := value.Map.Entries[1]
	require.NotNil(t, signatureEntry.Key)
	require.NotNil(t, signatureEntry.Key.Symbol)
	require.Equal(t, "signature", *signatureEntry.Key.Symbol)
	require.NotNil(t, signatureEntry.Val)
	require.Equal(t, stellartypes.ScValTypeBytes, signatureEntry.Val.Type)
	require.Len(t, signatureEntry.Val.Bytes, ed25519.SignatureSize)

	return publicKeyEntry.Val.Bytes, signatureEntry.Val.Bytes
}

func TestEncodeQueryTransmissionInputs(t *testing.T) {
	t.Parallel()
	codec := NewCREForwarderCodec()
	var workflowExecutionID [32]byte
	var reportID [2]byte
	workflowExecutionID[0] = 0xAB
	reportID[0] = 0x01

	args, err := codec.EncodeQueryTransmissionInputs(TransmissionID{
		Receiver:            testReceiverAddress,
		WorkflowExecutionID: workflowExecutionID,
		ReportID:            reportID,
	})
	require.NoError(t, err)
	require.Len(t, args, 3)
	require.Equal(t, stellartypes.ScValTypeAddress, args[0].Type)
	require.Equal(t, workflowExecutionID[:], args[1].Bytes)
	require.Equal(t, reportID[:], args[2].Bytes)

	t.Run("invalid receiver", func(t *testing.T) {
		t.Parallel()
		_, err := codec.EncodeQueryTransmissionInputs(TransmissionID{Receiver: "bad"})
		require.Error(t, err)
	})
}

func TestEncodeReportProcessedTopicFilter(t *testing.T) {
	t.Parallel()
	codec := NewCREForwarderCodec()
	var workflowExecutionID [32]byte
	var reportID [2]byte
	filter, err := codec.EncodeReportProcessedTopicFilter(TransmissionID{
		Receiver:            testReceiverAddress,
		WorkflowExecutionID: workflowExecutionID,
		ReportID:            reportID,
	})
	require.NoError(t, err)
	require.Len(t, filter.Segments, 4)
	require.Equal(t, reportProcessedTopicPrefix, *filter.Segments[0].Value.Symbol)
}

func TestDecodeContractTransmissionInfo_Errors(t *testing.T) {
	t.Parallel()

	t.Run("duplicate state field", func(t *testing.T) {
		t.Parallel()
		state := xdr.Uint32(TransmissionStateSucceeded)
		stateVal := xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &state}
		stateSym := xdr.ScSymbol("state")
		stateKey := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &stateSym}
		voidVal := xdr.ScVal{Type: xdr.ScValTypeScvVoid}
		txrSym := xdr.ScSymbol("transmitter")
		txrKey := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &txrSym}
		scMap := xdr.ScMap{
			{Key: stateKey, Val: stateVal},
			{Key: stateKey, Val: stateVal},
			{Key: txrKey, Val: voidVal},
		}
		mapPtr := &scMap
		sv := xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &mapPtr}
		_, _, err := decodeContractTransmissionInfo(sv)
		require.Error(t, err)
		require.Contains(t, err.Error(), "duplicate state")
	})

	t.Run("missing state field", func(t *testing.T) {
		t.Parallel()
		voidVal := xdr.ScVal{Type: xdr.ScValTypeScvVoid}
		txrSym := xdr.ScSymbol("transmitter")
		txrKey := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &txrSym}
		scMap := xdr.ScMap{{Key: txrKey, Val: voidVal}}
		mapPtr := &scMap
		sv := xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &mapPtr}
		_, _, err := decodeContractTransmissionInfo(sv)
		require.Error(t, err)
		require.Contains(t, err.Error(), "missing state")
	})
}
