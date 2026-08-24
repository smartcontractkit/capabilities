package ocr

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/crypto/curve25519"
	"google.golang.org/grpc"

	"github.com/smartcontractkit/chainlink-common/keystore/corekeys/ocr2key"

	"github.com/smartcontractkit/chainlink-protos/cre/impl/proxy"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

// Keyrings are what an oracle signs with: its protocol messages, and its
// reports.
//
// They are resolved alongside the networking because they come from the same
// place. A process hosting the node's peer holds the node's keys; one delegating
// to it holds neither, and signs by asking - see remoteKeyring, and the Signer
// service the host serves.
type Keyrings struct {
	Offchain ocrtypes.OffchainKeyring
	Onchain  ocr3types.OnchainKeyring[[]byte]
}

// remoteKeyring signs by asking the process that holds the key.
//
// It is both keyrings at once because they are one key bundle on the other side,
// and splitting them here would only mean fetching the same public keys twice.
//
// The public halves are read once, when this is built: an oracle needs them to
// say who it is, they cannot change while it runs, and asking per call would put
// a round trip on operations that are pure local arithmetic everywhere else.
type remoteKeyring struct {
	client proxy.SignerClient

	offchainPublicKey ocrtypes.OffchainPublicKey
	configPublicKey   ocrtypes.ConfigEncryptionPublicKey
	onchainPublicKey  ocrtypes.OnchainPublicKey
	maxSignatureLen   int
}

var (
	_ ocrtypes.OffchainKeyring         = (*remoteKeyring)(nil)
	_ ocr3types.OnchainKeyring[[]byte] = (*remoteKeyring)(nil)
)

// newRemoteKeyring dials the Signer service on conn and reads its public keys.
func newRemoteKeyring(ctx context.Context, conn grpc.ClientConnInterface) (*remoteKeyring, error) {
	client := proxy.NewSignerClient(conn)

	keys, err := client.Keys(ctx, &proxy.KeysRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to read the signer's public keys: %w", err)
	}

	// Taken as it is served, not re-encoded: an onchain keyring's public key is
	// already the multichain form a configuration lists it as (see
	// ocr2key.NewOCR3Keyring), and the signer holds the keyring. Encoding it again
	// here would announce a key wrapped twice, which no configuration carries, and
	// which is what an oracle whose offchain key matches but whose onchain key does
	// not looks like.
	//
	// Checked rather than trusted, because the difference is invisible until a DON
	// refuses to recognise this oracle: what comes back has to decode as the form it
	// claims to be.
	onchainPublicKey := ocrtypes.OnchainPublicKey(keys.GetOnchainPublicKey())
	if _, err := ocr2key.UnmarshalMultichainPublicKey(onchainPublicKey); err != nil {
		return nil, fmt.Errorf("the signer's onchain public key is not the encoded form a configuration lists: %w", err)
	}

	k := &remoteKeyring{
		client:           client,
		onchainPublicKey: onchainPublicKey,
		maxSignatureLen:  int(keys.GetMaxSignatureLength()),
	}

	// Both are fixed-size, and a wrong length here would otherwise surface as an
	// oracle no one recognises rather than as a bad reply.
	if got := len(keys.GetOffchainPublicKey()); got != len(k.offchainPublicKey) {
		return nil, fmt.Errorf("signer returned a %d byte offchain public key, want %d", got, len(k.offchainPublicKey))
	}
	copy(k.offchainPublicKey[:], keys.GetOffchainPublicKey())

	if got := len(keys.GetConfigEncryptionPublicKey()); got != len(k.configPublicKey) {
		return nil, fmt.Errorf("signer returned a %d byte config encryption public key, want %d", got, len(k.configPublicKey))
	}
	copy(k.configPublicKey[:], keys.GetConfigEncryptionPublicKey())

	return k, nil
}

func (k *remoteKeyring) OffchainSign(msg []byte) ([]byte, error) {
	reply, err := k.client.SignOffchain(context.Background(), &proxy.SignOffchainRequest{Message: msg})
	if err != nil {
		return nil, fmt.Errorf("failed to sign a protocol message: %w", err)
	}
	return reply.GetSignature(), nil
}

