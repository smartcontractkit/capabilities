package keystore

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"net"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/libs/capability/keystore/pb"
)

func TestOCRKeystore_RoundTrip(t *testing.T) {
	t.Parallel()
	evm, sol := newFakeBundle("evm-1", "evm"), newFakeBundle("sol-1", "solana")
	client := serve(t, Keystores{OCR: fakeKeystore{evm, sol}})

	all, err := client.ListKeyBundles(t.Context())
	require.NoError(t, err)
	require.Len(t, all, 2)
	assertSameBundle(t, evm, all[0])
	assertSameBundle(t, sol, all[1])

	solana, err := client.ListKeyBundles(t.Context(), "solana")
	require.NoError(t, err)
	require.Len(t, solana, 1)
	assert.Equal(t, "sol-1", solana[0].ID)

	kb, err := client.GetKeyBundle(t.Context(), "evm-1")
	require.NoError(t, err)
	assertSameBundle(t, evm, kb)
}

func TestOCRKeystore_DelegatesToBundle(t *testing.T) {
	t.Parallel()
	evm := newFakeBundle("evm-1", "evm")
	kb, err := serve(t, Keystores{OCR: fakeKeystore{evm}}).GetKeyBundle(t.Context(), "evm-1")
	require.NoError(t, err)

	rc := ocrtypes.ReportContext{
		ConfigDigest: ocrtypes.ConfigDigest{1, 2, 3}, Epoch: 7, Round: 255,
		ExtraHash: [32]byte{9},
	}
	report := ocrtypes.Report("report")

	sig, err := kb.Onchain.Sign(rc, report)
	require.NoError(t, err)
	want, err := evm.Sign(rc, report)
	require.NoError(t, err)
	assert.Equal(t, want, sig)
	assert.True(t, kb.Onchain.Verify(evm.PublicKey(), rc, report, sig))
	assert.False(t, kb.Onchain.Verify(evm.PublicKey(), rc, ocrtypes.Report("other"), sig))

	offchainSig, err := kb.Offchain.OffchainSign([]byte("msg"))
	require.NoError(t, err)
	wantOffchainSig, err := evm.OffchainSign([]byte("msg"))
	require.NoError(t, err)
	assert.Equal(t, wantOffchainSig, offchainSig)

	secret, err := kb.Offchain.ConfigDiffieHellman([32]byte{4})
	require.NoError(t, err)
	wantSecret, err := evm.ConfigDiffieHellman([32]byte{4})
	require.NoError(t, err)
	assert.Equal(t, wantSecret, secret)
}

func TestOCRKeystore_Errors(t *testing.T) {
	t.Parallel()
	srv := &ocrKeystoreServer{lggr: logger.Test(t), ks: fakeKeystore{newFakeBundle("evm-1", "evm")}}
	validRC := reportContextToPB(ocrtypes.ReportContext{})

	for name, tc := range map[string]struct {
		call func() error
		want codes.Code
	}{
		"unknown bundle": {
			call: func() error {
				_, err := srv.GetKeyBundle(t.Context(), &pb.GetKeyBundleRequest{Id: "missing"})
				return err
			},
			want: codes.NotFound,
		},
		"no bundle ID": {
			call: func() error {
				_, err := srv.SignOffchain(t.Context(), &pb.SignOffchainRequest{Message: []byte("msg")})
				return err
			},
			want: codes.InvalidArgument,
		},
		"no report context": {
			call: func() error {
				_, err := srv.SignOnchain(t.Context(), &pb.SignOnchainRequest{KeyBundleId: "evm-1"})
				return err
			},
			want: codes.InvalidArgument,
		},
		"short config digest": {
			call: func() error {
				_, err := srv.SignOnchain(t.Context(), &pb.SignOnchainRequest{
					KeyBundleId:   "evm-1",
					ReportContext: &pb.ReportContext{ConfigDigest: []byte{1}, ExtraHash: make([]byte, 32)},
				})
				return err
			},
			want: codes.InvalidArgument,
		},
		"round out of range": {
			call: func() error {
				_, err := srv.VerifyOnchain(t.Context(), &pb.VerifyOnchainRequest{
					KeyBundleId:   "evm-1",
					ReportContext: &pb.ReportContext{ConfigDigest: make([]byte, 32), Round: 256, ExtraHash: make([]byte, 32)},
				})
				return err
			},
			want: codes.InvalidArgument,
		},
		"short point": {
			call: func() error {
				_, err := srv.ConfigDiffieHellman(t.Context(), &pb.ConfigDiffieHellmanRequest{KeyBundleId: "evm-1", Point: []byte{1}})
				return err
			},
			want: codes.InvalidArgument,
		},
		"sign with unknown bundle": {
			call: func() error {
				_, err := srv.SignOnchain(t.Context(), &pb.SignOnchainRequest{KeyBundleId: "missing", ReportContext: validRC})
				return err
			},
			want: codes.NotFound,
		},
		"keystore failure": {
			call: func() error {
				_, err := (&ocrKeystoreServer{lggr: logger.Test(t), ks: failingKeystore{}}).ListKeyBundles(t.Context(), &pb.ListKeyBundlesRequest{})
				return err
			},
			want: codes.Internal,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, status.Code(tc.call()))
		})
	}
}

