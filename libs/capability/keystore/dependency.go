package keystore

import (
	"errors"
	"fmt"

	"github.com/samber/do/v2"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"

	"github.com/smartcontractkit/capabilities/libs/capability"
	"github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr"
	ocrremote "github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr/remote"
)

// ConfigNamespace is the namespace Config is set under.
const ConfigNamespace = "keystore"

// Config points this process at the node's keystores.
type Config struct {
	// URL is the gRPC target the node serves its keystores on (e.g. localhost:9001). Unset means the node lends this
	// process no keys.
	URL string
}

func init() {
	capability.RunInjector.MustProvideConfig(ConfigNamespace, &Config{})
	capability.RunInjector.MustProvide(newNodeOCRClient)
}

// newNodeOCRClient connects to the node's OCR keystore. The connection lives as long as the process.
func newNodeOCRClient(i do.Injector) (ocr.Client, error) {
	cfg, err := do.Invoke[*Config](i)
	if err != nil {
		return nil, err
	}

	if cfg.URL == "" {
		return nil, errors.New("keystore.url is not set, so the node lends this process no keys")
	}

	lggr, err := do.Invoke[logger.Logger](i)
	if err != nil {
		return nil, err
	}

	grpcOpts, err := do.Invoke[loop.GRPCOpts](i)
	if err != nil {
		return nil, err
	}

	conn, err := capability.Dial(grpcOpts, cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to create keystore client for %s: %w", cfg.URL, err)
	}

	return ocrremote.NewClient(lggr, conn), nil
}