func (k *remoteKeyring) ConfigDiffieHellman(point [curve25519.PointSize]byte) ([curve25519.PointSize]byte, error) {
	var shared [curve25519.PointSize]byte

	reply, err := k.client.ConfigDiffieHellman(context.Background(), &proxy.ConfigDiffieHellmanRequest{Point: point[:]})
	if err != nil {
		return shared, fmt.Errorf("failed to compute a config shared secret: %w", err)
	}
	if got := len(reply.GetSharedSecret()); got != len(shared) {
		return shared, fmt.Errorf("signer returned a %d byte shared secret, want %d", got, len(shared))
	}

	copy(shared[:], reply.GetSharedSecret())
	return shared, nil
}

func (k *remoteKeyring) OffchainPublicKey() ocrtypes.OffchainPublicKey {
	return k.offchainPublicKey
}

func (k *remoteKeyring) ConfigEncryptionPublicKey() ocrtypes.ConfigEncryptionPublicKey {
	return k.configPublicKey
}

func (k *remoteKeyring) PublicKey() ocrtypes.OnchainPublicKey {
	return k.onchainPublicKey
}

func (k *remoteKeyring) Sign(digest ocrtypes.ConfigDigest, seqNr uint64, report ocr3types.ReportWithInfo[[]byte]) ([]byte, error) {
	reply, err := k.client.SignReport(context.Background(), &proxy.SignReportRequest{
		ConfigDigest: digest[:],
		SeqNr:        seqNr,
		Report:       report.Report,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to sign a report: %w", err)
	}
	return reply.GetSignature(), nil
}

// Verify is done here rather than asked for. It takes the signer's public key as
// an argument, so it needs no secret, and every signature an oracle receives
// would otherwise cost a round trip.
func (k *remoteKeyring) Verify(
	publicKey ocrtypes.OnchainPublicKey,
	digest ocrtypes.ConfigDigest,
	seqNr uint64,
	report ocr3types.ReportWithInfo[[]byte],
	signature []byte,
) bool {
	return verifyReport(publicKey, digest, seqNr, report.Report, signature)
}

func (k *remoteKeyring) MaxSignatureLength() int { return k.maxSignatureLen }

// verifyReport checks a signature the way an EVM key bundle makes one: the
// signed blob is the report bound to the round it belongs to, and the signature
// is secp256k1 over it.
//
// publicKey is a peer's key as the config carries it, so the EVM entry is taken
// out of it first - see multichain.go.
//
// Only EVM, deliberately. A signature has to be verified against the scheme the
// signer used, and guessing wrong would accept nothing rather than accept the
// wrong thing - but it would do so silently, so the process holding the key
// refuses to serve a bundle of another chain type instead of letting this find
// out one signature at a time.
func verifyReport(
	publicKey ocrtypes.OnchainPublicKey,
	digest ocrtypes.ConfigDigest,
	seqNr uint64,
	report ocrtypes.Report,
	signature []byte,
) bool {
	key, err := ocr2key.OnchainPublicKeyFor(EVMFamily, publicKey)
	if err != nil {
		return false
	}
	return ocr2key.EvmVerifyBlob(key, ocr2key.ReportToSigData(ocr2key.OCR3ReportContext(digest, seqNr), report), signature)
}

// An OCR3 capability's members are registered with a multi-chain onchain public
// key: one length-prefixed entry per signing family rather than the bare key of
// whichever chain it happens to sign for. The codec is ocr2key's, shared with the
// node that writes those configs; what is here is the EVM-shaped view of it that a
// process delegating its signing needs.

// EVMFamily is the name the EVM entry is keyed by, and the name core's jobs give
// the EVM bundle in onchainSigningStrategy.config.
const EVMFamily = "evm"

// marshalEVMOnchainPublicKey encodes a bare EVM key as the single-family form.
func marshalEVMOnchainPublicKey(key ocrtypes.OnchainPublicKey) (ocrtypes.OnchainPublicKey, error) {
	if len(key) == 0 {
		return nil, errors.New("no evm onchain public key to encode")
	}
	return ocr2key.MarshalMultichainPublicKey(map[string]ocrtypes.OnchainPublicKey{EVMFamily: key})
}
