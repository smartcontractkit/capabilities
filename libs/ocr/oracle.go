// Package ocr makes a capability's libocr oracles over what they need from the node however it is run: its networking
// ([Network]) and its keys with the OCR config the registry has them in ([Signing]), from the node for run and compat,
// and in-process for embed. A capability gets [Oracles] from the run and makes its oracles with it.
package ocr

import (
	"github.com/samber/do/v2"

	libocr "github.com/smartcontractkit/libocr/offchainreporting2plus"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3shims"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/ocrutil"
)

// NewOracle makes an oracle of a capability whose report info is RI, from the args the capability fills in.
type NewOracle[RI ReportInfo] = func(libocr.OCR3OracleArgs2[RI]) (libocr.Oracle, error)

// NewOracleFunc makes an oracle from its args. The run provides libocr.NewOracle; a test can provide its own instead.
type NewOracleFunc func(libocr.OracleArgs) (libocr.Oracle, error)

// Oracles makes a capability's oracles over the node's networking and signing.
type Oracles struct {
	network   Network
	signing   Signing
	newOracle NewOracleFunc
}

// New makes an oracle from args, with the parts that are the node's filled in: its networking, keys, transmit account
// and OCR config. args carry what is the capability's own, such as its plugin, transmitter, database and local config.
func (o Oracles) New[RI ReportInfo](args libocr.OCR3OracleArgs2[RI]) (libocr.Oracle, error) {
	return o.newOracle(wire(args, o.network, o.signing))
}

func newOracles(i do.Injector) (Oracles, error) {
	network, err := do.Invoke[Network](i)
	if err != nil {
		return Oracles{}, err
	}

	signing, err := do.Invoke[Signing](i)
	if err != nil {
		return Oracles{}, err
	}

	newOracle, err := do.Invoke[NewOracleFunc](i)
	if err != nil {
		return Oracles{}, err
	}

	return Oracles{network: network, signing: signing, newOracle: newOracle}, nil
}

// wire is args with the parts that are the node's filled in: its networking from network, and its keys, transmit
// account and OCR config from signing.
func wire[RI ReportInfo](args libocr.OCR3OracleArgs2[RI], network Network, signing Signing) libocr.OCR3OracleArgs2[RI] {
	args.BinaryNetworkEndpointFactory = network.BinaryNetworkEndpointFactory
	args.V2Bootstrappers = network.V2Bootstrappers
	args.OffchainKeyring = signing.OffchainKeyring
	args.OnchainKeyring = signing.OnchainKeyring[RI]()
	args.ContractConfigTracker = signing.ContractConfigTracker
	args.OffchainConfigDigester = signing.OffchainConfigDigester
	args.ContractTransmitter = signing.Transmitter(args.ContractTransmitter)
	args.LocalConfig = signing.LocalConfig(args.LocalConfig)
	return args
}

// OnchainKeyring is the node's onchain keyring, which signs each report with the key bundle its info names.
func (s Signing) OnchainKeyring[RI ReportInfo]() ocr3types.OnchainKeyring2[RI] {
	return ocr3shims.OnchainKeyringAsOnchainKeyring2[RI](&keyring[RI]{keyring: s.onchainKeyring})
}

// Transmitter is transmitter, transmitting as the node's account, which is how the config finds this oracle.
func (s Signing) Transmitter[RI any](transmitter ocr3types.ContractTransmitter[RI]) ocr3types.ContractTransmitter[RI] {
	return ocrutil.TransmitterWithAccount(s.TransmitAccount, transmitter)
}

// LocalConfig is lc for a config from the capabilities registry, which has no blocks to confirm.
func (s Signing) LocalConfig(lc ocrtypes.LocalConfig) ocrtypes.LocalConfig {
	return ocrutil.ForRegistryConfig(lc)
}

// keyring is the node's keyring, which signs with the key bundle a report's info names.
type keyring[RI ReportInfo] struct {
	keyring ocr3types.OnchainKeyring[string]
}

func (k *keyring[RI]) PublicKey() ocrtypes.OnchainPublicKey { return k.keyring.PublicKey() }

func (k *keyring[RI]) Sign(digest ocrtypes.ConfigDigest, seqNr uint64, report ocr3types.ReportWithInfo[RI]) ([]byte, error) {
	return k.keyring.Sign(digest, seqNr, keyBundleReport(report))
}

func (k *keyring[RI]) Verify(key ocrtypes.OnchainPublicKey, digest ocrtypes.ConfigDigest, seqNr uint64,
	report ocr3types.ReportWithInfo[RI], signature []byte) bool {
	return k.keyring.Verify(key, digest, seqNr, keyBundleReport(report), signature)
}

func (k *keyring[RI]) MaxSignatureLength() int { return k.keyring.MaxSignatureLength() }

func keyBundleReport[RI ReportInfo](report ocr3types.ReportWithInfo[RI]) ocr3types.ReportWithInfo[string] {
	return ocr3types.ReportWithInfo[string]{Report: report.Report, Info: report.Info.KeyBundle()}
}
