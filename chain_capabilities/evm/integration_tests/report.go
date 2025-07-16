package integration_test

import (
	"fmt"
	"testing"

	ocr3types "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
)

// PLEX-1612 - Remove once we have the actual report type
type Report struct {
	ocr3types.Metadata
	Data []byte
}

func Decode(raw []byte) (*Report, error) {
	md, tail, err := ocr3types.Decode(raw)
	if err != nil {
		return nil, err
	}
	if md.Version != 1 {
		return nil, fmt.Errorf("unsupported version %d", md.Version)
	}
	return &Report{Metadata: md, Data: tail}, nil
}

func (r Report) Encode() ([]byte, error) {
	// Encode the metadata
	metadataBytes, err := r.Metadata.Encode()
	if err != nil {
		return nil, err
	}

	return append(metadataBytes, r.Data...), nil
}

func NewTestReport(t *testing.T, data []byte) ([]byte, error) {
	metadata := ocr3types.Metadata{
		Version:          1,
		ExecutionID:      "0102030405060708090a0b0c0d0e0f1000000000000000000000000000000000",
		Timestamp:        1620000000,
		DONID:            1,
		DONConfigVersion: 1,
		WorkflowID:       "1234567890123456789012345678901234567890123456789012345678901234",
		WorkflowName:     "12",
		WorkflowOwner:    "1234567890123456789012345678901234567890",
		ReportID:         "1234",
	}
	report := Report{
		Metadata: metadata,
		Data:     data,
	}
	encoded, err := report.Encode()
	if err != nil {
		return nil, err
	}
	return encoded, nil
}
