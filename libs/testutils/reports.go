package testutils

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/ocr3cap"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
)

func NewReport(t *testing.T, value map[string][]byte) *values.Value {
	wrappedValue, err := values.Wrap(value)
	if err != nil {
		t.Errorf("failed to wrap value: %v", err)
	}

	wrappedValueBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(values.Proto(wrappedValue))
	if err != nil {
		t.Errorf("failed to marshal wrapped value: %v", err)
	}

	wrappedSignedReport, err := values.Wrap(
		ocr3cap.SignedReport{
			Context:    []uint8{},
			ID:         []uint8{1},
			Report:     wrappedValueBytes,
			Signatures: [][]uint8{{}},
		},
	)
	if err != nil {
		t.Errorf("failed to wrap signed report: %v", err)
	}

	return &wrappedSignedReport
}
