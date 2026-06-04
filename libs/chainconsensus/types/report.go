package types

import ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

type HashableRequestReport struct {
	ConfigDigest               ocrtypes.ConfigDigest
	SeqNr                      uint64
	ReportData                 Hash
	AttributedOnchainSignature []ocrtypes.AttributedOnchainSignature
}

func NewHashableRequestReport(
	configDigest ocrtypes.ConfigDigest,
	seqNr uint64,
	reportData Hash,
	attributedOnchainSignature []ocrtypes.AttributedOnchainSignature,
) *HashableRequestReport {
	return &HashableRequestReport{
		ConfigDigest:               configDigest,
		SeqNr:                      seqNr,
		ReportData:                 reportData,
		AttributedOnchainSignature: attributedOnchainSignature,
	}
}
