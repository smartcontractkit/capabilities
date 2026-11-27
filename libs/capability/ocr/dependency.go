package ocr

import (
	"errors"
	"fmt"

	"github.com/samber/do/v2"

	"github.com/smartcontractkit/libocr/commontypes"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"

	"github.com/smartcontractkit/capabilities/libs/capability"
	"github.com/smartcontractkit/capabilities/libs/capability/ocr/remote"
)

// ConfigNamespace is the namespace Config is set under.
const ConfigNamespace = "ocr"

// Config points this process at the node's OCR networking.
type Config struct {
	// URL is the gRPC target the node serves its peer's networking on (e.g. localhost:9001). Unset means the node lends
	// this process no networking.
	URL string
	// PeerID is the node's peer ID (e.g. 12D3KooWKh28EhBVfiiFh39w3zqtBxzYJhmGfBZNmoL4tRjMWSor).
	PeerID string
	// Bootstrappers are the DON's bootstrap peers (e.g. 12D3KooWFirst@127.0.0.1:6690).
	Bootstrappers []commontypes.BootstrapperLocator
}

// Network is the node's networking, which a capability's oracles talk to each other over.
type Network struct {
	BinaryNetworkEndpointFactory ocrtypes.BinaryNetworkEndpointFactory
	V2Bootstrappers              []commontypes.BootstrapperLocator
}

// init provides the node's networking however the capability is run: from the node at Config.URL for run and compat,
// and over the in-process network for each instance of embed.
func init() {
	capability.RunInjector.MustProvideConfig(ConfigNamespace, &Config{})
	capability.RunInjector.MustProvide(newNodeNetwork)
	capability.EmbedInjector.MustProvide(newEmbeddedNetwork)
}

// newNodeNetwork connects to the node's networking. The connection lives as long as the process.
func newNodeNetwork(i do.Injector) (Network, error) {
	cfg, err := do.Invoke[*Config](i)
	if err != nil {
		return Network{}, err
	}

	if cfg.URL == "" || cfg.PeerID == "" {
		return Network{}, errors.New("ocr.url and ocr.peer-id are not set, so the node lends this process no networking")
	}

	lggr, err := do.Invoke[logger.Logger](i)
	if err != nil {
		return Network{}, err
	}

	grpcOpts, err := do.Invoke[loop.GRPCOpts](i)
	if err != nil {
		return Network{}, err
	}

	conn, err := capability.Dial(grpcOpts, cfg.URL)
	if err != nil {
		return Network{}, fmt.Errorf("failed to create OCR network client for %s: %w", cfg.URL, err)
	}

	return Network{
		BinaryNetworkEndpointFactory: remote.NewClient(lggr, conn, cfg.PeerID),
		V2Bootstrappers:              cfg.Bootstrappers,
	}, nil
}
