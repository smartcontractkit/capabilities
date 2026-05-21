//nolint:revive
package types

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	commoncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities"

	commonMon "github.com/smartcontractkit/capabilities/libs/monitoring"
)

func TestRequest_CaptureObservation(t *testing.T) {
	expectedObErr, err := NewObservationError(assert.AnError)
	require.NoError(t, err)
	testCases := []struct {
		Name                string
		prepareRequest      func(t *testing.T, request *observableRequest[int])
		ExpectedOb          int
		ExpectedObError     ObservationError
		ExpectedObAvailable bool
	}{
		{
			Name:           "observation was not captured",
			prepareRequest: func(t *testing.T, request *observableRequest[int]) {},
		},
		{
			Name: "successful observation",
			prepareRequest: func(t *testing.T, request *observableRequest[int]) {
				request.observe = func(ctx context.Context) (int, error) {
					return 1, nil
				}
				require.NoError(t, request.CaptureObservation(t.Context()))
			},
			ExpectedOb:          1,
			ExpectedObAvailable: true,
		},
		{
			Name: "failed observation",
			prepareRequest: func(t *testing.T, request *observableRequest[int]) {
				request.observe = func(ctx context.Context) (int, error) {
					return 0, assert.AnError
				}
				require.Error(t, request.CaptureObservation(t.Context()))
			},
			ExpectedObError:     expectedObErr,
			ExpectedObAvailable: true,
		},
		{
			Name: "successful observation followed by failed observation",
			prepareRequest: func(t *testing.T, request *observableRequest[int]) {
				request.observe = func(ctx context.Context) (int, error) {
					return 1, nil
				}
				require.NoError(t, request.CaptureObservation(t.Context()))
				request.observe = func(ctx context.Context) (int, error) {
					return 0, assert.AnError
				}
				require.Error(t, request.CaptureObservation(t.Context()))
			},
			ExpectedObError:     expectedObErr,
			ExpectedObAvailable: true,
		},
		{
			Name: "failed observation followed by successful observation",
			prepareRequest: func(t *testing.T, request *observableRequest[int]) {
				request.observe = func(ctx context.Context) (int, error) {
					return 0, assert.AnError
				}
				require.Error(t, request.CaptureObservation(t.Context()))
				request.observe = func(ctx context.Context) (int, error) {
					return 1, nil
				}
				require.NoError(t, request.CaptureObservation(t.Context()))
			},
			ExpectedOb:          1,
			ExpectedObAvailable: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			request := observableRequest[int]{}
			tc.prepareRequest(t, &request)
			actualOb, actualObErr, actualObAvailable := request.GetObservation()
			require.Equal(t, tc.ExpectedOb, actualOb)
			require.Equal(t, tc.ExpectedObError, actualObErr)
			require.Equal(t, tc.ExpectedObAvailable, actualObAvailable)
		})
	}
}

type customTestError struct {
	msg string
}

func (e customTestError) Error() string {
	return e.msg
}

func (e customTestError) GRPCStatus() *status.Status {
	return status.New(codes.AlreadyExists, e.msg)
}

func TestObservationError(t *testing.T) {
	t.Run("ensure custom GRPC status code is not lost during transmission", func(t *testing.T) {
		originalCustomError := customTestError{msg: "Custom error message"}
		originalError := error(originalCustomError)
		obError, err := NewObservationError(originalError)
		require.NoError(t, err)
		rawObError := []byte(obError) // error can be converted to bytes to be transmitted to other OCR nodes
		actualObError := ObservationError(rawObError)
		actualErr := actualObError.Err()
		type grpcStatus interface{ GRPCStatus() *status.Status }
		actualGRPCStatus, ok := actualErr.(grpcStatus)
		require.True(t, ok)
		require.Equal(t, originalCustomError.GRPCStatus().Code(), actualGRPCStatus.GRPCStatus().Code())
		require.Equal(t, originalCustomError.GRPCStatus().Message(), actualGRPCStatus.GRPCStatus().Message())
	})
	t.Run("Empty ObservationError produces nil error", func(t *testing.T) {
		require.NoError(t, ObservationError([]byte{}).Err())
	})
}

func testResponseMetadata() commoncap.ResponseMetadata {
	return commoncap.ResponseMetadata{
		Metering: []commoncap.MeteringNodeDetail{
			{SpendUnit: "test-unit", SpendValue: "1"},
		},
	}
}

func TestNewHashableRequest(t *testing.T) {
	wf, ref := "workflow-exec-id", "ref-id"
	meta := testResponseMetadata()
	r := NewHashableRequest(wf, ref, meta, func(context.Context) (*emptypb.Empty, error) {
		return nil, nil
	})
	require.Equal(t, commonMon.RequestID(wf, ref), r.ID())
	require.Equal(t, meta, r.GetMetadata())
}