func TestOCRKeystore_VerifyFailsClosed(t *testing.T) {
	t.Parallel()
	evm := newFakeBundle("evm-1", "evm")
	kb, err := serve(t, Keystores{OCR: fakeKeystore{evm}}).GetKeyBundle(t.Context(), "evm-1")
	require.NoError(t, err)

	rc := ocrtypes.ReportContext{}
	sig, err := evm.Sign(rc, ocrtypes.Report("report"))
	require.NoError(t, err)

	k := kb.Onchain.(*remoteOnchainKeyring)
	k.id = "missing"
	assert.False(t, k.Verify(evm.PublicKey(), rc, ocrtypes.Report("report"), sig))
}

func TestLocalOCRKeystoreClient(t *testing.T) {
	t.Parallel()
	evm, sol := newFakeBundle("evm-1", "evm"), newFakeBundle("sol-1", "solana")
	client := NewLocalOCRKeystoreClient(fakeKeystore{evm, sol})

	all, err := client.ListKeyBundles(t.Context())
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Same(t, evm, all[0].Onchain)
	assert.Same(t, evm, all[0].Offchain)

	kb, err := client.GetKeyBundle(t.Context(), "sol-1")
	require.NoError(t, err)
	assertSameBundle(t, sol, kb)

	_, err = client.GetKeyBundle(t.Context(), "missing")
	require.ErrorIs(t, err, ErrKeyBundleNotFound)
}

func TestRegisterServers_SkipsNilKeystores(t *testing.T) {
	t.Parallel()
	s := grpc.NewServer()
	RegisterServers(s, logger.Test(t), Keystores{})
	assert.Empty(t, s.GetServiceInfo())

	RegisterServers(s, logger.Test(t), Keystores{OCR: fakeKeystore{}})
	assert.Contains(t, s.GetServiceInfo(), pb.OCRKeystore_ServiceDesc.ServiceName)
}

func serve(t *testing.T, ks Keystores) OCRKeystoreClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	s := grpc.NewServer()
	RegisterServers(s, logger.Test(t), ks)
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return NewOCRKeystoreClient(logger.Test(t), conn)
}

func assertSameBundle(t *testing.T, want OCRKeyBundle, got OCRKeyBundleClient) {
	t.Helper()
	assert.Equal(t, want.ID(), got.ID)
	assert.Equal(t, want.ChainType(), got.ChainType)
	assert.Equal(t, want.PublicKey(), got.Onchain.PublicKey())
	assert.Equal(t, want.MaxSignatureLength(), got.Onchain.MaxSignatureLength())
	assert.Equal(t, want.OffchainPublicKey(), got.Offchain.OffchainPublicKey())
	assert.Equal(t, want.ConfigEncryptionPublicKey(), got.Offchain.ConfigEncryptionPublicKey())
}

type fakeKeystore []*fakeBundle

