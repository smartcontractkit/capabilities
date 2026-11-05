// Package migration serves a capability built for capability.Run as a StandardCapabilities LOOP, so nodes that
// still run capabilities that way can run it while it moves to capability.Run.
package migration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/smartcontractkit/capabilities/libs/capability"
	"github.com/smartcontractkit/capabilities/libs/loopserver"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"
	capmon "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/monitoring"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// Serve serves the capability newCapability builds as a StandardCapabilities LOOP named name. newCapability is
// called when the node calls Initialise, with what the node gives.
func Serve(name string, newCapability func(*loop.Server, core.StandardCapabilitiesDependencies) (capability.Capability, error),
	opts ...loop.ServerOpt) {
	loopserver.ServeNew(name, func(s *loop.Server) *standardCapabilities {
		return newStandardCapabilities(name, func(deps core.StandardCapabilitiesDependencies) (capability.Capability, error) {
			return newCapability(s, deps)
		})
	}, opts...)
}

var _ loop.StandardCapabilities = (*standardCapabilities)(nil)

// standardCapabilities is a capability as a StandardCapabilities LOOP. Until Initialise, there is no capability,
// because what it is built from comes with Initialise.
type standardCapabilities struct {
	name          string
	newCapability func(core.StandardCapabilitiesDependencies) (capability.Capability, error)
	initMetrics   capmon.InitMetrics

	mu         sync.Mutex
	capability capability.Capability
	registry   registry.CapabilitiesRegistry
}

// errNotInitialised is what is answered for the capability before the node calls Initialise.
var errNotInitialised = errors.New("the capability is built when the node calls Initialise, which it has not")

func newStandardCapabilities(name string,
	newCapability func(core.StandardCapabilitiesDependencies) (capability.Capability, error)) *standardCapabilities {
	return &standardCapabilities{name: name, newCapability: newCapability, initMetrics: capmon.NewInitMetrics()}
}

// Initialise builds the capability, starts it and adds it to the node's registry, which is what the node runs it
// through.
func (s *standardCapabilities) Initialise(ctx context.Context, deps core.StandardCapabilitiesDependencies) (err error) {
	defer func() { s.initMetrics.RecordInit(ctx, err, attribute.String("capability", s.name)) }()

	c, err := s.newCapability(deps)
	if err != nil {
		return fmt.Errorf("failed to instantiate capability: %w", err)
	}

	if err = c.Start(ctx); err != nil {
		return fmt.Errorf("failed to start capability: %w", err)
	}

	if err = deps.CapabilityRegistry.Add(ctx, c); err != nil {
		return errors.Join(fmt.Errorf("failed to add the capability to the registry: %w", err), c.Close())
	}

	s.mu.Lock()
	s.capability = c
	s.registry = deps.CapabilityRegistry
	s.mu.Unlock()
	return nil
}

func (s *standardCapabilities) Infos(ctx context.Context) ([]capabilities.CapabilityInfo, error) {
	c, _ := s.current()
	if c == nil {
		return nil, errNotInitialised
	}

	info, err := c.Info(ctx)
	if err != nil {
		return nil, err
	}

	return []capabilities.CapabilityInfo{info}, nil
}

func (s *standardCapabilities) Name() string { return s.name }

// Start does nothing: the node starts the capability with Initialise.
func (s *standardCapabilities) Start(context.Context) error { return nil }

func (s *standardCapabilities) HealthReport() map[string]error {
	c, _ := s.current()
	if c == nil {
		return map[string]error{s.name: errNotInitialised}
	}

	return c.HealthReport()
}

func (s *standardCapabilities) Ready() error {
	c, _ := s.current()
	if c == nil {
		return errNotInitialised
	}

	return c.Ready()
}

func (s *standardCapabilities) current() (capability.Capability, registry.CapabilitiesRegistry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.capability, s.registry
}

// Close removes the capability from the node's registry, then closes it.
func (s *standardCapabilities) Close() error {
	c, reg := s.current()
	if c == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var errs []error
	info, err := c.Info(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to read the capability's info: %w", err))
	} else if err = reg.Remove(ctx, info.ID); err != nil {
		errs = append(errs, fmt.Errorf("failed to remove %s from the registry: %w", info.ID, err))
	}

	return errors.Join(append(errs, c.Close())...)
}
