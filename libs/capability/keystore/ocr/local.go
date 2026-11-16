package ocr

import (
	"context"
	"fmt"
)

// NewLocalClient serves ks's bundles without gRPC, for embedded runs and tests.
func NewLocalClient(ks Keystore) Client {
	return localClient{ks: ks}
}

type localClient struct {
	ks Keystore
}

func (c localClient) ListKeyBundles(ctx context.Context, chainTypes ...string) ([]KeyBundleClient, error) {
	kbs, err := c.ks.List(ctx, chainTypes...)
	if err != nil {
		return nil, fmt.Errorf("failed to list key bundles: %w", err)
	}

	clients := make([]KeyBundleClient, len(kbs))
	for i, kb := range kbs {
		clients[i] = localKeyBundle(kb)
	}

	return clients, nil
}

func (c localClient) GetKeyBundle(ctx context.Context, id string) (KeyBundleClient, error) {
	kb, err := c.ks.Get(ctx, id)
	if err != nil {
		return KeyBundleClient{}, fmt.Errorf("failed to get key bundle %s: %w", id, err)
	}

	return localKeyBundle(kb), nil
}

func localKeyBundle(kb KeyBundle) KeyBundleClient {
	return KeyBundleClient{ID: kb.ID(), ChainType: kb.ChainType(), Onchain: kb, Offchain: kb}
}
