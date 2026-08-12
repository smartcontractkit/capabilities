package registry

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

// DefaultSyncInterval matches chainlink's registrysyncer tick, so a node fronted
// by a standalone process sees registry changes on the same cadence it did before.
const DefaultSyncInterval = 12 * time.Second

// Syncer keeps a current snapshot of the registry by asking a Reader for one on a timer, and
// publishes it to everything that resolves capabilities or their DONs.
//
// Readers of the snapshot never block on a sync and never see a half-updated view: each sync
// replaces the snapshot pointer wholesale, and a lookup takes whichever snapshot was current when
// it asked.
type Syncer struct {
	services.StateMachine

	lggr     logger.Logger
	reader   Reader
	interval time.Duration
	// getPeerID answers "which node am I", which a snapshot needs to resolve LocalNode and which
	// only this process knows.
	getPeerID func() (ragetypes.PeerID, error)

	// current holds the most recent successful snapshot, or nil before the first successful sync.
	current atomic.Pointer[LocalRegistry]

	stopCh services.StopChan
	done   chan struct{}
}

// NewSyncer returns a Syncer that keeps up with reader. getPeerID identifies this process to the
// snapshots it publishes. A non-positive interval means DefaultSyncInterval.
func NewSyncer(lggr logger.Logger, reader Reader, getPeerID func() (ragetypes.PeerID, error), interval time.Duration) (*Syncer, error) {
	if reader == nil {
		return nil, errors.New("a registry reader is required")
	}
	if getPeerID == nil {
		return nil, errors.New("a peer ID is required to resolve this node in the registry")
	}
	if interval <= 0 {
		interval = DefaultSyncInterval
	}
	return &Syncer{
		lggr:      logger.Named(lggr, "RegistrySyncer"),
		reader:    reader,
		getPeerID: getPeerID,
		interval:  interval,
		stopCh:    make(services.StopChan),
		done:      make(chan struct{}),
	}, nil
}

func (s *Syncer) Name() string { return s.lggr.Name() }

func (s *Syncer) Start(context.Context) error {
	return s.StartOnce("RegistrySyncer", func() error {
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
		err = errors.New("no successful registry sync yet")
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

	// Sync once up front: a ticker first fires at T+interval, and every reader of this process
	// blocks until the first snapshot exists.
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

// Sync performs one read and, on success, publishes a new snapshot. A failed sync leaves the
// previous snapshot in place: a registry that cannot be reached right now is better served stale
// than not at all, and HealthReport is what says so.
func (s *Syncer) Sync(ctx context.Context) error {
	snap, err := s.reader.Read(ctx)
	if err != nil {
		return err
	}
	lr := NewLocalRegistry(s.lggr, s.getPeerID, snap.DONs, snap.Nodes, snap.Capabilities)
	s.current.Store(lr)
	s.lggr.Debugw("registry synced",
		"capabilities", len(lr.IDsToCapabilities),
		"dons", len(lr.IDsToDONs),
		"nodes", len(lr.IDsToNodes))
	return nil
}