func TestHashableRequest_GetOCRObservation(t *testing.T) {
	t.Run("no observation yet", func(t *testing.T) {
		r := NewHashableRequest("wf", "ref", testResponseMetadata(), func(context.Context) (*emptypb.Empty, error) {
			return nil, nil
		})
		ob, err := r.GetOCRObservation()
		require.Nil(t, err)
		require.Nil(t, ob)
	})

	t.Run("observation error is surfaced as RequestObservation_Error", func(t *testing.T) {
		r := NewHashableRequest("wf", "ref", testResponseMetadata(), func(context.Context) (*emptypb.Empty, error) {
			return nil, assert.AnError
		})
		require.Error(t, r.CaptureObservation(t.Context()))
		ob, err := r.GetOCRObservation()
		require.NoError(t, err)
		obErr, ok := ob.Observation.(*RequestObservation_Error)
		require.True(t, ok)
		expectedErr, err := NewObservationError(assert.AnError)
		require.NoError(t, err)
		require.Equal(t, expectedErr, ObservationError(obErr.Error))
	})

	t.Run("successful observation returns hash matching ResponseToReportData", func(t *testing.T) {
		wf, ref := "wf-success", "ref-success"
		meta := testResponseMetadata()
		payload1 := &wrapperspb.StringValue{Value: "deterministic-payload-1"}
		payload2 := &wrapperspb.StringValue{Value: "deterministic-payload-2"}
		var counter int
		r := NewHashableRequest(wf, ref, meta, func(context.Context) (*wrapperspb.StringValue, error) {
			if counter == 0 {
				counter++
				return payload1, nil
			}

			return payload2, nil
		})

		requireCorrectObservation := func(t *testing.T, expectedPayload proto.Message) Hash {
			require.NoError(t, r.CaptureObservation(t.Context()))
			ob, err := r.GetOCRObservation()
			require.NoError(t, err)
			hashOb, ok := ob.Observation.(*RequestObservation_Hashable)
			require.True(t, ok)
			require.Len(t, hashOb.Hashable, HashLength)

			payloadAsBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(expectedPayload)
			require.NoError(t, err)
			expectedHash, err := commoncap.ResponseToReportData(wf, ref, payloadAsBytes, meta)
			require.NoError(t, err)
			require.Equal(t, expectedHash[:], hashOb.Hashable)
			return expectedHash
		}

		payload1Hash := requireCorrectObservation(t, payload1)
		payload2Hash := requireCorrectObservation(t, payload2)
		require.NotEqual(t, payload1Hash, payload2Hash, "different payloads should produce different hashes")

		// both observations must be available
		actualPayload1, ok := r.GetObservationByReportData(payload1Hash)
		require.True(t, ok)
		require.Empty(t, cmp.Diff(payload1, actualPayload1, protocmp.Transform()))

		actualPayload2, ok := r.GetObservationByReportData(payload2Hash)
		require.True(t, ok)
		require.Empty(t, cmp.Diff(payload2, actualPayload2, protocmp.Transform()))
	})

	t.Run("invalid metadata fails when building report data", func(t *testing.T) {
		r := NewHashableRequest("wf", "ref", commoncap.ResponseMetadata{}, func(context.Context) (*emptypb.Empty, error) {
			return &emptypb.Empty{}, nil
		})
		require.NoError(t, r.CaptureObservation(t.Context()))
		_, err := r.GetOCRObservation()
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to convert response to report data: failed to extract metering from metadata: unexpected number of metering records received from peer")
	})
}

func TestNewLockableToBlockHashableRequest(t *testing.T) {
	wf, ref := "wf-ltb", "ref-ltb"
	meta := testResponseMetadata()
	r := NewLockableToBlockHashableRequest(wf, ref, meta, func(context.Context, *ChainHeight) (*emptypb.Empty, error) {
		return nil, nil
	})
	require.Equal(t, commonMon.RequestID(wf, ref), r.ID())
	require.Equal(t, meta, r.GetMetadata())
}

func TestLockableToBlockHashableRequest_GetOCRObservation(t *testing.T) {
	r := NewLockableToBlockHashableRequest("wf", "ref", testResponseMetadata(), func(context.Context, *ChainHeight) (*emptypb.Empty, error) {
		return nil, nil
	})
	ob, err := r.GetOCRObservation()
	require.NoError(t, err)
	_, ok := ob.Observation.(*RequestObservation_LockableToBlock)
	require.True(t, ok)
}

func TestLockableToBlockHashableRequest_LockToABlock_delegatesToHashableRequest(t *testing.T) {
	wf, ref := "wf-deleg", "ref-deleg"
	meta := testResponseMetadata()
	payload := &wrapperspb.StringValue{Value: "locked-payload"}
	height := &ChainHeight{Latest: 42}
	r := NewLockableToBlockHashableRequest(wf, ref, meta, func(_ context.Context, h *ChainHeight) (*wrapperspb.StringValue, error) {
		require.Equal(t, int64(42), h.GetLatest())
		return payload, nil
	})

	// gracefully handles GetObservationByReportData before the lock
	_, ok := r.GetObservationByReportData(Hash{})
	require.False(t, ok)
	hashable := r.LockToABlock(height).(*HashableRequest[*wrapperspb.StringValue])
	require.NoError(t, hashable.CaptureObservation(t.Context()))

	ob, err := hashable.GetOCRObservation()
	require.NoError(t, err)
	hashOb := ob.Observation.(*RequestObservation_Hashable)
	rawPayload, err := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
	require.NoError(t, err)
	want, err := commoncap.ResponseToReportData(wf, ref, rawPayload, meta)
	require.NoError(t, err)
	require.Equal(t, want[:], hashOb.Hashable)

	var key Hash
	copy(key[:], hashOb.Hashable)
	got, ok := r.GetObservationByReportData(key)
	require.True(t, ok)
	require.Empty(t, cmp.Diff(payload, got, protocmp.Transform()))
}
