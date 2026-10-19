package capability

import (
	"github.com/smartcontractkit/capabilities/libs/capability/di"
)

// RunInjector is what the run command builds a capability from. Register what a capability needs on it
// before calling Run; the run adds its own dependencies to it.
var RunInjector = di.NewInjector()

// EmbedInjector is what the embed command builds each of its instances from. Every instance gets a clone
// of it, so each resolves its providers itself and can override them. A clone is not a deep copy: a value
// or config provided here is the same object in every instance, so provide a constructor for anything an
// instance must not share.
var EmbedInjector = di.NewInjector()

// Injectors provides to both RunInjector and EmbedInjector, for what a capability needs however it is run.
var Injectors = di.MultiInjector{RunInjector, EmbedInjector}

// EmbeddedInfo is which of an embed run's instances a capability is. Only embedded instances are
// provided one.
type EmbeddedInfo struct {
	// InstanceID is this instance's index, from 0 to TotalInstances-1.
	InstanceID int
	// TotalInstances is how many instances the embed run has.
	TotalInstances int
}
