package ocr

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/samber/do/v2"

	"github.com/smartcontractkit/libocr/commontypes"
	libocr "github.com/smartcontractkit/libocr/offchainreporting2plus"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"

	"github.com/smartcontractkit/capabilities/libs/capability"
	nodekeyring "github.com/smartcontractkit/capabilities/libs/ocr/keyring"
	nodenetwork "github.com/smartcontractkit/capabilities/libs/ocr/network"
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

// Network is the node's networking, which a capability's oracles talk to each other over.
type Network struct {
	BinaryNetworkEndpointFactory ocrtypes.BinaryNetworkEndpointFactory
	V2Bootstrappers              []commontypes.BootstrapperLocator
}

// Signing is the node's OCR keys, and the OCR config the capabilities registry has them in.
type Signing struct {
	OffchainKeyring ocrtypes.OffchainKeyring
	// onchainKeyring signs with the key bundle a report's info names, such as evm. OnchainKeyring is it for a
	// capability's report info.
	onchainKeyring ocr3types.OnchainKeyring[string]
	// TransmitAccount is the account the config lists the node under.
	TransmitAccount        ocrtypes.Account
	ContractConfigTracker  ocrtypes.ContractConfigTracker
	OffchainConfigDigester ocrtypes.OffchainConfigDigester
}

// init provides the node's networking and signing however the capability is run, and the Oracles made over them:
// from the node at Config.NetworkURL and Config.KeyringURL for run and compat, and over the in-process network for each
// instance of embed.
func init() {
	capability.RunInjector.MustProvideConfig(ConfigNamespace, &Config{})
	capability.RunInjector.MustProvide(newNodeNetwork)
	capability.RunInjector.MustProvide(newNodeSigning)
	capability.EmbedInjector.MustProvide(newEmbeddedNetwork)
	capability.EmbedInjector.MustProvide(newEmbeddedSigning)
	capability.Injectors.MustProvideValue[NewOracleFunc](libocr.NewOracle)
	capability.Injectors.MustProvide(newOracles)
}

// connectTimeout is how long the node has to give its OCR public keys.
const connectTimeout = time.Minute

// newNodeNetwork connects to the node's networking. The connection lives as long as the process, which is as long as a
// run has its capability.
func newNodeNetwork(i do.Injector) (Network, error) {
	cfg, err := do.Invoke[*Config](i)
	if err != nil {
		return Network{}, err
	}

	if cfg.NetworkURL == "" || cfg.PeerID == "" {
		return Network{}, errors.New("an OCR capability needs ocr.network-url and ocr.peer-id")
	}

	lggr, err := do.Invoke[logger.Logger](i)
	if err != nil {
		return Network{}, err
	}

	grpcOpts, err := do.Invoke[loop.GRPCOpts](i)
	if err != nil {
		return Network{}, err
	}

	conn, err := capability.Dial(grpcOpts, cfg.NetworkURL)
	if err != nil {
		return Network{}, fmt.Errorf("failed to create OCR network client for %s: %w", cfg.NetworkURL, err)
	}

	factory, err := nodenetwork.Connect(lggr, conn, cfg.PeerID, nodenetwork.Config{})
	if err != nil {
		return Network{}, err
	}

	return Network{BinaryNetworkEndpointFactory: factory, V2Bootstrappers: cfg.Bootstrappers}, nil
}

// newNodeSigning connects to the node's OCR keys, and finds the capability's OCR config through the registry. The
// connection lives as long as the process, which is as long as a run has its capability.
func newNodeSigning(i do.Injector) (Signing, error) {
	cfg, err := do.Invoke[*Config](i)
	if err != nil {
		return Signing{}, err
	}

	if cfg.KeyringURL == "" {
		return Signing{}, errors.New("an OCR capability needs ocr.keyring-url")
	}

	grpcOpts, err := do.Invoke[loop.GRPCOpts](i)
	if err != nil {
		return Signing{}, err
	}

	reg, err := do.Invoke[registry.CapabilitiesRegistry](i)
	if err != nil {
		return Signing{}, err
	}

	capabilities, err := do.Invoke[capability.CapabilitiesConfig](i)
	if err != nil {
		return Signing{}, err
	}

	conn, err := capability.Dial(grpcOpts, cfg.KeyringURL)
	if err != nil {
		return Signing{}, fmt.Errorf("failed to create OCR keyring client for %s: %w", cfg.KeyringURL, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	keyrings, err := nodekeyring.Connect(ctx, conn, nodekeyring.Config{}, verify)
	if err != nil {
		return Signing{}, err
	}

	transmitAccount := ocrtypes.Account(cfg.TransmitAccount)
	config := &registryConfig{registry: reg, capabilityID: capabilityIDFrom(i), donID: capabilities.CapabilityDonID,
		transmitAccount: transmitAccount}
	return Signing{
		OffchainKeyring:        keyrings.OffchainKeyring,
		onchainKeyring:         keyrings.OnchainKeyring,
		TransmitAccount:        transmitAccount,
		ContractConfigTracker:  config,
		OffchainConfigDigester: config,
	}, nil
}

func newEmbeddedNetwork(i do.Injector) (Network, error) {
	info, err := do.Invoke[capability.InstanceInfo](i)
	if err != nil {
		return Network{}, err
	}

	network, _, err := embeddedDependencies(embedNetwork, info.ID, info.Total)
	return network, err
}

func newEmbeddedSigning(i do.Injector) (Signing, error) {
	info, err := do.Invoke[capability.InstanceInfo](i)
	if err != nil {
		return Signing{}, err
	}

	_, signing, err := embeddedDependencies(embedNetwork, info.ID, info.Total)
	return signing, err
}
