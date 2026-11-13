package ocr

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/samber/do/v2"

	"github.com/smartcontractkit/libocr/commontypes"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	commonkeyring "github.com/smartcontractkit/chainlink-common/pkg/capabilities/ocr/keyring"
	commonnetwork "github.com/smartcontractkit/chainlink-common/pkg/capabilities/ocr/network"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"

	"github.com/smartcontractkit/capabilities/libs/capability"
)

// ConfigNamespace is the namespace Config is set under.
const ConfigNamespace = "ocr"

// Config is how a capability's oracles run for the node, over its peer and keys. The node sets it for compat.
type Config struct {
	// NetworkURL is the gRPC target the node serves its peer's networking on (e.g. localhost:9001).
	NetworkURL string
	// KeyringURL is the gRPC target the node serves its OCR keys on (e.g. localhost:9001).
	KeyringURL string
	// PeerID is the node's peer ID (e.g. 12D3KooWKh28EhBVfiiFh39w3zqtBxzYJhmGfBZNmoL4tRjMWSor).
	PeerID string
	// TransmitAccount is the account the node is registered to transmit from
	// (e.g. 0x5994a5155e9b81ab7794b79bfbf076ef5ef7c437).
	TransmitAccount string
	// Bootstrappers are the DON's bootstrap peers (e.g. 12D3KooWFirst@127.0.0.1:6690).
	Bootstrappers []commontypes.BootstrapperLocator
}

// Dependencies are what a capability's oracle needs from the node it runs for.
type Dependencies struct {
	BinaryNetworkEndpointFactory ocrtypes.BinaryNetworkEndpointFactory
	commonkeyring.Keyrings
	V2Bootstrappers []commontypes.BootstrapperLocator
	TransmitAccount ocrtypes.Account
	// Config returns the config tracker and digester of the oracle of the capability with capabilityID.
	Config func(capabilityID string) (ocrtypes.ContractConfigTracker, ocrtypes.OffchainConfigDigester)
}

// init provides the OCR dependencies however the capability is run: from the node at Config.NetworkURL and
// Config.KeyringURL for run and compat, and over the in-process network for each instance of embed.
func init() {
	capability.RunInjector.MustProvideConfig(ConfigNamespace, &Config{})
	capability.RunInjector.MustProvide(newNodeDependencies)
	capability.EmbedInjector.MustProvide(newEmbeddedDependencies)
}

// connectTimeout is how long the node has to give its OCR public keys.
const connectTimeout = time.Minute

// newNodeDependencies connects to the node's OCR networking and keys. The connections live as long as the process, which
// is as long as a run has its capability.
func newNodeDependencies(i do.Injector) (Dependencies, error) {
	cfg, err := do.Invoke[*Config](i)
	if err != nil {
		return Dependencies{}, err
	}

	if cfg.NetworkURL == "" || cfg.KeyringURL == "" || cfg.PeerID == "" {
		return Dependencies{}, errors.New("an OCR capability needs ocr.network-url, ocr.keyring-url and ocr.peer-id")
	}

	lggr, err := do.Invoke[logger.Logger](i)
	if err != nil {
		return Dependencies{}, err
	}

	grpcOpts, err := do.Invoke[loop.GRPCOpts](i)
	if err != nil {
		return Dependencies{}, err
	}

	reg, err := do.Invoke[registry.CapabilitiesRegistry](i)
	if err != nil {
		return Dependencies{}, err
	}

	capabilities, err := do.Invoke[capability.CapabilitiesConfig](i)
	if err != nil {
		return Dependencies{}, err
	}

	networkConn, err := capability.Dial(grpcOpts, cfg.NetworkURL)
	if err != nil {
		return Dependencies{}, fmt.Errorf("failed to create OCR network client for %s: %w", cfg.NetworkURL, err)
	}

	factory, err := commonnetwork.Connect(lggr, networkConn, cfg.PeerID, commonnetwork.Config{})
	if err != nil {
		return Dependencies{}, err
	}

	keyringConn, err := capability.Dial(grpcOpts, cfg.KeyringURL)
	if err != nil {
		return Dependencies{}, fmt.Errorf("failed to create OCR keyring client for %s: %w", cfg.KeyringURL, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	keyrings, err := commonkeyring.Connect(ctx, keyringConn, commonkeyring.Config{}, verify)
	if err != nil {
		return Dependencies{}, err
	}

	transmitAccount := ocrtypes.Account(cfg.TransmitAccount)
	return Dependencies{
		BinaryNetworkEndpointFactory: factory,
		Keyrings:                     keyrings,
		V2Bootstrappers:              cfg.Bootstrappers,
		TransmitAccount:              transmitAccount,
		Config: func(capabilityID string) (ocrtypes.ContractConfigTracker, ocrtypes.OffchainConfigDigester) {
			c := &registryConfig{registry: reg, capabilityID: capabilityID, donID: capabilities.CapabilityDonID,
				transmitAccount: transmitAccount}
			return c, c
		},
	}, nil
}

func newEmbeddedDependencies(i do.Injector) (Dependencies, error) {
	info, err := do.Invoke[capability.InstanceInfo](i)
	if err != nil {
		return Dependencies{}, err
	}

	return embeddedDependencies(embedNetwork, info.ID, info.Total)
}
