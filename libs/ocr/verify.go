package ocr

import (
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/keystore/corekeys/ocr2key"
)

// verify checks a report's signature with the key bundle family its info names. It needs no secret, so it is done
// here rather than on the node.
func verify(publicKey ocrtypes.OnchainPublicKey, digest ocrtypes.ConfigDigest, seqNr uint64,
	report ocr3types.ReportWithInfo[string], signature []byte) bool {
	return ocr2key.VerifyOCR3(report.Info, publicKey, digest, seqNr, report.Report, signature)
}
