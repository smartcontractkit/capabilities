package ocr

import (
	"crypto/sha256"
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
	return NewLocalClient(StaticKeystore{EmbeddedKeyBundle(instance)})
}