func (ks fakeKeystore) List(_ context.Context, chainTypes ...string) ([]OCRKeyBundle, error) {
	var kbs []OCRKeyBundle
	for _, kb := range ks {
		if len(chainTypes) == 0 || slices.Contains(chainTypes, kb.chainType) {
			kbs = append(kbs, kb)
		}
	}

	return kbs, nil
}

func (ks fakeKeystore) Get(_ context.Context, id string) (OCRKeyBundle, error) {
	for _, kb := range ks {
		if kb.id == id {
			return kb, nil
		}
	}

	return nil, fmt.Errorf("%s: %w", id, ErrKeyBundleNotFound)
}

type failingKeystore struct{}

func (failingKeystore) List(context.Context, ...string) ([]OCRKeyBundle, error) {
	return nil, fmt.Errorf("keystore is locked")
}

func (failingKeystore) Get(context.Context, string) (OCRKeyBundle, error) {
	return nil, fmt.Errorf("keystore is locked")
}

// fakeBundle signs with ed25519 keys derived from its ID. Its config Diffie-Hellman is a hash, which is enough to
// tell whether a call reached it.
type fakeBundle struct {
	id, chainType string
	onchain       ed25519.PrivateKey
	offchain      ed25519.PrivateKey
	configSecret  [32]byte
}

var _ OCRKeyBundle = (*fakeBundle)(nil)

func newFakeBundle(id, chainType string) *fakeBundle {
	onchainSeed := sha256.Sum256([]byte(id + "/onchain"))
	offchainSeed := sha256.Sum256([]byte(id + "/offchain"))
	return &fakeBundle{
		id:           id,
		chainType:    chainType,
		onchain:      ed25519.NewKeyFromSeed(onchainSeed[:]),
		offchain:     ed25519.NewKeyFromSeed(offchainSeed[:]),
		configSecret: sha256.Sum256([]byte(id + "/config")),
	}
}

func (b *fakeBundle) ID() string        { return b.id }
func (b *fakeBundle) ChainType() string { return b.chainType }

func (b *fakeBundle) PublicKey() ocrtypes.OnchainPublicKey {
	return ocrtypes.OnchainPublicKey(b.onchain.Public().(ed25519.PublicKey))
}

func (b *fakeBundle) Sign(rc ocrtypes.ReportContext, report ocrtypes.Report) ([]byte, error) {
	return ed25519.Sign(b.onchain, sigData(rc, report)), nil
}

func (b *fakeBundle) Verify(publicKey ocrtypes.OnchainPublicKey, rc ocrtypes.ReportContext, report ocrtypes.Report,
	signature []byte) bool {
	return len(publicKey) == ed25519.PublicKeySize && ed25519.Verify(ed25519.PublicKey(publicKey), sigData(rc, report), signature)
}

func (b *fakeBundle) MaxSignatureLength() int { return ed25519.SignatureSize }

func (b *fakeBundle) OffchainSign(msg []byte) ([]byte, error) {
	return ed25519.Sign(b.offchain, msg), nil
}

func (b *fakeBundle) ConfigDiffieHellman(point [32]byte) ([32]byte, error) {
	return sha256.Sum256(append(b.configSecret[:], point[:]...)), nil
}

func (b *fakeBundle) OffchainPublicKey() ocrtypes.OffchainPublicKey {
	return ocrtypes.OffchainPublicKey(b.offchain.Public().(ed25519.PublicKey))
}

func (b *fakeBundle) ConfigEncryptionPublicKey() ocrtypes.ConfigEncryptionPublicKey {
	return ocrtypes.ConfigEncryptionPublicKey(sha256.Sum256(b.configSecret[:]))
}

func sigData(rc ocrtypes.ReportContext, report ocrtypes.Report) []byte {
	h := sha256.New()
	h.Write(rc.ConfigDigest[:])
	h.Write([]byte{byte(rc.Epoch >> 24), byte(rc.Epoch >> 16), byte(rc.Epoch >> 8), byte(rc.Epoch), rc.Round})
	h.Write(rc.ExtraHash[:])
	h.Write(report)
	return h.Sum(nil)
}
