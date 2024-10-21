package reportingplugins

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

type mockRequests struct {
	mock.Mock
}

func (m *mockRequests) SetConsensusHeightForRequest(requestID string, height uint64) {
	m.Called(requestID, height)
}

func TestMedianHeightTransmitter_Transmit(t *testing.T) {
	mockReq := new(mockRequests)
	transmitter := MedianHeightTransmitter{Requests: mockReq}

	heights := []RequestObservationHeight{
		{RequestID: "req1", Height: 100},
		{RequestID: "req2", Height: 200},
	}
	reportBytes, err := json.Marshal(heights)
	require.NoError(t, err)

	report := ocr3types.ReportWithInfo[[]byte]{Report: reportBytes}
	signatures := []types.AttributedOnchainSignature{}

	mockReq.On("SetConsensusHeightForRequest", "req1", uint64(100)).Once()
	mockReq.On("SetConsensusHeightForRequest", "req2", uint64(200)).Once()

	err = transmitter.Transmit(context.Background(), types.ConfigDigest{}, 0, report, signatures)
	require.NoError(t, err)

	mockReq.AssertExpectations(t)
}
