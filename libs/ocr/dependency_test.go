package ocr

import (
	"testing"

	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/libocr/commontypes"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
)

func TestNewNodeNetwork(t *testing.T) {
	t.Parallel()
	newInjector := func(cfg *Config) do.Injector {
		i := do.New()
		do.ProvideValue(i, cfg)
		do.ProvideValue(i, logger.Test(t))
		do.ProvideValue(i, loop.GRPCOpts{})
		return i
	}

	_, err := newNodeNetwork(newInjector(&Config{PeerID: "12D3KooWPeer"}))
	require.ErrorContains(t, err, "ocr.url and ocr.peer-id are not set")

	_, err = newNodeNetwork(newInjector(&Config{URL: "localhost:9001"}))
	require.ErrorContains(t, err, "ocr.url and ocr.peer-id are not set")

	bootstrappers := []commontypes.BootstrapperLocator{{PeerID: "12D3KooWBoot", Addrs: []string{"127.0.0.1:6690"}}}
	network, err := newNodeNetwork(newInjector(&Config{URL: "localhost:9001", PeerID: "12D3KooWPeer", Bootstrappers: bootstrappers}))
	require.NoError(t, err)
	assert.Equal(t, "12D3KooWPeer", network.BinaryNetworkEndpointFactory.PeerID())
	assert.Equal(t, bootstrappers, network.V2Bootstrappers)
}
