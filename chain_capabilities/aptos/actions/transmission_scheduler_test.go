package actions

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	p2ptypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

func TestNewTransmissionScheduler_DefaultDeltaStageFallback(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	myPeerID := p2ptypes.PeerID{0x01}

	t.Run("zero deltaStage uses default", func(t *testing.T) {
		s := NewTransmissionScheduler(myPeerID, []p2ptypes.PeerID{myPeerID}, nil, 0, 1, lggr)
		require.Equal(t, defaultDeltaStage, s.deltaStage)
	})

	t.Run("negative deltaStage uses default", func(t *testing.T) {
		s := NewTransmissionScheduler(myPeerID, []p2ptypes.PeerID{myPeerID}, nil, -3*time.Second, 1, lggr)
		require.Equal(t, defaultDeltaStage, s.deltaStage)
	})

	t.Run("positive deltaStage preserved", func(t *testing.T) {
		want := 4 * time.Second
		s := NewTransmissionScheduler(myPeerID, []p2ptypes.PeerID{myPeerID}, nil, want, 1, lggr)
		require.Equal(t, want, s.deltaStage)
	})
}
