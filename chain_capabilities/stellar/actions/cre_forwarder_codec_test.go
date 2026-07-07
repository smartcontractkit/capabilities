package actions

import (
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

func TestEncodeReport_SanitizesNilSlices(t *testing.T) {
	t.Parallel()
	codec := NewCREForwarderCodec()

	report := &workflowpb.ReportResponse{
		Sigs: []*workflowpb.AttributedSignature{{Signature: nil}},
	}
	args, err := codec.EncodeReport(testNodeAddress, testReceiverAddress, report)
	require.NoError(t, err)
	require.Len(t, args, 5)

	require.Equal(t, stellartypes.ScValTypeBytes, args[2].Type)
	require.NotNil(t, args[2].Bytes)
	require.Empty(t, args[2].Bytes)

	require.Equal(t, stellartypes.ScValTypeBytes, args[3].Type)
	require.NotNil(t, args[3].Bytes)
	require.Empty(t, args[3].Bytes)

	require.Equal(t, stellartypes.ScValTypeVec, args[4].Type)
	require.NotNil(t, args[4].Vec)
	require.Len(t, args[4].Vec.Values, 1)
	require.NotNil(t, args[4].Vec.Values[0].Bytes)
	require.Empty(t, args[4].Vec.Values[0].Bytes)
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
