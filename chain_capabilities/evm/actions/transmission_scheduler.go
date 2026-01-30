package actions

import (
	"bytes"
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
	deltaStage time.Duration
	F          uint8 // Fault tolerance - maximum number of faulty nodes
	lggr       logger.Logger
}

func NewTransmissionScheduler(
	myPeerID p2ptypes.PeerID,
	donMembers []p2ptypes.PeerID,
	deltaStage time.Duration,
	F uint8,
	lggr logger.Logger,
) TransmissionScheduler {
	return TransmissionScheduler{
		myPeerID:   myPeerID,
		donMembers: slices.Clone(donMembers),
		deltaStage: deltaStage,
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

// transmissionScheduleSeed generates a deterministic 16-byte key from transmissionID
func transmissionScheduleSeed(transmissionID string) [16]byte {
	hash := sha3.NewLegacyKeccak256()
	hash.Write([]byte(transmissionID))
	var key [16]byte
	copy(key[:], hash.Sum(nil))
	return key
}
