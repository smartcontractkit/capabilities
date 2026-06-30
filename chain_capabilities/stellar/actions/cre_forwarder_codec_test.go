package actions

import (
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
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

func TestParseFieldsIntoTransmissionInfo(t *testing.T) {
	t.Parallel()

	t.Run("vec with state and transmitter", func(t *testing.T) {
		t.Parallel()
		state := xdr.Uint32(TransmissionStateSucceeded)
		stateVal := xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &state}
		accountID := xdr.MustAddress(testNodeAddress)
		txrVal := xdr.ScVal{
			Type: xdr.ScValTypeScvAddress,
			Address: &xdr.ScAddress{
				Type:      xdr.ScAddressTypeScAddressTypeAccount,
				AccountId: &accountID,
			},
		}
		vec := xdr.ScVec{stateVal, txrVal}
		vecPtr := &vec
		sv := xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &vecPtr}

		info := TransmissionInfo{}
		parseFieldsIntoTransmissionInfo(&info, sv)
		require.Equal(t, TransmissionStateSucceeded, info.State)
		require.Equal(t, testNodeAddress, info.Transmitter)
	})

	t.Run("map with state transmitter and flags", func(t *testing.T) {
		t.Parallel()
		state := xdr.Uint32(TransmissionStateInvalidReceiver)
		stateVal := xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &state}
		stateKey := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: func() *xdr.ScSymbol { s := xdr.ScSymbol("state"); return &s }()}
		invalid := true
		invalidVal := xdr.ScVal{Type: xdr.ScValTypeScvBool, B: &invalid}
		invalidKey := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: func() *xdr.ScSymbol { s := xdr.ScSymbol("invalid_receiver"); return &s }()}
		scMap := xdr.ScMap{
			{Key: stateKey, Val: stateVal},
			{Key: invalidKey, Val: invalidVal},
		}
		mapPtr := &scMap
		sv := xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &mapPtr}

		info := TransmissionInfo{}
		parseFieldsIntoTransmissionInfo(&info, sv)
		require.Equal(t, TransmissionStateInvalidReceiver, info.State)
		require.True(t, info.InvalidReceiver)
	})
}

func TestXDRExtractHelpers(t *testing.T) {
	t.Parallel()

	t.Run("extractStringish symbol and string", func(t *testing.T) {
		t.Parallel()
		sym := xdr.ScSymbol("state")
		symVal := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
		out, ok := extractStringish(symVal)
		require.True(t, ok)
		require.Equal(t, "state", out)

		str := xdr.ScString("transmitter")
		strVal := xdr.ScVal{Type: xdr.ScValTypeScvString, Str: &str}
		out, ok = extractStringish(strVal)
		require.True(t, ok)
		require.Equal(t, "transmitter", out)
	})

	t.Run("extractAddressString account and contract", func(t *testing.T) {
		t.Parallel()
		accountID := xdr.MustAddress(testNodeAddress)
		accountVal := xdr.ScVal{
			Type: xdr.ScValTypeScvAddress,
			Address: &xdr.ScAddress{
				Type:      xdr.ScAddressTypeScAddressTypeAccount,
				AccountId: &accountID,
			},
		}
		out, ok := extractAddressString(accountVal)
		require.True(t, ok)
		require.Equal(t, testNodeAddress, out)

		contractBytes, err := strkey.Decode(strkey.VersionByteContract, testForwarderAddress)
		require.NoError(t, err)
		var contractID xdr.ContractId
		copy(contractID[:], contractBytes)
		contractVal := xdr.ScVal{
			Type: xdr.ScValTypeScvAddress,
			Address: &xdr.ScAddress{
				Type:       xdr.ScAddressTypeScAddressTypeContract,
				ContractId: &contractID,
			},
		}
		out, ok = extractAddressString(contractVal)
		require.True(t, ok)
		require.Equal(t, testForwarderAddress, out)
	})

	t.Run("extractBool", func(t *testing.T) {
		t.Parallel()
		b := true
		val := xdr.ScVal{Type: xdr.ScValTypeScvBool, B: &b}
		require.NotNil(t, extractBool(val))
		require.True(t, *extractBool(val))
	})
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
}
