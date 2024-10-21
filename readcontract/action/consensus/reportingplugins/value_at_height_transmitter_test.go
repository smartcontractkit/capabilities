package reportingplugins

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

type mockValueAtHeightRequests struct {
	requests map[string][]byte
}

func (m *mockValueAtHeightRequests) SetConsensusValue(ctx context.Context, requestID string, value []byte) {
	if m.requests == nil {
		m.requests = make(map[string][]byte)
	}
	m.requests[requestID] = value
}

func TestValueAtHeightTransmitter_Transmit(t *testing.T) {
	mockRequests := &mockValueAtHeightRequests{}
	transmitter := &ValueAtHeightTransmitter{Requests: mockRequests}

	ctx := context.Background()
	digest := types.ConfigDigest{}
	u := uint64(0)

	report := ocr3types.ReportWithInfo[[]byte]{
		Report: []byte(`[{"RequestID":"req1","Value":"` + base64.StdEncoding.EncodeToString([]byte("value1")) +
			`"},{"RequestID":"req2","Value":"` + base64.StdEncoding.EncodeToString([]byte("value2")) + `"}]`),
	}
	signatures := []types.AttributedOnchainSignature{}

	err := transmitter.Transmit(ctx, digest, u, report, signatures)
	assert.NoError(t, err)
	assert.Equal(t, []byte("value1"), mockRequests.requests["req1"])
	assert.Equal(t, []byte("value2"), mockRequests.requests["req2"])
}
