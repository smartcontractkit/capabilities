package remote

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr"
	"github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr/remote/pb"
)

// callTimeout bounds a keyring call to the node. libocr's keyrings take no context, so the client makes its own.
const callTimeout = 10 * time.Second

// NewClient is a client of the OCRKeystore service served on conn. Public keys and lengths are read once; signing,
// verification and Diffie-Hellman go to the server.
func NewClient(lggr logger.Logger, conn grpc.ClientConnInterface) ocr.Client {
	return &client{lggr: logger.Named(lggr, "OCRKeystoreClient"), client: pb.NewOCRKeystoreClient(conn)}
}

type client struct {
	lggr   logger.Logger
	client pb.OCRKeystoreClient
}

func (c *client) ListKeyBundles(ctx context.Context, chainTypes ...string) ([]ocr.KeyBundleClient, error) {
	reply, err := c.client.ListKeyBundles(ctx, &pb.ListKeyBundlesRequest{ChainTypes: chainTypes})
	if err != nil {
		return nil, fmt.Errorf("failed to list key bundles: %w", err)
	}

	kbs := make([]ocr.KeyBundleClient, len(reply.KeyBundles))
	for i, kb := range reply.KeyBundles {
		if kbs[i], err = c.keyBundle(kb); err != nil {
			return nil, err
		}
	}

	return kbs, nil
}

func (c *client) GetKeyBundle(ctx context.Context, id string) (ocr.KeyBundleClient, error) {
	reply, err := c.client.GetKeyBundle(ctx, &pb.GetKeyBundleRequest{Id: id})
	if err != nil {
		return ocr.KeyBundleClient{}, fmt.Errorf("failed to get key bundle %s: %w", id, err)
	}

	return c.keyBundle(reply.KeyBundle)
}

func (c *client) keyBundle(kb *pb.KeyBundle) (ocr.KeyBundleClient, error) {
	offchain := &offchainKeyring{client: c.client, id: kb.GetId()}
	if len(kb.GetOffchainPublicKey()) != len(offchain.offchainPublicKey) {
		return ocr.KeyBundleClient{}, fmt.Errorf("key bundle %s: offchain public key is %d bytes, want %d",
			kb.GetId(), len(kb.GetOffchainPublicKey()), len(offchain.offchainPublicKey))
	}

	if len(kb.GetConfigEncryptionPublicKey()) != len(offchain.configEncryptionPublicKey) {
		return ocr.KeyBundleClient{}, fmt.Errorf("key bundle %s: config encryption public key is %d bytes, want %d",
			kb.GetId(), len(kb.GetConfigEncryptionPublicKey()), len(offchain.configEncryptionPublicKey))
	}

	copy(offchain.offchainPublicKey[:], kb.GetOffchainPublicKey())
	copy(offchain.configEncryptionPublicKey[:], kb.GetConfigEncryptionPublicKey())
	return ocr.KeyBundleClient{
		ID:        kb.GetId(),
		ChainType: kb.GetChainType(),
		Onchain: &onchainKeyring{
			lggr:               logger.With(c.lggr, "keyBundleID", kb.GetId()),
			client:             c.client,
			id:                 kb.GetId(),
			publicKey:          kb.GetOnchainPublicKey(),
			maxSignatureLength: int(kb.GetOnchainMaxSignatureLength()),
		},
		Offchain: offchain,
	}, nil
}

var _ ocrtypes.OnchainKeyring = (*onchainKeyring)(nil)

type onchainKeyring struct {
	lggr               logger.Logger
	client             pb.OCRKeystoreClient
	id                 string
	publicKey          ocrtypes.OnchainPublicKey
	maxSignatureLength int
}

func (k *onchainKeyring) PublicKey() ocrtypes.OnchainPublicKey { return k.publicKey }

func (k *onchainKeyring) Sign(rc ocrtypes.ReportContext, report ocrtypes.Report) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	reply, err := k.client.SignOnchain(ctx, &pb.SignOnchainRequest{
		KeyBundleId:   k.id,
		ReportContext: reportContextToPB(rc),
		Report:        report,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to sign report with key bundle %s: %w", k.id, err)
	}

	return reply.Signature, nil
}

// Verify reports a failed call as an invalid signature, since libocr's Verify has no error to return. The failure is
// logged so it is not mistaken for a bad signature.
func (k *onchainKeyring) Verify(publicKey ocrtypes.OnchainPublicKey, rc ocrtypes.ReportContext, report ocrtypes.Report,
	signature []byte) bool {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	reply, err := k.client.VerifyOnchain(ctx, &pb.VerifyOnchainRequest{
		KeyBundleId:   k.id,
		PublicKey:     publicKey,
		ReportContext: reportContextToPB(rc),
		Report:        report,
		Signature:     signature,
	})
	if err != nil {
		k.lggr.Errorw("Failed to verify report signature on the node", "err", err)
		return false
	}

	return reply.Valid
}

func (k *onchainKeyring) MaxSignatureLength() int { return k.maxSignatureLength }

var _ ocrtypes.OffchainKeyring = (*offchainKeyring)(nil)

type offchainKeyring struct {
	client                    pb.OCRKeystoreClient
	id                        string
	offchainPublicKey         ocrtypes.OffchainPublicKey
	configEncryptionPublicKey ocrtypes.ConfigEncryptionPublicKey
}

func (k *offchainKeyring) OffchainSign(msg []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	reply, err := k.client.SignOffchain(ctx, &pb.SignOffchainRequest{KeyBundleId: k.id, Message: msg})
	if err != nil {
		return nil, fmt.Errorf("failed to sign offchain with key bundle %s: %w", k.id, err)
	}

	return reply.Signature, nil
}

func (k *offchainKeyring) ConfigDiffieHellman(point [32]byte) ([32]byte, error) {
	var secret [32]byte
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	reply, err := k.client.ConfigDiffieHellman(ctx, &pb.ConfigDiffieHellmanRequest{KeyBundleId: k.id, Point: point[:]})
	if err != nil {
		return secret, fmt.Errorf("failed to compute the config shared secret with key bundle %s: %w", k.id, err)
	}

	if len(reply.SharedSecret) != len(secret) {
		return secret, fmt.Errorf("shared secret is %d bytes, want %d", len(reply.SharedSecret), len(secret))
	}

	copy(secret[:], reply.SharedSecret)
	return secret, nil
}

func (k *offchainKeyring) OffchainPublicKey() ocrtypes.OffchainPublicKey { return k.offchainPublicKey }

func (k *offchainKeyring) ConfigEncryptionPublicKey() ocrtypes.ConfigEncryptionPublicKey {
	return k.configEncryptionPublicKey
}
