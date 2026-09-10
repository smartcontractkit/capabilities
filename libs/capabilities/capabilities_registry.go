package capabilities

import (
	"context"
	"errors"

	"github.com/smartcontractkit/libocr/ragep2p/types"
)

//nolint:revive // Exported API: stuttering name kept for compatibility with consumers of the published module
type CapabilitiesRegistry interface {
	CapabilitiesRegistryBase
	CapabilitiesRegistryMetadata
}

//nolint:revive // Exported API: stuttering name kept for compatibility with consumers of the published module
type CapabilitiesRegistryMetadata interface {
	LocalNode(ctx context.Context) (Node, error)
	NodeByPeerID(ctx context.Context, peerID types.PeerID) (Node, error)
	ConfigForCapability(ctx context.Context, capabilityID string, donID uint32) (CapabilityConfiguration, error)
	DONsForCapability(ctx context.Context, capabilityID string) ([]DONWithNodes, error)
	// DONByID resolves a DON by its registry ID. Unlike DONsForCapability, this
	// resolves any DON known to the registry (including caller/workflow DONs that
	// do not host a given capability), which is required to authoritatively read
	// a caller DON's Families (e.g. zone membership) from its WorkflowDonID.
	DONByID(ctx context.Context, donID uint32) (DON, error)
}

//nolint:revive // Exported API: stuttering name kept for compatibility with consumers of the published module
type CapabilitiesRegistryBase interface {
	GetTrigger(ctx context.Context, ID string) (TriggerCapability, error)
	Get(ctx context.Context, ID string) (BaseCapability, error)
	GetExecutable(ctx context.Context, ID string) (ExecutableCapability, error)
	List(ctx context.Context) ([]BaseCapability, error)
	Add(ctx context.Context, c BaseCapability) error
	Remove(ctx context.Context, ID string) error
}

var _ CapabilitiesRegistry = UnimplementedCapabilitiesRegistry{}
var _ CapabilitiesRegistryBase = UnimplementedCapabilitiesRegistryBase{}

type UnimplementedCapabilitiesRegistry struct {
	UnimplementedCapabilitiesRegistryMetadata
	UnimplementedCapabilitiesRegistryBase
}

type UnimplementedCapabilitiesRegistryMetadata struct{}

func (UnimplementedCapabilitiesRegistryMetadata) LocalNode(ctx context.Context) (Node, error) {
	return Node{}, errors.New("LocalNode not implemented")
}

func (UnimplementedCapabilitiesRegistryMetadata) NodeByPeerID(ctx context.Context, peerID types.PeerID) (Node, error) {
	return Node{}, errors.New("NodeByPeerID not implemented")
}

func (UnimplementedCapabilitiesRegistryMetadata) ConfigForCapability(ctx context.Context, capabilityID string, donID uint32) (CapabilityConfiguration, error) {
	return CapabilityConfiguration{}, errors.New("ConfigForCapability not implemented")
}

func (UnimplementedCapabilitiesRegistryMetadata) DONsForCapability(ctx context.Context, capabilityID string) ([]DONWithNodes, error) {
	return nil, errors.New("DONsForCapability not implemented")
}

func (UnimplementedCapabilitiesRegistryMetadata) DONByID(ctx context.Context, donID uint32) (DON, error) {
	return DON{}, errors.New("DONByID not implemented")
}

type UnimplementedCapabilitiesRegistryBase struct {
}

func (UnimplementedCapabilitiesRegistryBase) GetTrigger(ctx context.Context, ID string) (TriggerCapability, error) {
	return nil, errors.New("GetTrigger not implemented")
}

func (UnimplementedCapabilitiesRegistryBase) Get(ctx context.Context, ID string) (BaseCapability, error) {
	return nil, errors.New("Get not implemented")
}

func (UnimplementedCapabilitiesRegistryBase) GetExecutable(ctx context.Context, ID string) (ExecutableCapability, error) {
	return nil, errors.New("GetExecutable not implemented")
}

func (UnimplementedCapabilitiesRegistryBase) List(ctx context.Context) ([]BaseCapability, error) {
	return nil, errors.New("List not implemented")
}

func (UnimplementedCapabilitiesRegistryBase) Add(ctx context.Context, c BaseCapability) error {
	return errors.New("Add not implemented")
}

func (UnimplementedCapabilitiesRegistryBase) Remove(ctx context.Context, ID string) error {
	return errors.New("Remove not implemented")
}
