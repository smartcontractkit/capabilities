// Package tinysim is a tiny in-process stand-in for an OCR3 DON, for testing a capability together with its consensus
// plugin without real nodes or networking.
//
// A DON is a group of N nodes that agree on a value in rounds. The logic of a round lives in a reporting plugin. Sim
// simulates this with a single plugin instance shared by all nodes. While Run is in progress, Sim runs a round every
// ocrRoundInterval:
//
//  1. call plugin.Observation once per node, in node order (0, 1, ..., numNodes-1),
//  2. call plugin.Outcome with all observations and the previous round's Outcome.
//
// Since all nodes share one plugin, "what node i observes" is whatever the plugin returns on its i-th Observation call
// in a round.
//
// The plugin is expected to deliver outcomes to the capability itself. Sim does not call Query, ValidateObservation,
// ObservationQuorum or Reports, and does not transmit anything.
package tinysim

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/smartcontractkit/libocr/commontypes"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

const (
	ocrRoundInterval  = time.Second
	capabilityTimeout = 30 * time.Second
	roundTimeout      = 10 * time.Second
)

// Sim is a simulated DON. Req and Resp are the capability's request and response types.
type Sim[Req, Resp any] struct {
	plugin     ocr3types.ReportingPlugin[[]byte]
	capability func(context.Context, Req) (Resp, error)
	numNodes   int

	mu              sync.Mutex
	activeTest      *testing.T // non-nil only while Run is in progress; rounds are skipped otherwise
	seqNr           uint64     // sequence number of the last successful round
	previousOutcome ocr3types.Outcome
}

// New returns a simulated DON of numNodes nodes running plugin, with capability as the API under test. Rounds run in
// the background until t ends.
func New[Req, Resp any](
	t *testing.T,
	capability func(context.Context, Req) (Resp, error),
	plugin ocr3types.ReportingPlugin[[]byte],
	numNodes int,
) *Sim[Req, Resp] {
	s := &Sim[Req, Resp]{plugin: plugin, capability: capability, numNodes: numNodes}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(ocrRoundInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.runRound()
			}
		}
	}()
	t.Cleanup(func() { close(stop); <-done })
	return s
}

// Run calls the capability with req, while running rounds, and asserts that it returns want.
func (s *Sim[Req, Resp]) Run(t *testing.T, req Req, want Resp) {
	t.Helper()
	s.setActiveTest(t)
	defer s.setActiveTest(nil)

	ctx, cancel := context.WithTimeout(context.Background(), capabilityTimeout)
	defer cancel()
	got, err := s.capability(ctx, req)
	require.NoError(t, err)
	if wantMsg, ok := any(want).(proto.Message); ok {
		gotMsg, _ := any(got).(proto.Message)
		assert.True(t, proto.Equal(wantMsg, gotMsg), "want %v, got %v", wantMsg, gotMsg)
		return
	}
	assert.Equal(t, want, got)
}

// setActiveTest waits for any in-progress round, so a round never reports to a test that has already finished.
func (s *Sim[Req, Resp]) setActiveTest(t *testing.T) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeTest = t
}

func (s *Sim[Req, Resp]) runRound() {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.activeTest
	if t == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), roundTimeout)
	defer cancel()
	roundCtx := ocr3types.OutcomeContext{SeqNr: s.seqNr + 1, PreviousOutcome: s.previousOutcome}

	observations := s.observeAllNodes(ctx, t, roundCtx)
	outcome, err := s.plugin.Outcome(ctx, roundCtx, nil, observations)
	if err != nil {
		t.Errorf("tinysim: round %d: Outcome: %v", roundCtx.SeqNr, err)
		return
	}
	s.seqNr = roundCtx.SeqNr
	s.previousOutcome = outcome
}

// observeAllNodes collects one observation per node. A node whose Observation fails contributes nothing, like a node
// that is down.
func (s *Sim[Req, Resp]) observeAllNodes(ctx context.Context, t *testing.T, roundCtx ocr3types.OutcomeContext) []types.AttributedObservation {
	var observations []types.AttributedObservation
	for node := range s.numNodes {
		obs, err := s.plugin.Observation(ctx, roundCtx, nil)
		if err != nil {
			t.Logf("tinysim: round %d: node %d: Observation: %v", roundCtx.SeqNr, node, err)
			continue
		}
		observations = append(observations, types.AttributedObservation{
			Observation: obs,
			Observer:    commontypes.OracleID(node), //nolint:gosec // numNodes is small
		})
	}
	return observations
}
