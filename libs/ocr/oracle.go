// Package ocr makes a capability's libocr oracles over the node's OCR however it is run: over the node's peer and
// keys for run and compat, and over an in-process network for embed.
package ocr

import (
	"fmt"

	libocr "github.com/smartcontractkit/libocr/offchainreporting2plus"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3shims"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	commonocr "github.com/smartcontractkit/chainlink-common/pkg/capabilities/ocr"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/ocr/remote"

	"github.com/smartcontractkit/capabilities/libs/ocr/internal/nodeinfo"
)

// WithNode is args with the parts that are the node's filled in from deps, as the node's oracle factory fills them in.
func WithNode[T any](args libocr.OCR3OracleArgs2[ReportInfo[T]], deps remote.Dependencies) libocr.OCR3OracleArgs2[ReportInfo[T]] {
	args.BinaryNetworkEndpointFactory = deps.BinaryNetworkEndpointFactory
	args.V2Bootstrappers = deps.V2Bootstrappers
	args.ContractConfigTracker = deps.ContractConfigTracker
	args.OffchainConfigDigester = deps.OffchainConfigDigester
	args.OffchainKeyring = deps.OffchainKeyring
	args.OnchainKeyring = ocr3shims.OnchainKeyringAsOnchainKeyring2[ReportInfo[T]](&keyring[T]{keyring: deps.OnchainKeyring})
	args.ContractTransmitter = commonocr.TransmitterWithAccount(deps.TransmitAccount, args.ContractTransmitter)
	args.LocalConfig = commonocr.ForRegistryConfig(args.LocalConfig)
	return args
}

// keyring signs with the key bundle a report's info names. The node's keyring reads the name from []byte info.
type keyring[T any] struct {
	keyring ocr3types.OnchainKeyring[[]byte]
}

func (k *keyring[T]) PublicKey() ocrtypes.OnchainPublicKey { return k.keyring.PublicKey() }

func (k *keyring[T]) Sign(digest ocrtypes.ConfigDigest, seqNr uint64, report ocr3types.ReportWithInfo[ReportInfo[T]]) ([]byte, error) {
	r, err := k.report(report)
	if err != nil {
		return nil, err
	}

	return k.keyring.Sign(digest, seqNr, r)
}

func (k *keyring[T]) Verify(key ocrtypes.OnchainPublicKey, digest ocrtypes.ConfigDigest, seqNr uint64,
	report ocr3types.ReportWithInfo[ReportInfo[T]], signature []byte) bool {
	r, err := k.report(report)
	return err == nil && k.keyring.Verify(key, digest, seqNr, r, signature)
}

func (k *keyring[T]) MaxSignatureLength() int { return k.keyring.MaxSignatureLength() }

func (k *keyring[T]) report(report ocr3types.ReportWithInfo[ReportInfo[T]]) (ocr3types.ReportWithInfo[[]byte], error) {
	info, err := nodeinfo.Encode(report.Info.KeyBundle, nil)
	if err != nil {
		return ocr3types.ReportWithInfo[[]byte]{}, fmt.Errorf("failed to encode report info: %w", err)
	}

	return ocr3types.ReportWithInfo[[]byte]{Report: report.Report, Info: info}, nil
}
