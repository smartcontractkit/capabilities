package keystore

import (
	"testing"

	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
)

func TestNewNodeOCRKeystoreClient(t *testing.T) {
	t.Parallel()
	newInjector := func(cfg *Config) do.Injector {
		i := do.New()
		do.ProvideValue(i, cfg)
		do.ProvideValue(i, logger.Test(t))
		do.ProvideValue(i, loop.GRPCOpts{})
		return i
	}

	_, err := newNodeOCRKeystoreClient(newInjector(&Config{}))
	require.ErrorContains(t, err, "keystore.url is not set")

	client, err := newNodeOCRKeystoreClient(newInjector(&Config{URL: "localhost:9001"}))
	require.NoError(t, err)
	assert.NotNil(t, client)
}
