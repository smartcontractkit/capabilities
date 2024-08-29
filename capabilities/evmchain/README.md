# Log Event Trigger Capabilities

Log Event Trigger capability. Starts a workflow based on on-chain events.

## Plan

Inputs: Report

```go
type MetadataV1 struct {
    Version             uint8
	WorkflowExecutionID [32]byte
	Timestamp           uint32
	DonID               uint32
	DonConfigVersion    uint32
	WorkflowCID         [32]byte
	WorkflowName        [10]byte
	WorkflowOwner       [20]byte
	ReportID            [2]byte
}

type Report struct {
    // Payload prepends MetadataV1 struct.
	Report []byte
	// Report context is appended to the payload before signing by libOCR.
	// It contains config digest + round/epoch/sequence numbers (currently 96 bytes).
	// Has to be appended to the report before validating signatures.
	Context []byte
	// Always exactly F+1 signatures.
	Signatures [][]byte
	// Report ID defined in the workflow spec (2 bytes).
	ID []byte
}
```

