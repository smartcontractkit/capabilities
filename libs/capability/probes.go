package capability

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

const runName = "Run"

var (
	errStarting     = errors.New("starting")
	errShuttingDown = errors.New("shutting down")
)

// probes answers the health endpoints by asking the run's services on every request, so an answer
// is never stale.
type probes struct {
	mu        sync.RWMutex
	reporters []services.HealthReporter

	started  atomic.Bool
	draining atomic.Bool
}

func (p *probes) add(rs ...services.HealthReporter) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reporters = append(p.reporters, rs...)
}

func (p *probes) snapshot() []services.HealthReporter {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return slices.Clone(p.reporters)
}

func (p *probes) live() (bool, map[string]error) {
	return true, nil // being able to answer at all means the process is up
}

func (p *probes) startup() (bool, map[string]error) {
	if !p.started.Load() {
		return false, map[string]error{runName: errStarting}
	}
	return true, nil
}

func (p *probes) ready() (bool, map[string]error) {
	if p.draining.Load() {
		return false, map[string]error{runName: errShuttingDown}
	}
	if ok, errs := p.startup(); !ok {
		return ok, errs
	}

	ok := true
	errs := map[string]error{}
	for _, r := range p.snapshot() {
		err := r.Ready()
		errs[r.Name()] = err
		if err != nil {
			ok = false
		}
	}
	return ok, errs
}

func (p *probes) healthy() (bool, map[string]error) {
	// Services still starting are not reported on yet, so a clear report would mean nothing.
	if ok, errs := p.startup(); !ok {
		return ok, errs
	}

	errs := map[string]error{}
	for _, r := range p.snapshot() {
		services.CopyHealth(errs, r.HealthReport())
	}

	for _, err := range errs {
		if err != nil {
			return false, errs
		}
	}
	return true, errs
}
