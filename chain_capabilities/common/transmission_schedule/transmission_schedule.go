package transmissionschedule

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/smartcontractkit/libocr/permutation"
	p2ptypes "github.com/smartcontractkit/libocr/ragep2p/types"
	"golang.org/x/crypto/sha3"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// TransmissionScheduler handles capability-layer transmission scheduling by waiting for the appropriate delay
// based on queue position. It does NOT check transmission state - that's handled by WriteReport.
type TransmissionScheduler struct {
	myPeerID   p2ptypes.PeerID
	donMembers []p2ptypes.PeerID // Immutable copy - safe for concurrent reads
	DeltaStage time.Duration
	F          uint8 // Fault tolerance - maximum number of faulty nodes
	lggr       logger.Logger
}

const DefaultDeltaStage = 15 * time.Second

func NewTransmissionScheduler(
	myPeerID p2ptypes.PeerID,
	donMembers []p2ptypes.PeerID,
	deltaStage time.Duration,
	F uint8,
	lggr logger.Logger,
) TransmissionScheduler {
	if deltaStage <= 0 {
		lggr.Debugf("deltaStage is set to a zero/negative value %v using default value of %v.", deltaStage, DefaultDeltaStage)
	}
	return TransmissionScheduler{
		myPeerID:   myPeerID,
		donMembers: slices.Clone(donMembers),
		DeltaStage: deltaStage,
		F:          F,
		lggr:       lggr,
	}
}

// GetQueuePosition returns this node's position (0 to N-1) in the transmission queue.
// Returns -1 if node is not in DON.
func (ts *TransmissionScheduler) GetQueuePosition(transmissionID string) int {
	sorted := slices.Clone(ts.donMembers)
	slices.SortFunc(sorted, func(a, b p2ptypes.PeerID) int {
		return bytes.Compare(a[:], b[:])
	})

	key := transmissionScheduleSeed(transmissionID)
	permuted := permutation.Permutation(len(sorted), key)

	for i, peerID := range sorted {
		if peerID == ts.myPeerID {
			return permuted[i]
		}
	}
	return -1
}

// permutedOrder returns DON members in queue order (position 0 first) for the given transmissionID.
func (ts *TransmissionScheduler) GetPermutedOrder(transmissionID string) []p2ptypes.PeerID {
	sorted := slices.Clone(ts.donMembers)
	slices.SortFunc(sorted, func(a, b p2ptypes.PeerID) int {
		return bytes.Compare(a[:], b[:])
	})

	key := transmissionScheduleSeed(transmissionID)
	permuted := permutation.Permutation(len(sorted), key)

	ordered := make([]p2ptypes.PeerID, len(sorted))
	for i, pos := range permuted {
		ordered[pos] = sorted[i]
	}
	return ordered
}

func transmissionScheduleSeed(transmissionID string) [16]byte {
	hash := sha3.New256()
	hash.Write([]byte(transmissionID))
	var key [16]byte
	copy(key[:], hash.Sum(nil))
	return key
}

func InitMyDON(ctx context.Context, registry core.CapabilitiesRegistry, capabilityID string, lggr logger.Logger, isLocal bool) (capabilities.DON, error) {
	if isLocal {
		return capabilities.DON{}, nil
	}
	if registry == nil {
		return capabilities.DON{}, fmt.Errorf("capabilities registry is nil")
	}
	localNode, err := registry.LocalNode(ctx)
	if err != nil {
		lggr.Errorw("failed to get local node", "error", err)
		return capabilities.DON{}, fmt.Errorf("failed to receiver local node: %w", err)
	}

	var dons []capabilities.DON

	donsWithNodes, err := registry.DONsForCapability(ctx, capabilityID)
	if err != nil {
		lggr.Errorw("failed getting DONs for capability", "capabilityID", capabilityID, "error", err)
		return capabilities.DON{}, fmt.Errorf("failed getting dons for capability: %w", err)
	}

	for _, d := range donsWithNodes {
		for _, n := range d.Nodes {
			if n.PeerID.String() == localNode.PeerID.String() {
				dons = append(dons, d.DON)
			}
		}
	}

	if len(dons) == 0 {
		lggr.Errorw("no DON found for local peer", "peerID", localNode.PeerID.String(), "capabilityID", capabilityID)
		return capabilities.DON{}, errors.New("failed to find don for my peer ID: " + localNode.PeerID.String())
	}

	if len(dons) > 1 {
		for _, d := range dons {
			lggr.Errorf("received more than one don for capability id: %s don id: %d don name: %s", capabilityID, d.ID, d.Name)
		}
	}

	return dons[0], nil
}

func InitialiseTransmissionScheduler(
	ctx context.Context,
	capRegistry core.CapabilitiesRegistry,
	deltaStage time.Duration,
	lggr logger.Logger,
	don *capabilities.DON,
	isLocal bool,
) (TransmissionScheduler, error) {
	if isLocal {
		return TransmissionScheduler{}, nil
	}
	localNode, err := capRegistry.LocalNode(ctx)
	if err != nil {
		lggr.Errorw("failed to get local node for transmission scheduler", "error", err)
		return TransmissionScheduler{}, fmt.Errorf("failed to get local node: %w", err)
	}

	if don == nil {
		lggr.Errorw("DON is nil when initialising transmission scheduler")
		return TransmissionScheduler{}, errors.New("capabilityInfo DON is nil")
	}

	if len(don.Members) == 0 {
		lggr.Errorw("DON has no members when initialising transmission scheduler")
		return TransmissionScheduler{}, errors.New("capabilityInfo DON is empty")
	}

	var donPeerIDs []p2ptypes.PeerID
	myPeerID := localNode.PeerID
	donPeerIDs = append(donPeerIDs, don.Members...)

	if myPeerID == nil {
		lggr.Errorw("local node peer ID is nil")
		return TransmissionScheduler{}, fmt.Errorf("local node peer ID is nil")
	}
	if len(donPeerIDs) == 0 {
		lggr.Errorw("DON members list is empty")
		return TransmissionScheduler{}, fmt.Errorf("DON members list is empty")
	}

	found := slices.Contains(donPeerIDs, *myPeerID)
	if !found {
		lggr.Errorw("local peer not in DON members", "myPeerID", myPeerID.String(), "donMembers", len(donPeerIDs))
		return TransmissionScheduler{}, fmt.Errorf("local peer ID %s not found in DON members", myPeerID.String())
	}

	lggr.Debugw("Transmission scheduler initialized",
		"deltaStage", deltaStage,
		"donSize", len(donPeerIDs),
		"F", don.F,
		"myPeerID", myPeerID.String(),
	)

	return NewTransmissionScheduler(
		*myPeerID,
		donPeerIDs,
		deltaStage,
		don.F,
		lggr,
	), nil
}
