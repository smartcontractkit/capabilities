package oracle

import (
	"bytes"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/smartcontractkit/libocr/commontypes"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	libocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/libs/chainconsensus/oracle/mocks"
	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
)

func TestContractTransmitter_Transmit(t *testing.T) {
	ctx := t.Context()
	var configDigest libocrtypes.ConfigDigest
	copy(configDigest[:], []byte("01234567890123456789012345678901"))
	const seqNr = uint64(42)

	t.Run("invalid report info", func(t *testing.T) {
		store := mocks.NewRequestsHandler(t)
		ct := NewContractTransmitter(logger.Test(t), store)
		err := ct.Transmit(ctx, configDigest, seqNr, ocr3types.ReportWithInfo[[]byte]{
			Report: []byte("x"),
			Info:   []byte("not valid protobuf"),
		}, nil)
		require.ErrorContains(t, err, "failed to unmarshal report info")
	})

	t.Run("proto report unmarshals and completes", func(t *testing.T) {
		store := mocks.NewRequestsHandler(t)
		expectedReport := &ctypes.RequestReport{
			RequestID: "proto-req",
			Report:    &ctypes.RequestReport_EventuallyConsistent{EventuallyConsistent: []byte("payload")},
		}
		rawReport := mustMarshalProto(expectedReport)
		info, err := marshalInfo(map[string]any{"keyBundleName": "evm"})
		require.NoError(t, err)

		store.EXPECT().CompleteProtoRequest("proto-req", mock.MatchedBy(func(r *ctypes.RequestReport) bool {
			return assert.Empty(t, cmp.Diff(expectedReport, r, cmp.Comparer(proto.Equal)))
		})).Return(nil).Once()

		ct := NewContractTransmitter(logger.Test(t), store)
		err = ct.Transmit(ctx, configDigest, seqNr, ocr3types.ReportWithInfo[[]byte]{
			Report: rawReport,
			Info:   info,
		}, nil)
		require.NoError(t, err)
	})

	t.Run("proto report unmarshal error", func(t *testing.T) {
		store := mocks.NewRequestsHandler(t)
		info, err := marshalInfo(map[string]any{"keyBundleName": "evm"})
		require.NoError(t, err)

		ct := NewContractTransmitter(logger.Test(t), store)
		err = ct.Transmit(ctx, configDigest, seqNr, ocr3types.ReportWithInfo[[]byte]{
			Report: []byte("not a request report"),
			Info:   info,
		}, nil)
		require.ErrorContains(t, err, "failed to unmarshal report")
	})

	t.Run("proto CompleteProtoRequest error", func(t *testing.T) {
		store := mocks.NewRequestsHandler(t)
		expectedReport := &ctypes.RequestReport{
			RequestID: "proto-req",
			Report:    &ctypes.RequestReport_EventuallyConsistent{EventuallyConsistent: []byte("payload")},
		}
		rawReport := mustMarshalProto(expectedReport)
		info, err := marshalInfo(map[string]any{"keyBundleName": "evm"})
		require.NoError(t, err)
		storeErr := errors.New("complete proto failed")
		store.EXPECT().CompleteProtoRequest("proto-req", mock.MatchedBy(func(r *ctypes.RequestReport) bool {
			return assert.Empty(t, cmp.Diff(expectedReport, r, cmp.Comparer(proto.Equal)))
		})).Return(storeErr).Once()

		ct := NewContractTransmitter(logger.Test(t), store)
		err = ct.Transmit(ctx, configDigest, seqNr, ocr3types.ReportWithInfo[[]byte]{
			Report: rawReport,
			Info:   info,
		}, nil)
		require.ErrorIs(t, err, storeErr)
	})

	t.Run("hashable completes", func(t *testing.T) {
		store := mocks.NewRequestsHandler(t)
		hashReport := bytes.Repeat([]byte{0xAB}, ctypes.HashLength)
		sigs := []libocrtypes.AttributedOnchainSignature{{Signer: 1, Signature: bytes.Repeat([]byte{0xAB}, ctypes.HashLength)}}

		info, err := marshalInfo(map[string]any{
			"keyBundleName":         "evm",
			reportInfoKeyReportType: reportTypeHashable,
			reportInfoKeyRequestID:  "hash-req",
		})
		require.NoError(t, err)

		store.EXPECT().CompleteHashableRequest("hash-req", mock.MatchedBy(func(r *ctypes.HashableRequestReport) bool {
			require.NotNil(t, r)
			require.Equal(t, configDigest, r.ConfigDigest)
			require.Equal(t, hashReport[:], r.ReportData[:])
			require.Len(t, r.AttributedOnchainSignature, 1)
			require.Equal(t, commontypes.OracleID(1), r.AttributedOnchainSignature[0].Signer)
			require.Equal(t, bytes.Repeat([]byte{0xAB}, ctypes.HashLength), r.AttributedOnchainSignature[0].Signature)
			return true
		})).Return(nil).Once()

		ct := NewContractTransmitter(logger.Test(t), store)
		err = ct.Transmit(ctx, configDigest, seqNr, ocr3types.ReportWithInfo[[]byte]{
			Report: hashReport,
			Info:   info,
		}, sigs)
		require.NoError(t, err)
	})

	t.Run("hashable invalid report length", func(t *testing.T) {
		store := mocks.NewRequestsHandler(t)
		info, err := marshalInfo(map[string]any{
			"keyBundleName":         "evm",
			reportInfoKeyReportType: reportTypeHashable,
			reportInfoKeyRequestID:  "hash-req",
		})
		require.NoError(t, err)

		ct := NewContractTransmitter(logger.Test(t), store)
		err = ct.Transmit(ctx, configDigest, seqNr, ocr3types.ReportWithInfo[[]byte]{
			Report: bytes.Repeat([]byte{1}, ctypes.HashLength-1),
			Info:   info,
		}, nil)
		require.ErrorContains(t, err, "invalid report length for hashable report")
	})

	t.Run("hashable request ID missing", func(t *testing.T) {
		store := mocks.NewRequestsHandler(t)
		info, err := marshalInfo(map[string]any{
			"keyBundleName":         "evm",
			reportInfoKeyReportType: reportTypeHashable,
		})
		require.NoError(t, err)

		ct := NewContractTransmitter(logger.Test(t), store)
		err = ct.Transmit(ctx, configDigest, seqNr, ocr3types.ReportWithInfo[[]byte]{
			Report: bytes.Repeat([]byte{1}, ctypes.HashLength),
			Info:   info,
		}, nil)
		require.ErrorContains(t, err, "failed to get request ID from report info")
		require.ErrorContains(t, err, "requestID not found")
	})

	t.Run("hashable request ID not a string", func(t *testing.T) {
		store := mocks.NewRequestsHandler(t)
		info, err := marshalInfo(map[string]any{
			"keyBundleName":         "evm",
			reportInfoKeyReportType: reportTypeHashable,
			reportInfoKeyRequestID:  12345,
		})
		require.NoError(t, err)

		ct := NewContractTransmitter(logger.Test(t), store)
		err = ct.Transmit(ctx, configDigest, seqNr, ocr3types.ReportWithInfo[[]byte]{
			Report: bytes.Repeat([]byte{1}, ctypes.HashLength),
			Info:   info,
		}, nil)
		require.ErrorContains(t, err, "failed to get request ID from report info")
		require.ErrorContains(t, err, "requestID is not a string")
	})

	t.Run("hashable CompleteHashableRequest error", func(t *testing.T) {
		store := mocks.NewRequestsHandler(t)
		hashReport := bytes.Repeat([]byte{0xCD}, ctypes.HashLength)
		info, err := marshalInfo(map[string]any{
			"keyBundleName":         "evm",
			reportInfoKeyReportType: reportTypeHashable,
			reportInfoKeyRequestID:  "hash-req",
		})
		require.NoError(t, err)
		storeErr := errors.New("complete hashable failed")
		store.EXPECT().CompleteHashableRequest("hash-req", mock.Anything).Return(storeErr).Once()

		ct := NewContractTransmitter(logger.Test(t), store)
		err = ct.Transmit(ctx, configDigest, seqNr, ocr3types.ReportWithInfo[[]byte]{
			Report: hashReport,
			Info:   info,
		}, nil)
		require.ErrorIs(t, err, storeErr)
	})

	t.Run("unknown report type", func(t *testing.T) {
		store := mocks.NewRequestsHandler(t)
		info, err := marshalInfo(map[string]any{
			"keyBundleName":         "evm",
			reportInfoKeyReportType: "unknown-kind",
		})
		require.NoError(t, err)

		ct := NewContractTransmitter(logger.Test(t), store)
		err = ct.Transmit(ctx, configDigest, seqNr, ocr3types.ReportWithInfo[[]byte]{
			Report: mustMarshalProto(&ctypes.RequestReport{RequestID: "x"}),
			Info:   info,
		}, nil)
		require.ErrorContains(t, err, "unknown report type")
	})
}
