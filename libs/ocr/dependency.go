package ocr

//go:generate go run ./internal/docgen

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/samber/do/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/ocr/remote"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/libs/capability"
)

// ConfigNamespace is the namespace Config is set under.
const ConfigNamespace = "ocr"

// Config is how a run reaches the node's peer and OCR keys.
type Config struct {
	// ProxyAddress is the gRPC address the node serves its peer and OCR keys on. The node sets it for compat.
	ProxyAddress string
}

// init provides the node's OCR dependencies however the capability is run: from the node at Config.ProxyAddress for
// run and compat, and over the in-process network for each instance of embed.
func init() {
	capability.RunInjector.MustProvideConfig(ConfigNamespace, &Config{})
	capability.RunInjector.MustProvide(newProxyDependencies)
	capability.EmbedInjector.MustProvide(newEmbeddedDependencies)
}

// describeTimeout is how long the node has to describe its OCR dependencies.
const describeTimeout = time.Minute

// newProxyDependencies asks the node for its OCR dependencies, which do not change while it runs. The connection
// lives as long as the process, which is as long as a run has its capability.
func newProxyDependencies(i do.Injector) (remote.Dependencies, error) {
	cfg, err := do.Invoke[*Config](i)
	if err != nil {
		return remote.Dependencies{}, err
	}

	if cfg.ProxyAddress == "" {
		return remote.Dependencies{}, errors.New("an OCR capability needs the address of the node's peer and OCR keys, set ocr.proxy-address")
	}

	lggr, err := do.Invoke[logger.Logger](i)
	if err != nil {
		return remote.Dependencies{}, err
	}

	conn, err := grpc.NewClient(cfg.ProxyAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return remote.Dependencies{}, fmt.Errorf("failed to create OCR client for %s: %w", cfg.ProxyAddress, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), describeTimeout)
	defer cancel()
	return remote.NewFactory(lggr, conn).NewDependencies(ctx, remote.Config{})
}

func newEmbeddedDependencies(i do.Injector) (remote.Dependencies, error) {
	info, err := do.Invoke[capability.InstanceInfo](i)
	if err != nil {
		return remote.Dependencies{}, err
	}

	return embeddedFactory{net: embedNetwork, id: info.ID, total: info.Total}.NewDependencies(context.Background(), remote.Config{})
}
