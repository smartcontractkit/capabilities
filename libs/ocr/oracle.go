// Package ocr makes a capability's libocr oracles over the node's OCR however it is run: over the node's peer and
// keys for run and compat, and over an in-process network for embed.
package ocr

import (
	libocr "github.com/smartcontractkit/libocr/offchainreporting2plus"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3shims"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/ocrutil"
)

// WithNode is args for the oracle of the capability with capabilityID, with the parts that are the node's filled in
// from deps, as the node's oracle factory fills them in.
func WithNode[T any](capabilityID string, args libocr.OCR3OracleArgs2[ReportInfo[T]], deps Dependencies) libocr.OCR3OracleArgs2[ReportInfo[T]] {
	args.BinaryNetworkEndpointFactory = deps.BinaryNetworkEndpointFactory
	args.V2Bootstrappers = deps.V2Bootstrappers
	args.ContractConfigTracker, args.OffchainConfigDigester = deps.Config(capabilityID)
	args.OffchainKeyring = deps.OffchainKeyring
	args.OnchainKeyring = ocr3shims.OnchainKeyringAsOnchainKeyring2[ReportInfo[T]](&keyring[T]{keyring: deps.OnchainKeyring})
	args.ContractTransmitter = ocrutil.TransmitterWithAccount(deps.TransmitAccount, args.ContractTransmitter)
	args.LocalConfig = ocrutil.ForRegistryConfig(args.LocalConfig)
	return args
}

// keyring is the node's keyring, which signs with the key bundle a report's info names.
type keyring[T any] struct {
	keyring ocr3types.OnchainKeyring[string]
}

func (k *keyring[T]) PublicKey() ocrtypes.OnchainPublicKey { return k.keyring.PublicKey() }

func (k *keyring[T]) Sign(digest ocrtypes.ConfigDigest, seqNr uint64, report ocr3types.ReportWithInfo[ReportInfo[T]]) ([]byte, error) {
	return k.keyring.Sign(digest, seqNr, keyBundleReport(report))
}

func (k *keyring[T]) Verify(key ocrtypes.OnchainPublicKey, digest ocrtypes.ConfigDigest, seqNr uint64,
	report ocr3types.ReportWithInfo[ReportInfo[T]], signature []byte) bool {
	return k.keyring.Verify(key, digest, seqNr, keyBundleReport(report), signature)
}

func (k *keyring[T]) MaxSignatureLength() int { return k.keyring.MaxSignatureLength() }

func keyBundleReport[T any](report ocr3types.ReportWithInfo[ReportInfo[T]]) ocr3types.ReportWithInfo[string] {
	return ocr3types.ReportWithInfo[string]{Report: report.Report, Info: report.Info.KeyBundle}
}
