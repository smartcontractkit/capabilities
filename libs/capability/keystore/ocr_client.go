package keystore

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/libs/capability/keystore/pb"
)

// callTimeout bounds a keyring call to the node. libocr's keyrings take no context, so the client makes its own.
const callTimeout = 10 * time.Second

// OCRKeyBundleClient is one of the node's bundles. Public keys and lengths are local; signing and verification are
// remote.
type OCRKeyBundleClient struct {
	ID        string
	ChainType string
	Onchain   ocrtypes.OnchainKeyring
	Offchain  ocrtypes.OffchainKeyring
}

// OCRKeystoreClient fetches the node's OCR2 key bundles.
type OCRKeystoreClient interface {
	// ListKeyBundles returns the bundles of the given chain types, or every bundle when none are given.
	ListKeyBundles(ctx context.Context, chainTypes ...string) ([]OCRKeyBundleClient, error)
	GetKeyBundle(ctx context.Context, id string) (OCRKeyBundleClient, error)
}

// NewOCRKeystoreClient is a client of the OCRKeystore service the node serves on conn.
func NewOCRKeystoreClient(lggr logger.Logger, conn grpc.ClientConnInterface) OCRKeystoreClient {
	return &ocrKeystoreClient{lggr: logger.Named(lggr, "OCRKeystoreClient"), client: pb.NewOCRKeystoreClient(conn)}
}

type ocrKeystoreClient struct {
	lggr   logger.Logger
	client pb.OCRKeystoreClient
}

func (c *ocrKeystoreClient) ListKeyBundles(ctx context.Context, chainTypes ...string) ([]OCRKeyBundleClient, error) {
	reply, err := c.client.ListKeyBundles(ctx, &pb.ListKeyBundlesRequest{ChainTypes: chainTypes})
	if err != nil {
		return nil, fmt.Errorf("failed to list key bundles: %w", err)
	}

	kbs := make([]OCRKeyBundleClient, len(reply.KeyBundles))
	for i, kb := range reply.KeyBundles {
		if kbs[i], err = c.keyBundle(kb); err != nil {
			return nil, err
		}
	}

	return kbs, nil
}

func (c *ocrKeystoreClient) GetKeyBundle(ctx context.Context, id string) (OCRKeyBundleClient, error) {
	reply, err := c.client.GetKeyBundle(ctx, &pb.GetKeyBundleRequest{Id: id})
	if err != nil {
		return OCRKeyBundleClient{}, fmt.Errorf("failed to get key bundle %s: %w", id, err)
	}

	return c.keyBundle(reply.KeyBundle)
}

func (c *ocrKeystoreClient) keyBundle(kb *pb.KeyBundle) (OCRKeyBundleClient, error) {
	offchain := &remoteOffchainKeyring{client: c.client, id: kb.GetId()}
	if len(kb.GetOffchainPublicKey()) != len(offchain.offchainPublicKey) {
		return OCRKeyBundleClient{}, fmt.Errorf("key bundle %s: offchain public key is %d bytes, want %d",
			kb.GetId(), len(kb.GetOffchainPublicKey()), len(offchain.offchainPublicKey))
	}

	if len(kb.GetConfigEncryptionPublicKey()) != len(offchain.configEncryptionPublicKey) {
		return OCRKeyBundleClient{}, fmt.Errorf("key bundle %s: config encryption public key is %d bytes, want %d",
			kb.GetId(), len(kb.GetConfigEncryptionPublicKey()), len(offchain.configEncryptionPublicKey))
	}

	copy(offchain.offchainPublicKey[:], kb.GetOffchainPublicKey())
	copy(offchain.configEncryptionPublicKey[:], kb.GetConfigEncryptionPublicKey())
	return OCRKeyBundleClient{
		ID:        kb.GetId(),
		ChainType: kb.GetChainType(),
		Onchain: &remoteOnchainKeyring{
			lggr:               logger.With(c.lggr, "keyBundleID", kb.GetId()),
			client:             c.client,
			id:                 kb.GetId(),
			publicKey:          kb.GetOnchainPublicKey(),
			maxSignatureLength: int(kb.GetOnchainMaxSignatureLength()),
		},
		Offchain: offchain,
	}, nil
}

var _ ocrtypes.OnchainKeyring = (*remoteOnchainKeyring)(nil)

type remoteOnchainKeyring struct {
	lggr               logger.Logger
	client             pb.OCRKeystoreClient
	id                 string
	publicKey          ocrtypes.OnchainPublicKey
	maxSignatureLength int
}

func (k *remoteOnchainKeyring) PublicKey() ocrtypes.OnchainPublicKey { return k.publicKey }

func (k *remoteOnchainKeyring) Sign(rc ocrtypes.ReportContext, report ocrtypes.Report) ([]byte, error) {
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
func (k *remoteOnchainKeyring) Verify(publicKey ocrtypes.OnchainPublicKey, rc ocrtypes.ReportContext, report ocrtypes.Report,
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

func (k *remoteOnchainKeyring) MaxSignatureLength() int { return k.maxSignatureLength }

var _ ocrtypes.OffchainKeyring = (*remoteOffchainKeyring)(nil)

type remoteOffchainKeyring struct {
	client                    pb.OCRKeystoreClient
	id                        string
	offchainPublicKey         ocrtypes.OffchainPublicKey
	configEncryptionPublicKey ocrtypes.ConfigEncryptionPublicKey
}

func (k *remoteOffchainKeyring) OffchainSign(msg []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	reply, err := k.client.SignOffchain(ctx, &pb.SignOffchainRequest{KeyBundleId: k.id, Message: msg})
	if err != nil {
		return nil, fmt.Errorf("failed to sign offchain with key bundle %s: %w", k.id, err)
	}

	return reply.Signature, nil
}

func (k *remoteOffchainKeyring) ConfigDiffieHellman(point [32]byte) ([32]byte, error) {
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

func (k *remoteOffchainKeyring) OffchainPublicKey() ocrtypes.OffchainPublicKey { return k.offchainPublicKey }

func (k *remoteOffchainKeyring) ConfigEncryptionPublicKey() ocrtypes.ConfigEncryptionPublicKey {
	return k.configEncryptionPublicKey
}

// NewLocalOCRKeystoreClient serves ks's bundles without gRPC, for embedded runs and tests.
func NewLocalOCRKeystoreClient(ks OCRKeystore) OCRKeystoreClient {
	return localOCRKeystoreClient{ks: ks}
}

type localOCRKeystoreClient struct {
	ks OCRKeystore
}

func (c localOCRKeystoreClient) ListKeyBundles(ctx context.Context, chainTypes ...string) ([]OCRKeyBundleClient, error) {
	kbs, err := c.ks.List(ctx, chainTypes...)
	if err != nil {
		return nil, fmt.Errorf("failed to list key bundles: %w", err)
	}

	clients := make([]OCRKeyBundleClient, len(kbs))
	for i, kb := range kbs {
		clients[i] = localKeyBundle(kb)
	}

	return clients, nil
}

func (c localOCRKeystoreClient) GetKeyBundle(ctx context.Context, id string) (OCRKeyBundleClient, error) {
	kb, err := c.ks.Get(ctx, id)
	if err != nil {
		return OCRKeyBundleClient{}, fmt.Errorf("failed to get key bundle %s: %w", id, err)
	}

	return localKeyBundle(kb), nil
}

func localKeyBundle(kb OCRKeyBundle) OCRKeyBundleClient {
	return OCRKeyBundleClient{ID: kb.ID(), ChainType: kb.ChainType(), Onchain: kb, Offchain: kb}
}
