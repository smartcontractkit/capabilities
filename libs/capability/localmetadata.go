package capability

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"sync"

	"github.com/smartcontractkit/chainlink-common/keystore/corekeys"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	p2ptypes "github.com/smartcontractkit/libocr/ragep2p/types"

	keystoreocr "github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr"
)

// embeddedPeerSeedPrefix separates the peer keys derived here from any other keys derived from an index.
const embeddedPeerSeedPrefix = "cre/capability/embed/peer/"

// EmbeddedPeerID is the peer ID of instance of an embedded run, derived from its index so every instance knows every other's.
func EmbeddedPeerID(instance int) (p2ptypes.PeerID, error) {
	seed := sha256.Sum256([]byte(embeddedPeerSeedPrefix + strconv.Itoa(instance)))
	peerID, err := p2ptypes.PeerIDFromPublicKey(ed25519.NewKeyFromSeed(seed[:]).Public().(ed25519.PublicKey))
	if err != nil {
		return p2ptypes.PeerID{}, fmt.Errorf("failed to derive the peer ID of instance %d: %w", instance, err)
	}

	return peerID, nil
}

// localMetadata is the metadata of a registry with no node to sync it from: the embedded run's instances are the
// only DON there is, which both runs workflows and hosts every capability, as the run has no other.
type localMetadata struct {
	don   capabilities.DON
	nodes []capabilities.Node
	local int

	mu sync.Mutex
	// ocr3Configs are the OCR3 configs added to every capability on the DON.
	ocr3Configs map[string]ocrtypes.ContractConfig
}

var _ registry.CapabilitiesRegistryMetadata = (*localMetadata)(nil)

// newLocalMetadata is the metadata of instance info of an embedded run, whose DON has ID donID.
func newLocalMetadata(info InstanceInfo, donID uint32) (*localMetadata, error) {
	m := &localMetadata{local: info.ID}
	m.don = capabilities.DON{
		Name:             "embedded",
		ID:               donID,
		F:                uint8((info.Total - 1) / 3), //nolint:gosec // an embedded run has few instances
		IsPublic:         true,
		AcceptsWorkflows: true,
	}

	for i := range info.Total {
		peerID, err := EmbeddedPeerID(i)
		if err != nil {
			return nil, err
		}

		signer, err := embeddedSigner(i)
		if err != nil {
			return nil, err
		}

		m.don.Members = append(m.don.Members, peerID)
		m.nodes = append(m.nodes, capabilities.Node{PeerID: &peerID, Signer: signer})
	}

	for i := range m.nodes {
		m.nodes[i].WorkflowDON = m.don
		m.nodes[i].CapabilityDONs = []capabilities.DON{m.don}
	}

	return m, nil
}

// embeddedSigner is the onchain public key of instance's evm key bundle, as the registry lists a node's signer.
func embeddedSigner(instance int) ([32]byte, error) {
	bundles, err := keystoreocr.EmbeddedKeyBundles(instance)
	if err != nil {
		return [32]byte{}, err
	}

	j := slices.IndexFunc(bundles, func(b keystoreocr.KeyBundle) bool { return b.ChainType() == string(corekeys.EVM) })
	if j < 0 {
		return [32]byte{}, fmt.Errorf("instance %d has no evm key bundle", instance)
	}

	var signer [32]byte
	copy(signer[:], bundles[j].PublicKey())
	return signer, nil
}

func (m *localMetadata) LocalNode(context.Context) (capabilities.Node, error) {
	return m.nodes[m.local], nil
}

func (m *localMetadata) NodeByPeerID(_ context.Context, peerID p2ptypes.PeerID) (capabilities.Node, error) {
	j := slices.IndexFunc(m.nodes, func(n capabilities.Node) bool { return *n.PeerID == peerID })
	if j < 0 {
		return capabilities.Node{}, fmt.Errorf("peer %s is not an instance of the embedded run", peerID)
	}

	return m.nodes[j], nil
}

func (m *localMetadata) addOCR3Config(key string, config ocrtypes.ContractConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ocr3Configs == nil {
		m.ocr3Configs = map[string]ocrtypes.ContractConfig{}
	}

	m.ocr3Configs[key] = config
}

// ConfigForCapability is the config of a capability on the DON: only the OCR3 configs added to the run, as an
// embedded run configures its capabilities itself.
func (m *localMetadata) ConfigForCapability(ctx context.Context, _ string, donID uint32) (capabilities.CapabilityConfiguration, error) {
	if _, err := m.DONByID(ctx, donID); err != nil {
		return capabilities.CapabilityConfiguration{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	return capabilities.CapabilityConfiguration{Ocr3Configs: maps.Clone(m.ocr3Configs)}, nil
}

func (m *localMetadata) DONsForCapability(context.Context, string) ([]capabilities.DONWithNodes, error) {
	return []capabilities.DONWithNodes{{DON: m.don, Nodes: slices.Clone(m.nodes)}}, nil
}

func (m *localMetadata) DONByID(_ context.Context, donID uint32) (capabilities.DON, error) {
	if donID != m.don.ID {
		return capabilities.DON{}, fmt.Errorf("DON %d is not the embedded run's DON %d", donID, m.don.ID)
	}

	return m.don, nil
}
