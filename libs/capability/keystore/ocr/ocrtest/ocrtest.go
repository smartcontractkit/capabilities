// Package ocrtest is an in-memory ocr.Keystore for tests.
package ocrtest

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"

	"github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr"
)

// Keystore is the bundles it holds, in order.
type Keystore []ocr.KeyBundle

var _ ocr.Keystore = Keystore(nil)

func (ks Keystore) List(_ context.Context, chainTypes ...string) ([]ocr.KeyBundle, error) {
	var kbs []ocr.KeyBundle
	for _, kb := range ks {
		if len(chainTypes) == 0 || slices.Contains(chainTypes, kb.ChainType()) {
			kbs = append(kbs, kb)
		}
	}

	return kbs, nil
}

func (ks Keystore) Get(_ context.Context, id string) (ocr.KeyBundle, error) {
	for _, kb := range ks {
		if kb.ID() == id {
			return kb, nil
		}
	}

	return nil, fmt.Errorf("%s: %w", id, ocr.ErrKeyBundleNotFound)
}

// NewKeyBundle derives its keys from id, so two bundles with the same ID are the same keys.
func NewKeyBundle(id, chainType string) ocr.KeyBundle {
	return ocr.NewSeededKeyBundle(id, chainType, sha256.Sum256([]byte(id)))
}
