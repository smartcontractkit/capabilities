// Package registry keeps this binary's view of the CapabilitiesRegistry fresh and answerable: who
// the DONs are, which nodes belong to them, which capabilities they host, and which node this is.
//
// Where the registry actually lives is not this package's business - a Reader supplies whole
// snapshots from wherever it is written, and the on-chain one lives in chainlink-evm. What is here
// is the Syncer that keeps asking, and the Registry that serves what it finds over gRPC alongside
// the capabilities registered against it.
//
// Holding a snapshot and answering questions about it belongs to neither: that is
// libs/x/registrysyncer, shared with chainlink core so both build against the same code while core
// migrates off its own copy.
package registry

import (
	commonregistry "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone/registry"

	"github.com/smartcontractkit/capabilities/libs/x/registrysyncer"
)

// The vocabulary a Reader fills in, aliased so this package reads as though it owned it. These are
// the contract's own types - a Reader deals in raw config bytes - and are what a Snapshot is made
// of.
type (
	DonID                   = commonregistry.DonID
	DON                     = commonregistry.DON
	CapabilityConfiguration = commonregistry.CapabilityConfiguration
	NodeInfo                = commonregistry.NodeInfo
	Capability              = commonregistry.Capability
	Snapshot                = commonregistry.Snapshot
	Reader                  = commonregistry.Reader
)

// LocalRegistry is one snapshot, and the lookups over it.
type LocalRegistry = registrysyncer.LocalRegistry
