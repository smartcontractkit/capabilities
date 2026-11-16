package remote

import (
	"context"
	"errors"
	"net"
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

	"github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr"
	"github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr/remote/pb"
)

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	evm, sol := ocr.NewSeededKeyBundle("evm-1", "evm", [32]byte{1}), ocr.NewSeededKeyBundle("sol-1", "solana", [32]byte{2})
	client := serve(t, ocr.StaticKeystore{evm, sol})

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

	_, err = client.GetKeyBundle(t.Context(), "missing")
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestDelegatesToBundle(t *testing.T) {
	t.Parallel()
	evm := ocr.NewSeededKeyBundle("evm-1", "evm", [32]byte{1})
	kb, err := serve(t, ocr.StaticKeystore{evm}).GetKeyBundle(t.Context(), "evm-1")
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

func TestServerErrors(t *testing.T) {
	t.Parallel()
	srv := &server{ks: ocr.StaticKeystore{ocr.NewSeededKeyBundle("evm-1", "evm", [32]byte{1})}}
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
				_, err := (&server{ks: failingKeystore{}}).ListKeyBundles(t.Context(), &pb.ListKeyBundlesRequest{})
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

func TestVerifyFailsClosed(t *testing.T) {
	t.Parallel()
	evm := ocr.NewSeededKeyBundle("evm-1", "evm", [32]byte{1})
	kb, err := serve(t, ocr.StaticKeystore{evm}).GetKeyBundle(t.Context(), "evm-1")
	require.NoError(t, err)

	rc := ocrtypes.ReportContext{}
	sig, err := evm.Sign(rc, ocrtypes.Report("report"))
	require.NoError(t, err)

	k := kb.Onchain.(*onchainKeyring)
	k.id = "missing"
	assert.False(t, k.Verify(evm.PublicKey(), rc, ocrtypes.Report("report"), sig))
}

func serve(t *testing.T, ks ocr.Keystore) ocr.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	s := grpc.NewServer()
	RegisterServer(s, ks)
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return NewClient(logger.Test(t), conn)
}

func assertSameBundle(t *testing.T, want ocr.KeyBundle, got ocr.KeyBundleClient) {
	t.Helper()
	assert.Equal(t, want.ID(), got.ID)
	assert.Equal(t, want.ChainType(), got.ChainType)
	assert.Equal(t, want.PublicKey(), got.Onchain.PublicKey())
	assert.Equal(t, want.MaxSignatureLength(), got.Onchain.MaxSignatureLength())
	assert.Equal(t, want.OffchainPublicKey(), got.Offchain.OffchainPublicKey())
	assert.Equal(t, want.ConfigEncryptionPublicKey(), got.Offchain.ConfigEncryptionPublicKey())
}

type failingKeystore struct{}

func (failingKeystore) List(context.Context, ...string) ([]ocr.KeyBundle, error) {
	return nil, errors.New("keystore is locked")
}

func (failingKeystore) Get(context.Context, string) (ocr.KeyBundle, error) {
	return nil, errors.New("keystore is locked")
}
