package oracle

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/libocr/commontypes"
	"github.com/smartcontractkit/libocr/offchainreporting2/types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
)

func Test_Transmit(t *testing.T) {
	lggr := logger.Test(t)
	sendResponseCalled := false

	configDigest := [32]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F,
		0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1A, 0x1B, 0x1C, 0x1D, 0x1E, 0x1F, 0x20}
	seqNr := uint64(1)
	report := []byte("test-report")
	signatures := []types.AttributedOnchainSignature{
		{Signature: []byte("signature-1"), Signer: commontypes.OracleID(1)},
	}

	sendResponse := func(ctx context.Context, response ConsensusResponse) {
		sendResponseCalled = true
		require.NotEmpty(t, response.ReqID, "Request ID should not be empty")
		require.NotEmpty(t, response.ConfigDigest, "Config Digest should not be empty")
		require.NotEmpty(t, response.ReportContext, "Report Context should not be empty")
		require.NotEmpty(t, response.RawReport, "Raw Report should not be empty")
		require.NotEmpty(t, response.Sigs, "Signatures should not be empty")

		require.Equal(t, "test-request-id", response.ReqID, "Request ID does not match")
		require.Equal(t, types.ConfigDigest(configDigest), response.ConfigDigest, "Config Digest does not match")
		require.Equal(t, seqNr, response.SeqNr, "Sequence Number does not match")
		require.Equal(t, report, response.RawReport, "Raw Report does not match")
		require.Equal(t, signatures, response.Sigs, "Signatures do not match")
	}

	transmitter := NewContractTransmitter(lggr, sendResponse)

	info := &structpb.Struct{
		Fields: map[string]*structpb.Value{
			InfoRequestID: structpb.NewStringValue("test-request-id"),
		},
	}
	infoBytes, err := proto.Marshal(info)
	require.NoError(t, err, "failed to marshal report info")

	rwi := ocr3types.ReportWithInfo[[]byte]{
		Report: report,
		Info:   infoBytes,
	}

	err = transmitter.Transmit(context.Background(), configDigest, seqNr, rwi, signatures)
	require.NoError(t, err, "Transmit method returned an error")
	require.True(t, sendResponseCalled, "sendResponse should be called")
}
