package testutils

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/libocr/ragep2p/types"
)

var _ core.CapabilitiesRegistry = (*capabilitiesRegistry)(nil)

type capabilitiesRegistry struct {
	core.UnimplementedCapabilitiesRegistry
	mu           sync.RWMutex
	capabilities map[string]interface{}
	t            *testing.T
}

func NewCapabilitiesRegistry(t *testing.T) *capabilitiesRegistry {
	return &capabilitiesRegistry{
		capabilities: make(map[string]interface{}),
		t:            t,
	}
}

func (r *capabilitiesRegistry) LocalNode(ctx context.Context) (capabilities.Node, error) {
	// Implement the logic for LocalNode
	return capabilities.Node{}, nil
}

func (r *capabilitiesRegistry) ConfigForCapability(ctx context.Context, name string, version uint32) (capabilities.CapabilityConfiguration, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	capability, exists := r.capabilities[name]
	if !exists {
		return capabilities.CapabilityConfiguration{}, fmt.Errorf("capability %s not found", name)
	}
	return capability.(capabilities.CapabilityConfiguration), nil
}

func (r *capabilitiesRegistry) Get(ctx context.Context, ID string) (capabilities.BaseCapability, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	capability, exists := r.capabilities[ID]
	if !exists {
		return nil, fmt.Errorf("capability %s not found", ID)
	}
	return capability.(capabilities.BaseCapability), nil
}

func (r *capabilitiesRegistry) GetExecutable(ctx context.Context, ID string) (capabilities.ExecutableCapability, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	capability, exists := r.capabilities[ID]
	if !exists {
		return nil, fmt.Errorf("trigger capability %s not found", ID)
	}
	return capability.(capabilities.ExecutableCapability), nil
}

func (r *capabilitiesRegistry) GetTrigger(ctx context.Context, ID string) (capabilities.TriggerCapability, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	capability, exists := r.capabilities[ID]
	if !exists {
		return nil, fmt.Errorf("trigger capability %s not found", ID)
	}
	return capability.(capabilities.TriggerCapability), nil
}

func (r *capabilitiesRegistry) GetAction(ctx context.Context, ID string) (capabilities.ExecutableCapability, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	capability, exists := r.capabilities[ID]
	if !exists {
		return nil, fmt.Errorf("action capability %s not found", ID)
	}
	return capability.(capabilities.ExecutableCapability), nil
}

func (r *capabilitiesRegistry) GetConsensus(ctx context.Context, ID string) (capabilities.ExecutableCapability, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	capability, exists := r.capabilities[ID]
	if !exists {
		return nil, fmt.Errorf("consensus capability %s not found", ID)
	}
	return capability.(capabilities.ExecutableCapability), nil
}

func (r *capabilitiesRegistry) GetTarget(ctx context.Context, ID string) (capabilities.ExecutableCapability, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	capability, exists := r.capabilities[ID]
	if !exists {
		return nil, fmt.Errorf("target capability %s not found", ID)
	}
	return capability.(capabilities.ExecutableCapability), nil
}

func (r *capabilitiesRegistry) List(ctx context.Context) ([]capabilities.BaseCapability, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var list []capabilities.BaseCapability
	for _, capability := range r.capabilities {
		list = append(list, capability.(capabilities.BaseCapability))
	}
	return list, nil
}

func (r *capabilitiesRegistry) Add(ctx context.Context, c capabilities.BaseCapability) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	info, err := c.Info(ctx)
	if err != nil {
		return err
	}

	r.capabilities[info.ID] = c
	return nil
}

func (r *capabilitiesRegistry) Remove(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.capabilities, id)
	return nil
}

// Test helpers
func (r *capabilitiesRegistry) Contains(capabilityIDs []string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, id := range capabilityIDs {
		if _, exists := r.capabilities[id]; !exists {
			return fmt.Errorf("capability %s was not added to the capabilities registry", id)
		}
	}

	return nil
}

func (r *capabilitiesRegistry) NodeByPeerID(ctx context.Context, peerID types.PeerID) (capabilities.Node, error) {
	return capabilities.Node{}, errors.New("unimplemented")
}
