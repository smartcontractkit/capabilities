package ocr

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"strconv"
)

// EmbeddedChainType is the chain type of an embedded instance's bundle.
const EmbeddedChainType = "evm"

// embeddedSeedPrefix separates the keys derived here from any other keys derived from an index.
const embeddedSeedPrefix = "cre/capability/embed/ocr/"

// EmbeddedKeyBundle is the key bundle of instance of an embedded run, derived from its index so every instance can
// compute every other's public keys.
func EmbeddedKeyBundle(instance int) KeyBundle {
	i := strconv.Itoa(instance)
	return NewSeededKeyBundle("embedded-"+i, EmbeddedChainType, sha256.Sum256([]byte(embeddedSeedPrefix+i)))
}

// NewEmbeddedClient is the Client of instance of an embedded run, which holds only EmbeddedKeyBundle(instance).
func NewEmbeddedClient(instance int) Client {
	kb := EmbeddedKeyBundle(instance)
	return embeddedClient{kb: KeyBundleClient{ID: kb.ID(), ChainType: kb.ChainType(), Onchain: kb, Offchain: kb}}
}

type embeddedClient struct {
	kb KeyBundleClient
}

func (c embeddedClient) ListKeyBundles(_ context.Context, chainTypes ...string) ([]KeyBundleClient, error) {
	if len(chainTypes) > 0 && !slices.Contains(chainTypes, c.kb.ChainType) {
		return nil, nil
	}

	return []KeyBundleClient{c.kb}, nil
}

func (c embeddedClient) GetKeyBundle(_ context.Context, id string) (KeyBundleClient, error) {
	if id != c.kb.ID {
		return KeyBundleClient{}, fmt.Errorf("%s: %w", id, ErrKeyBundleNotFound)
	}

	return c.kb, nil
}
