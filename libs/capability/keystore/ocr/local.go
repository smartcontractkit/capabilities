package ocr

import (
	"context"
	"fmt"
	"slices"
)

// StaticKeystore is a Keystore of the bundles it holds, in order.
type StaticKeystore []KeyBundle

var _ Keystore = StaticKeystore(nil)

func (ks StaticKeystore) List(_ context.Context, chainTypes ...string) ([]KeyBundle, error) {
	var kbs []KeyBundle
	for _, kb := range ks {
		if len(chainTypes) == 0 || slices.Contains(chainTypes, kb.ChainType()) {
			kbs = append(kbs, kb)
		}
	}

	return kbs, nil
}

func (ks StaticKeystore) Get(_ context.Context, id string) (KeyBundle, error) {
	for _, kb := range ks {
		if kb.ID() == id {
			return kb, nil
		}
	}

	return nil, fmt.Errorf("%s: %w", id, ErrKeyBundleNotFound)
}

// NewLocalClient is a Client of a Keystore in this process.
func NewLocalClient(ks Keystore) Client {
	return localClient{ks: ks}
}

type localClient struct {
	ks Keystore
}

func (c localClient) ListKeyBundles(ctx context.Context, chainTypes ...string) ([]KeyBundleClient, error) {
	kbs, err := c.ks.List(ctx, chainTypes...)
	if err != nil {
		return nil, err
	}

	clients := make([]KeyBundleClient, len(kbs))
	for i, kb := range kbs {
		clients[i] = toClient(kb)
	}

	return clients, nil
}

func (c localClient) GetKeyBundle(ctx context.Context, id string) (KeyBundleClient, error) {
	kb, err := c.ks.Get(ctx, id)
	if err != nil {
		return KeyBundleClient{}, err
	}

	return toClient(kb), nil
}

func toClient(kb KeyBundle) KeyBundleClient {
	return KeyBundleClient{ID: kb.ID(), ChainType: kb.ChainType(), Onchain: kb, Offchain: kb}
}
