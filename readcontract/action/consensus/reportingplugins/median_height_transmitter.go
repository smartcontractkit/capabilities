package reportingplugins

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

type requestHeights interface {
	SetConsensusHeightForRequest(requestID string, height uint64)
}

type MedianHeightTransmitter struct {
	Requests requestHeights
}

func (m *MedianHeightTransmitter) Transmit(ctx context.Context, digest types.ConfigDigest, u uint64, r ocr3types.ReportWithInfo[[]byte], signatures []types.AttributedOnchainSignature) error {
	var heights []RequestObservationHeight
	if err := json.Unmarshal(r.Report, &heights); err != nil {
		return fmt.Errorf("failed to unmarshal heights: %w", err)
	}

	for _, h := range heights {
		m.Requests.SetConsensusHeightForRequest(h.RequestID, h.Height)
	}

	return nil
}

func (m *MedianHeightTransmitter) FromAccount(ctx context.Context) (types.Account, error) {
	return "", nil
}
