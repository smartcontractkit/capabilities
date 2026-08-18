package ocr

import (
	"context"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/smartcontractkit/libocr/commontypes"
	libocr "github.com/smartcontractkit/libocr/offchainreporting2plus"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3shims"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// OracleArgs is what a capability brings to an oracle of its own: what it is,
// what it computes, where it sends the result, and the identity it runs under.
//
// Everything else - the configuration, the digest, the database - follows from
// these, which is the point: a capability hosted outside a node states what it
// does and is handed an oracle, rather than assembling libocr itself.
type OracleArgs struct {
	// CapabilityID and DonID say which configuration in the registry is this
	// oracle's. Key names the OCR instance for a capability running more than
	// one; empty is its only one.
	CapabilityID string
	DonID        uint32
	Key          string

	// Registry is where the configuration comes from, digest included.
	Registry core.OCRConfigRegistry

	// Endpoints is the networking, and Keyrings the identity. Both come from
	// whoever holds the node's peer - see libs/standalone/ocr.
	Endpoints ocrtypes.BinaryNetworkEndpointFactory
	Offchain  ocrtypes.OffchainKeyring
	Onchain   ocr3types.OnchainKeyring[[]byte]

	// Bootstrappers are the peers to dial before this oracle has heard of
	// anyone. The registry lists who the oracle set is, not where to find them.
	Bootstrappers []commontypes.BootstrapperLocator

	Plugin      ocr3types.ReportingPluginFactory[[]byte]
	Transmitter ocr3types.ContractTransmitter[[]byte]

	LocalConfig ocrtypes.LocalConfig
	Logger      logger.Logger
	Metrics     prometheus.Registerer
}

// NewOracle builds the libocr oracle a capability runs.
func NewOracle(args OracleArgs) (libocr.Oracle, error) {
	if args.Registry == nil {
		return nil, fmt.Errorf("capability %s has no registry to read its OCR config from", args.CapabilityID)
	}

	tracker := &registryTracker{
		registry:     args.Registry,
		capabilityID: args.CapabilityID,
		donID:        args.DonID,
		key:          args.Key,
	}

	metrics := args.Metrics
	if metrics == nil {
		metrics = prometheus.NewRegistry()
	}

	return libocr.NewOracle(libocr.OCR3OracleArgs2[[]byte]{
		BinaryNetworkEndpointFactory: args.Endpoints,
		V2Bootstrappers:              args.Bootstrappers,
		ContractConfigTracker:        tracker,
		OffchainConfigDigester:       digester{},
		ContractTransmitter:          args.Transmitter,
		ReportingPluginFactory:       args.Plugin,
		Database:                     NewDatabase(args.CapabilityID, args.Logger),
		LocalConfig:                  args.LocalConfig,
		Logger:                       logger.NewOCRWrapper(args.Logger, true, func(string) {}),
		MetricsRegisterer:            metrics,
		MonitoringEndpoint:           monitoringEndpoint{},
		OffchainKeyring:              args.Offchain,
		OnchainKeyring:               ocr3shims.OnchainKeyringAsOnchainKeyring2(args.Onchain),
	})
}

// registryTracker is the capabilities registry, as a libocr config tracker.
//
// Where an on-chain OCR job watches its contract, a capability's configuration
// is a record in the CapabilitiesRegistry, so this reads it. There is nothing to
// subscribe to - the registry is a snapshot someone else keeps fresh - so it
// notifies nothing and libocr polls it at LocalConfig.ContractConfigTrackerPollInterval.
type registryTracker struct {
	registry     core.OCRConfigRegistry
	capabilityID string
	donID        uint32
	key          string
}

var _ ocrtypes.ContractConfigTracker = (*registryTracker)(nil)

func (t *registryTracker) Notify() <-chan struct{} { return nil }

func (t *registryTracker) LatestConfigDetails(ctx context.Context) (uint64, ocrtypes.ConfigDigest, error) {
	config, err := t.config(ctx)
	if err != nil {
		return 0, ocrtypes.ConfigDigest{}, err
	}
	// Block zero always: a registry record has no block a caller can ask about,
	// and libocr only uses this to tell one configuration from another, which
	// the digest already does.
	return 0, config.ConfigDigest, nil
}

func (t *registryTracker) LatestConfig(ctx context.Context, _ uint64) (ocrtypes.ContractConfig, error) {
	return t.config(ctx)
}

func (t *registryTracker) LatestBlockHeight(context.Context) (uint64, error) { return 0, nil }

func (t *registryTracker) config(ctx context.Context) (ocrtypes.ContractConfig, error) {
	config, err := t.registry.OCRConfig(ctx, t.capabilityID, t.donID, t.key)
	if err != nil {
		return ocrtypes.ContractConfig{}, err
	}
	if config.ConfigDigest == (ocrtypes.ConfigDigest{}) {
		return ocrtypes.ContractConfig{}, fmt.Errorf(
			"the registry returned the OCR config of capability %s without a digest", t.capabilityID)
	}
	return config, nil
}

// digester hands back the digest the configuration arrived with.
//
// Computing it is the registry's job, since it covers the chain and address the
// configuration was read from and a capability is not told either. Every oracle
// in the DON therefore agrees on the digest by having been given the same one,
// rather than by all computing it the same way.
type digester struct{}

var _ ocrtypes.OffchainConfigDigester = digester{}

func (digester) ConfigDigest(_ context.Context, config ocrtypes.ContractConfig) (ocrtypes.ConfigDigest, error) {
	return config.ConfigDigest, nil
}

func (digester) ConfigDigestPrefix(context.Context) (ocrtypes.ConfigDigestPrefix, error) {
	return ocrtypes.ConfigDigestPrefixKeystoneOCR3Capability, nil
}

// monitoringEndpoint drops libocr's telemetry. A capability reports through
// beholder like everything else in its process.
type monitoringEndpoint struct{}

var _ commontypes.MonitoringEndpoint = monitoringEndpoint{}

func (monitoringEndpoint) SendLog([]byte) {}
