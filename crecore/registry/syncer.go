package registry

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"

	"github.com/smartcontractkit/capabilities/libs/x/registrysyncer"
)

// DefaultSyncInterval matches chainlink's registrysyncer tick, so a node fronted
// by a standalone process sees registry changes on the same cadence it did before.
const DefaultSyncInterval = 12 * time.Second

// ORM persists snapshots so a restart has something to answer from before its first read lands.
type ORM = registrysyncer.ORM

// Syncer keeps a current snapshot of the registry by asking a Reader for one on a timer, and
// publishes it to everything that resolves capabilities or their DONs.
//
// Readers of the snapshot never block on a sync and never see a half-updated view: each sync
// replaces the snapshot pointer wholesale, and a lookup takes whichever snapshot was current when
// it asked.
//
// Every snapshot read is also stored, and the newest stored one is loaded at startup. The registry
// lives on a chain, and a chain is not always reachable when a process starts; without this, a
// restart would fail every registry lookup until its first read landed, which for a process core
// depends on is a restart that takes the node's capabilities down with it.
type Syncer struct {
	services.StateMachine

	lggr     logger.Logger
	reader   Reader
	orm      ORM
	interval time.Duration

	peerID ragetypes.PeerID

	// current holds the most recent snapshot, or nil before the first one lands.
	current atomic.Pointer[LocalRegistry]

	stopCh services.StopChan
	done   chan struct{}
}

// NewSyncer returns a Syncer that keeps up with reader, storing what it reads through orm.
// getPeerID identifies this process to the snapshots it publishes. A non-positive interval means
// DefaultSyncInterval.
//
// What it was given is checked when it starts rather than here, so that a Syncer always exists to
// be wired to the things that read from it: whether it can run is a startup question, and startup
// is where an answer of "no" has somewhere to go.
func NewSyncer(
	lggr logger.Logger,
	reader Reader,
	orm ORM,
	peerID ragetypes.PeerID,
	interval time.Duration,
) *Syncer {
	if interval <= 0 {
		interval = DefaultSyncInterval
	}
	return &Syncer{
		lggr:     logger.Named(lggr, "RegistrySyncer"),
		reader:   reader,
		orm:      orm,
		peerID:   peerID,
		interval: interval,
		stopCh:   make(services.StopChan),
		done:     make(chan struct{}),
	}
}

func (s *Syncer) Name() string { return s.lggr.Name() }

func (s *Syncer) Start(context.Context) error {
	return s.StartOnce("RegistrySyncer", func() error {
		switch {
		case s.reader == nil:
			return errors.New("a registry reader is required")
		case s.orm == nil:
			return errors.New("somewhere to store registry snapshots is required")
		case len(s.peerID) == 0:
			return errors.New("a peer ID is required to resolve this node in the registry")
		}
		go s.syncLoop()
		return nil
	})
}

func (s *Syncer) Close() error {
	return s.StopOnce("RegistrySyncer", func() error {
		close(s.stopCh)
		<-s.done
		return nil
	})
}

// HealthReport is unhealthy until the first snapshot lands: until then nothing this serves can be
// answered, and saying so is more useful than answering every lookup with the same error.
func (s *Syncer) HealthReport() map[string]error {
	err := s.Healthy()
	if s.current.Load() == nil {
		err = errors.New("no registry snapshot yet")
	}
	return map[string]error{s.Name(): err}
}

// Current returns the latest snapshot, or an error if none has landed yet.
func (s *Syncer) Current() (*LocalRegistry, error) {
	lr := s.current.Load()
	if lr == nil {
		return nil, errors.New("registry not synced yet")
	}
	return lr, nil
}

func (s *Syncer) syncLoop() {
	defer close(s.done)

	ctx, cancel := s.stopCh.NewCtx()
	defer cancel()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	// Publish the stored snapshot first so lookups are answerable while the first read is still in
	// flight, and still answerable if it fails outright.
	s.restore(ctx)

	// Sync once up front: a ticker first fires at T+interval, and until something is published every
	// reader of this process is blocked.
	if err := s.Sync(ctx); err != nil {
		s.lggr.Errorw("initial registry sync failed", "err", err)
	}

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			if err := s.Sync(ctx); err != nil {
				s.lggr.Errorw("registry sync failed", "err", err)
			}
		}
	}
}

// restore publishes the newest stored snapshot, if there is one.
//
// Having none is the ordinary state on a first run, so it is not an error here: the sync that
// follows is what this process actually relies on, and this only shortens the window before it
// lands.
func (s *Syncer) restore(ctx context.Context) {
	lr, err := s.orm.LatestLocalRegistry(ctx)
	if err != nil {
		s.lggr.Infow("no stored registry snapshot to start from; waiting for the first read", "err", err)
		return
	}

	// Neither of these survives being stored, so a restored snapshot cannot resolve anything until
	// they are put back.
	lr.Logger = s.lggr

	lr.GetPeerID = s.getPeerID

	s.current.Store(lr)
	s.lggr.Infow("serving a stored registry snapshot until the first read lands",
		"capabilities", len(lr.IDsToCapabilities), "dons", len(lr.IDsToDONs), "nodes", len(lr.IDsToNodes))
}

// Sync performs one read and, on success, publishes and stores a new snapshot. A failed sync leaves
// the previous snapshot in place: a registry that cannot be reached right now is better served
// stale than not at all, and HealthReport is what says so.
func (s *Syncer) Sync(ctx context.Context) error {
	snap, err := s.reader.Read(ctx)
	if err != nil {
		return err
	}
	lr := registrysyncer.FromSnapshot(s.lggr, s.getPeerID, snap)
	s.current.Store(lr)
	s.lggr.Debugw("registry synced",
		"capabilities", len(lr.IDsToCapabilities),
		"dons", len(lr.IDsToDONs),
		"nodes", len(lr.IDsToNodes))

	// Storing is what the next restart starts from, not what this process serves, so failing to
	// store costs a cold start rather than this sync.
	if err := s.orm.AddLocalRegistry(ctx, lr); err != nil {
		s.lggr.Errorw("failed to store the registry snapshot", "err", err)
	}
	return nil
}

func (s *Syncer) getPeerID() (ragetypes.PeerID, error) {
	return s.peerID, nil
}
