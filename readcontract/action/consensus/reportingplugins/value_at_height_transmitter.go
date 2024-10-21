package reportingplugins

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

type ValueAtHeightRequests interface {
	SetConsensusValue(ctx context.Context, requestID string, value []byte)
}

type ValueAtHeightTransmitter struct {
	Requests ValueAtHeightRequests
}

func (t *ValueAtHeightTransmitter) Transmit(ctx context.Context, digest types.ConfigDigest, u uint64, r ocr3types.ReportWithInfo[[]byte], signatures []types.AttributedOnchainSignature) error {
	var values []ObservedValue
	if err := json.Unmarshal(r.Report, &values); err != nil {
		return fmt.Errorf("failed to unmarshal values: %w", err)
	}

	for _, v := range values {
		t.Requests.SetConsensusValue(ctx, v.RequestID, v.Value)
	}

	return nil
}

func (t *ValueAtHeightTransmitter) FromAccount(ctx context.Context) (types.Account, error) {
	return "", nil
}
