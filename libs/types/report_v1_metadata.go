package types

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

type ReportV1Metadata struct {
	WorkflowExecutionID [32]byte
	Timestamp           uint32
	DonID               uint32
	DonConfigVersion    uint32
	WorkflowCID         [32]byte
	WorkflowName        [10]byte
	WorkflowOwner       [20]byte
	ReportID            [2]byte
}

const ReportV1MetadataLength = 108

func (rm ReportV1Metadata) Encode() ([]byte, error) {
	buf := new(bytes.Buffer)
	err := binary.Write(buf, binary.BigEndian, rm)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func DecodeReportV1Metadata(data []byte) (metadata ReportV1Metadata, err error) {
	if len(data) < ReportV1MetadataLength {
		return metadata, fmt.Errorf("data too short: %d bytes", len(data))
	}
	return metadata, binary.Read(bytes.NewReader(data[:ReportV1MetadataLength]), binary.BigEndian, &metadata)
}
