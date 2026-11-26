package ocr

import (
	"testing"

	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/libocr/commontypes"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/keystore/corekeys/ocr2key"
	capabilitiespb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"

	keystoreocr "github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr"
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

func TestNodeSigning(t *testing.T) {
	t.Parallel()
	evm := newKeyBundle(t, "evm")
	solana := newKeyBundle(t, "solana")
	otherEVM := newKeyBundle(t, "evm")
	signerOf := func(bundles ...keystoreocr.KeyBundle) ocrtypes.OnchainPublicKey {
		keys := ocr2key.MultichainPublicKey{}
		for _, b := range bundles {
			keys[b.ChainType()] = b.PublicKey()
		}

		signer, err := keys.MarshalBinary()
		require.NoError(t, err)
		return signer
	}

	identityFor := func(cc ocrtypes.ContractConfig) (identity, error) {
		reg := &fakeRegistry{configs: map[string]ocrtypes.ContractConfig{capabilitiespb.OCR3ConfigDefaultKey: cc}}
		return nodeIdentity(t.Context(), keystoreocr.NewLocalClient(keystoreocr.StaticKeystore{evm, solana, otherEVM}),
			&registryConfig{registry: reg, capabilityID: "test@1.0.0", donID: 3})
	}

	unknown := newKeyBundle(t, "evm")
	signing, err := identityFor(ocrtypes.ContractConfig{
		Signers:      []ocrtypes.OnchainPublicKey{signerOf(unknown), signerOf(evm, solana)},
		Transmitters: []ocrtypes.Account{"aaaa", "bbbb"},
	})
	require.NoError(t, err)
	assert.Equal(t, ocrtypes.Account("bbbb"), signing.TransmitAccount, "the transmitter is paired with the node's signer")
	assert.Equal(t, evm.OffchainPublicKey(), signing.OffchainKeyring.OffchainPublicKey(), "offchain signing uses the evm bundle")
	assert.Equal(t, signerOf(evm, solana), signing.onchainKeyring.PublicKey())

	_, err = identityFor(ocrtypes.ContractConfig{Signers: []ocrtypes.OnchainPublicKey{signerOf(unknown)}, Transmitters: []ocrtypes.Account{"aaaa"}})
	require.ErrorContains(t, err, "none of the node's 3 OCR key bundles make up one of the 1 signers")

	_, err = identityFor(ocrtypes.ContractConfig{Signers: []ocrtypes.OnchainPublicKey{signerOf(solana)}, Transmitters: []ocrtypes.Account{"aaaa"}})
	require.ErrorContains(t, err, "with an evm bundle among them")
}

func newKeyBundle(t *testing.T, chainType string) keystoreocr.KeyBundle {
	t.Helper()
	kb, err := keystoreocr.NewKeyBundle(chainType)
	require.NoError(t, err)
	return kb
}
