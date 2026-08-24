package simulated

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common"

	"github.com/smartcontractkit/capabilities/libs/standalone/ocr"
)

// signers are the oracles of an embedded run, as the forwarder lists them.
//
// They are the onchain halves of the same derived OCR keys the instances sign
// reports with, so the forwarder accepts exactly what this run produces and
// nothing else. An embedded instance's identity is its index, here as everywhere
// else in an embedded run.
func signers(instances int) ([]common.Address, error) {
	addresses := make([]common.Address, 0, instances)
	for instance := range instances {
		bundle, err := ocr.EmbeddedOCR2Bundle(instance)
		if err != nil {
			return nil, fmt.Errorf("failed to read the OCR key of instance %d: %w", instance, err)
		}
		// An EVM bundle's public key is the address its signatures recover to, which is
		// what the forwarder compares against.
		key := bundle.PublicKey()
		if len(key) != common.AddressLength {
			return nil, fmt.Errorf("the OCR key of instance %d is %d bytes, want an address", instance, len(key))
		}
		addresses = append(addresses, common.BytesToAddress(key))
	}
	return addresses, nil
}
