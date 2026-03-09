package actions

import (
	"bytes"
	"fmt"
	"slices"
	"time"

	"github.com/smartcontractkit/libocr/permutation"
	p2ptypes "github.com/smartcontractkit/libocr/ragep2p/types"
	"golang.org/x/crypto/sha3"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// TransmissionScheduler handles capability-layer transmission scheduling by waiting for the appropriate delay
// based on queue position. It does NOT check transmission state - that's handled by WriteReport.
type TransmissionScheduler struct {
	myPeerID   p2ptypes.PeerID
	donMembers []p2ptypes.PeerID // Immutable copy - safe for concurrent reads
	p2pConfig  map[string]string // peerID hex -> transmitter address
	deltaStage time.Duration
	F          uint8 // Fault tolerance - maximum number of faulty nodes
	lggr       logger.Logger
}

const defaultDeltaStage = 15 * time.Second

func NewTransmissionScheduler(
	myPeerID p2ptypes.PeerID,
	donMembers []p2ptypes.PeerID,
	p2pConfig map[string]string,
	deltaStage time.Duration,
	F uint8,
	lggr logger.Logger,
) TransmissionScheduler {
	if deltaStage <= 0 {
		lggr.Debugf("deltaStage is set to a zero/negative value %v using default value of %v.", deltaStage, defaultDeltaStage)
	}
	return TransmissionScheduler{
		myPeerID:   myPeerID,
		donMembers: slices.Clone(donMembers),
		p2pConfig:  p2pConfig,
		deltaStage: deltaStage,
		F:          F,
		lggr:       lggr,
	}
}

// permutedOrder returns DON members in queue order (position 0 first) for the given transmissionID.
func (ts *TransmissionScheduler) permutedOrder(transmissionID string) []p2ptypes.PeerID {
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

// GetQueuePosition returns this node's position (0 to N-1) in the transmission queue.
// Returns -1 if node is not in DON.
func (ts *TransmissionScheduler) GetQueuePosition(transmissionID string) int {
	for pos, peerID := range ts.permutedOrder(transmissionID) {
		if peerID == ts.myPeerID {
			return pos
		}
	}
	return -1
}

// GetOrderedTransmitters returns transmitter addresses in queue order (position 0 first)
// for the given transmissionID. PeerIDs are resolved to transmitter addresses via p2pConfig.
// Peers not found in p2pConfig are skipped.
func (ts *TransmissionScheduler) GetOrderedTransmitters(transmissionID string) []string {
	permuted := ts.permutedOrder(transmissionID)
	ts.lggr.Debugf("TestingAptosWriteCap: GetOrderedTransmitters donMembersCount=%d permutedCount=%d p2pConfigKeys=%d transmissionID=%s",
		len(ts.donMembers), len(permuted), len(ts.p2pConfig), transmissionID)
	var transmitters []string
	for i, peerID := range permuted {
		peerHex := fmt.Sprintf("%x", peerID[:])
		if addr, ok := ts.p2pConfig[peerHex]; ok {
			transmitters = append(transmitters, addr)
		} else {
			ts.lggr.Debugf("TestingAptosWriteCap: GetOrderedTransmitters peerID[%d]=%s not found in p2pConfig", i, peerHex)
		}
	}
	return transmitters
}

// transmissionScheduleSeed generates a deterministic 16-byte key from transmissionID
func transmissionScheduleSeed(transmissionID string) [16]byte {
	hash := sha3.New256()
	hash.Write([]byte(transmissionID))
	var key [16]byte
	copy(key[:], hash.Sum(nil))
	return key
}
