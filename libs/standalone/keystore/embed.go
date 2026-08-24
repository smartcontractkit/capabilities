package keystore

import (
	"context"
	"errors"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"
	commonlogger "github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// Embedded builds the keystore instance i of an embedded run signs with.
//
// It is a parameter rather than something this package does, because a key is a
// chain's: deriving an EVM account means secp256k1 and an address, deriving a
// Solana one means something else, and neither belongs in the package that only
// knows how to ask another process for a signature. A capability passes the one
// its chain provides - see chainlink-evm's cre/evmchain for the EVM one.
//
// A capability with no answer to this passes nil, and an embedded run of it fails
// where it is resolved rather than at the first signature.
type Embedded func(instance int) (core.Keystore, error)

// embedded is one embedded instance's keystore: keys of its own, since embedding
// replaces the node it would otherwise borrow them from.
type embedded struct {
	lggr  commonlogger.Logger
	build Embedded
	index int
}

var _ standalone.BootstrapDependency[core.Keystore] = (*embedded)(nil)

func (d *embedded) Namespace() string { return namespace }

// Config is nothing at all: there is no address to dial, and the keys come from
// the instance index.
func (d *embedded) Config() any { return nil }

func (d *embedded) Dependencies() []standalone.BootstrapCommand { return nil }

// ForEmbedding returns the dependency of instance i, so an already-embedded
// dependency embedded again is instance i's rather than a copy of this one's.
func (d *embedded) ForEmbedding(i, _ int) standalone.BootstrapDependency[core.Keystore] {
	return &embedded{lggr: d.lggr, build: d.build, index: i}
}

func (d *embedded) Get(context.Context, standalone.CommonConfig) (core.Keystore, error) {
	if d.build == nil {
		return nil, errors.New("this binary has no keys of its own to run embedded with: it signs with the node's, and an embedded run has no node")
	}

	keystore, err := d.build(d.index)
	if err != nil {
		return nil, fmt.Errorf("failed to build the keys of instance %d: %w", d.index, err)
	}

	accounts, err := keystore.Accounts(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to read the accounts of instance %d: %w", d.index, err)
	}
	d.lggr.Infow("Signing with keys of this instance's own", "instance", d.index, "accounts", accounts)

	return keystore, nil
}
