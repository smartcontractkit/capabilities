package ocr

import (
	"context"
	"time"

	libocr "github.com/smartcontractkit/libocr/offchainreporting2plus"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3shims"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"

	keystoreocr "github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr"
)

// NewRegistryOracle is an oracle for the DON capabilityID runs on in donID, as the node's own are made: it finds the
// node's keys and transmit account in the capability's OCR config from reg, then wires them, network and the config
// into args. The registry has synced by the time a capability starts, so a config that is missing is an error.
func NewRegistryOracle[RI ReportInfo](ctx context.Context, args libocr.OCR3OracleArgs2[RI], network Network,
	keystore keystoreocr.Client, reg registry.CapabilitiesRegistry, capabilityID string, donID uint32) (libocr.Oracle, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	config := &registryConfig{registry: reg, capabilityID: capabilityID, donID: donID}
	id, err := nodeIdentity(ctx, keystore, config)
	if err != nil {
		return nil, err
	}

	return libocr.NewOracle(wire(args, network, id))
}

// wire is args with the parts that are the node's filled in: its networking from network, and its keys, transmit
// account and OCR config from id.
func wire[RI ReportInfo](args libocr.OCR3OracleArgs2[RI], network Network, id identity) libocr.OCR3OracleArgs2[RI] {
	args.BinaryNetworkEndpointFactory = network.BinaryNetworkEndpointFactory
	args.V2Bootstrappers = network.V2Bootstrappers
	args.OffchainKeyring = id.OffchainKeyring
	args.OnchainKeyring = id.OnchainKeyring[RI]()
	args.ContractConfigTracker = id.ContractConfigTracker
	args.OffchainConfigDigester = id.OffchainConfigDigester
	args.ContractTransmitter = id.Transmitter(args.ContractTransmitter)
	args.LocalConfig = id.LocalConfig(args.LocalConfig)
	return args
}

// OnchainKeyring is the node's onchain keyring, which signs each report with the key bundle its info names.
func (s identity) OnchainKeyring[RI ReportInfo]() ocr3types.OnchainKeyring2[RI] {
	return ocr3shims.OnchainKeyringAsOnchainKeyring2[RI](&keyring[RI]{keyring: s.onchainKeyring})
}

// Transmitter is transmitter, transmitting as the node's account, which is how the config finds this oracle.
func (s identity) Transmitter[RI any](transmitter ocr3types.ContractTransmitter[RI]) ocr3types.ContractTransmitter[RI] {
	return &accountTransmitter[RI]{ContractTransmitter: transmitter, account: s.TransmitAccount}
}

// LocalConfig is lc for a config from the capabilities registry, which has no blocks to confirm, polled often so
// config changes are picked up quickly.
func (s identity) LocalConfig(lc ocrtypes.LocalConfig) ocrtypes.LocalConfig {
	lc.SkipContractConfigConfirmations = true
	lc.ContractConfigTrackerPollInterval = 5 * time.Second
	return lc
}

type accountTransmitter[RI any] struct {
	ocr3types.ContractTransmitter[RI]
	account ocrtypes.Account
}

func (t *accountTransmitter[RI]) FromAccount(context.Context) (ocrtypes.Account, error) {
	return t.account, nil
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
