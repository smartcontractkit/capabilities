package ocr

import (
	"context"
	"crypto/ed25519"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/smartcontractkit/chainlink-protos/cre/impl/proxy"

	ocr2types "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"
)

func TestProxy(t *testing.T) {
	t.Run("the configured peer ID is the one reported and delegated for", func(t *testing.T) {
		peerID := mustPeerID(t, 7)

		// Resolving this form reads the signer's public keys, so there has to be one to read:
		// keyrings and networking are resolved together because they come from the same process.
		dep := &proxyDependency{lggr: logger.Test(t), cfg: ProxyConfig{
			ProxyAddress:    serveSigner(t),
			PeerID:          peerID,
			TransmitAccount: "0x5994a5155e9b81ab7794b79bfbf076ef5ef7c437",
		}}

		factories, err := dep.Get(t.Context(), standalone.CommonConfig{})
		require.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, factories.Close()) })

		// No keystore was unlocked and no database was opened to learn this: the peer ID is public,
		// and the password that would yield it stays with the process hosting the peer.
		assert.Equal(t, peerID, factories.PeerID)
		assert.Equal(t, peerID.String(), factories.OCR2Endpoint.PeerID())
		assert.Equal(t, peerID.String(), factories.OCR3_1Endpoint.PeerID())

		// The third part of the identity the configuration lists, carried like the peer ID and for
		// the same reason: it is public, and this process holds none of the keys behind it.
		assert.Equal(t, ocr2types.Account("0x5994a5155e9b81ab7794b79bfbf076ef5ef7c437"), factories.TransmitAccount)
	})

	t.Run("a peer ID is decoded from its text form", func(t *testing.T) {
		// ragetypes.PeerID unmarshals text, so the flags package binds it as a leaf and rejects a
		// malformed value when the configuration is decoded rather than when it is first used.
		var decoded ragetypes.PeerID
		require.NoError(t, decoded.UnmarshalText([]byte(mustPeerID(t, 4).String())))
		assert.Equal(t, mustPeerID(t, 4), decoded)

		require.Error(t, decoded.UnmarshalText([]byte("not-a-peer-id")))
	})

	t.Run("delegating needs the proxy address and the peer ID", func(t *testing.T) {
		_, err := (&proxyDependency{lggr: logger.Test(t)}).Get(t.Context(), standalone.CommonConfig{})
		require.ErrorContains(t, err, "--ocr.proxy-address is required")

		_, err = (&proxyDependency{lggr: logger.Test(t), cfg: ProxyConfig{ProxyAddress: "127.0.0.1:50051"}}).
			Get(t.Context(), standalone.CommonConfig{})
		require.ErrorContains(t, err, "--ocr.peer-id is required")

		// The transmit account is not among them: a process passing messages over the endpoints runs
		// no oracle and has no account, and an oracle given none is rejected by libocr as a
		// non-member - which is where a wrong one shows up too.
	})

	t.Run("an embedded instance needs neither, deriving its identity instead", func(t *testing.T) {
		// Nothing about where to reach anyone is configured: the identity is derived from the index,
		// and the only settings left are the protocol the oracles run.
		dep := Proxy(logger.Test(t)).ForEmbedding(2, 4)

		factories, err := dep.Get(t.Context(), standalone.CommonConfig{})
		require.NoError(t, err)

		assert.Equal(t, mustPeerID(t, 2), factories.PeerID)
		assert.Equal(t, &DefaultEmbeddedOCRConfig, dep.Config(), "the protocol is still configurable")
	})
}

// serveSigner starts a Signer serving fixed public keys and returns its address.
//
// Only the keys are needed: this covers what resolving the dependency reads, and a
// signature made by a fake key would prove nothing about the real one. See
// signer_test.go for the signing itself.
func serveSigner(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := grpc.NewServer()
	proxy.RegisterSignerServer(server, stubSigner{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	return listener.Addr().String()
}

// stubSigner answers with well-formed public keys and refuses to sign: nothing here
// holds a key, and a caller reaching the signing calls would be testing this rather
// than the code under test.
type stubSigner struct {
	proxy.UnimplementedSignerServer
}

func (stubSigner) Keys(context.Context, *proxy.KeysRequest) (*proxy.KeysReply, error) {
	return &proxy.KeysReply{
		// ed25519 sized, as the remote keyring checks.
		OffchainPublicKey:         make([]byte, ed25519.PublicKeySize),
		ConfigEncryptionPublicKey: make([]byte, ed25519.PublicKeySize),
		// An EVM onchain public key is an address.
		OnchainPublicKey:   make([]byte, 20),
		MaxSignatureLength: 65,
	}, nil
}
