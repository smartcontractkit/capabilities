package keystore

import (
	"testing"

	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"

	"github.com/smartcontractkit/capabilities/libs/capability"
	"github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr"
)

func TestNewNodeOCRClient(t *testing.T) {
	t.Parallel()
	newInjector := func(cfg *Config) do.Injector {
		i := do.New()
		do.ProvideValue(i, cfg)
		do.ProvideValue(i, logger.Test(t))
		do.ProvideValue(i, loop.GRPCOpts{})
		return i
	}

	_, err := newNodeOCRClient(newInjector(&Config{}))
	require.ErrorContains(t, err, "keystore.url is not set")

	client, err := newNodeOCRClient(newInjector(&Config{URL: "localhost:9001"}))
	require.NoError(t, err)
	assert.NotNil(t, client)
}

func TestNewEmbeddedOCRClient(t *testing.T) {
	t.Parallel()
	i := do.New()
	do.ProvideValue(i, capability.InstanceInfo{ID: 1, Total: 4})

	client, err := newEmbeddedOCRClient(i)
	require.NoError(t, err)
	want, err := ocr.EmbeddedKeyBundle(1)
	require.NoError(t, err)

	kbs, err := client.ListKeyBundles(t.Context())
	require.NoError(t, err)
	require.Len(t, kbs, 1)
	assert.Equal(t, want.PublicKey(), kbs[0].Onchain.PublicKey())
}
