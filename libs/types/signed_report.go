package types

type SignedReport struct {
	// Version determines the metadata format.
	Version  uint8
	Metadata []byte
	// Payload is the actual report data.
	Payload []byte
	// Report context is appended to the payload before signing by libOCR.
	// It contains config digest + round/epoch/sequence numbers (currently 96 bytes).
	// Has to be appended to the report before validating signatures.
	Context []byte
	// Always exactly F+1 signatures.
	Signatures [][]byte
}
