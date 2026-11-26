package ocr

import (
	"fmt"
	"sync"

	"github.com/smartcontractkit/chainlink-common/keystore/corekeys"
)

// embedded holds the key bundles of an embedded run's instances, made the first time they are asked for.
var embedded = struct {
	mu      sync.Mutex
	bundles map[int][]KeyBundle
}{bundles: map[int][]KeyBundle{}}

// EmbeddedKeyBundles are the key bundles of instance of an embedded run, one for each chain type a report may name,
// so it can sign any report. Every instance runs in one process, so each instance's bundles are made once and are
// the same wherever they are asked for: in its keystore and in every instance's OCR config.
func EmbeddedKeyBundles(instance int) ([]KeyBundle, error) {
	embedded.mu.Lock()
	defer embedded.mu.Unlock()
	if bundles, ok := embedded.bundles[instance]; ok {
		return bundles, nil
	}

	bundles := make([]KeyBundle, 0, len(corekeys.SupportedChainTypes))
	for _, chainType := range corekeys.SupportedChainTypes {
		bundle, err := NewKeyBundle(string(chainType))
		if err != nil {
			return nil, fmt.Errorf("failed to make the %s key bundle of instance %d: %w", chainType, instance, err)
		}

		bundles = append(bundles, bundle)
	}

	embedded.bundles[instance] = bundles
	return bundles, nil
}

// NewEmbeddedClient is the Client of instance of an embedded run, which holds EmbeddedKeyBundles(instance).
func NewEmbeddedClient(instance int) (Client, error) {
	bundles, err := EmbeddedKeyBundles(instance)
	if err != nil {
		return nil, err
	}

	return NewLocalClient(StaticKeystore(bundles)), nil
}
