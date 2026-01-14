//nolint:revive
package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
