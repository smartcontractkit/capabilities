package capability

import (
	"context"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

// rootService is everything one run supervises, aggregated into a single service - the whole of it,
// including the observability services and the HTTP server that reports on them.
//
// Aggregating is what lets the run report its health as one thing: the health checker registers the
// root, and the root's report is its own plus every sub-service's, so a capability that goes
// unhealthy shows up on /healthz without anything having to know it exists.
//
// It is built up rather than constructed in one go because what belongs to it is discovered in
// order - telemetry before the capability, since the capability is built out of what telemetry
// installed. services.Config takes its sub-services when the engine is made, so the engine is not
// made until start.
type rootService struct {
	lggr logger.Logger
	// name is the binary's, which is what the health metrics are labelled with. An embed run will
	// need an index on the end of it: the labels would otherwise collide between instances.
	name string
	subs []services.Service
}

// add gives the root another service to start and close.
//
// Order matters, and is the order added: sub-services start in it. Closing is not ordered -
// services.MultiCloser closes them concurrently, here as everywhere else in the stack - so this
// says nothing about the order they go down in.
func (r *rootService) add(svc services.Service) { r.subs = append(r.subs, svc) }

// reporters is what has been added so far, as things a health checker can report on.
//
// It is a snapshot rather than a live view, and that is the point: the health checker is given the
// services that were added before it, which are the ones that will already be running when it
// starts. Sub-services start in order, so anything added afterwards would not be.
func (r *rootService) reporters() []services.HealthReporter {
	out := make([]services.HealthReporter, 0, len(r.subs))
	for _, sub := range r.subs {
		out = append(out, sub)
	}
	return out
}

// start builds the engine over everything added and starts it, returning the aggregate.
//
// A failure part way is the engine's to unwind: services.MultiStart closes the sub-services that
// did start before reporting, so a root that failed to start is one that is not running rather than
// one that is half running. That is also why nothing is handed back to close in that case.
func (r *rootService) start(ctx context.Context) (services.Service, error) {
	svc, _ := services.Config{
		Name:           r.name,
		NewSubServices: func(logger.Logger) []services.Service { return r.subs },
	}.NewServiceEngine(r.lggr)

	if err := svc.Start(ctx); err != nil {
		return nil, err
	}
	return svc, nil
}
