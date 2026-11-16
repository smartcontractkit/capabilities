// Package ocrtest is OCR key bundles for tests.
package ocrtest

import (
	"crypto/sha256"

	"github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr"
)

// NewKeyBundle derives its keys from id, so two bundles with the same ID are the same keys.
func NewKeyBundle(id, chainType string) ocr.KeyBundle {
	return ocr.NewSeededKeyBundle(id, chainType, sha256.Sum256([]byte(id)))
}
