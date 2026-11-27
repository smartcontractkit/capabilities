// Package ocr is the node's OCR2 key bundles as a capability process uses them: the node implements Keystore, and the
// process signs through a Client, which hands each bundle back as libocr keyrings. See the remote package for serving
// a Keystore over gRPC.
package ocr

import (
	"context"
	"errors"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

// ErrKeyBundleNotFound is what a Keystore returns, wrapped or not, for a bundle it does not have.
var ErrKeyBundleNotFound = errors.New("key bundle not found")

// KeyBundle is one of the node's OCR2 key bundles.
type KeyBundle interface {
	ID() string
	// ChainType is the bundle's corekeys.ChainType, e.g. "evm".
	ChainType() string
	ocrtypes.OnchainKeyring
	ocrtypes.OffchainKeyring
}

// Keystore is the node's OCR2 key bundles.
type Keystore interface {
	// List returns the bundles of the given chain types, or every bundle when none are given.
	List(ctx context.Context, chainTypes ...string) ([]KeyBundle, error)
	Get(ctx context.Context, id string) (KeyBundle, error)
}

// KeyBundleClient is one of the node's bundles as a process uses it.
type KeyBundleClient struct {
	ID        string
	ChainType string
	Onchain   ocrtypes.OnchainKeyring
	Offchain  ocrtypes.OffchainKeyring
}

// Client fetches the node's OCR2 key bundles.
type Client interface {
	// ListKeyBundles returns the bundles of the given chain types, or every bundle when none are given.
	ListKeyBundles(ctx context.Context, chainTypes ...string) ([]KeyBundleClient, error)
	GetKeyBundle(ctx context.Context, id string) (KeyBundleClient, error)
}
