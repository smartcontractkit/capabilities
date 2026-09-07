package poller

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/list"

	"github.com/smartcontractkit/capabilities/libs/chainconsensus/metrics"
	"github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
)

const (
	// DefaultMaxQueuedRequests bounds how many requests a Poller tracks at once across its input and retry queues.
	DefaultMaxQueuedRequests = 1000
	// DefaultObservationTimeout bounds a single CaptureObservation attempt. A slow attempt is abandoned and retried
	// on the next poll instead of holding a worker indefinitely.
	DefaultObservationTimeout = 30 * time.Second
)

// ErrQueueFull is returned by Enqueue when the poller already tracks its maximum number of requests.
var ErrQueueFull = errors.New("poller queue is full")

type requestToPoll struct {
	types.ObservableRequest
	//nolint:containedctx // Justification: required to receive signal, that request no longer requires polling
	Ctx context.Context
}

type requestToRetry struct {
	requestToPoll
	LastAttemptAt time.Time
}

// Option configures a Poller.
type Option func(*Poller)

// WithMaxQueuedRequests sets how many requests the poller tracks at once. Enqueue returns ErrQueueFull beyond it.
func WithMaxQueuedRequests(n int) Option {
	return func(p *Poller) { p.maxQueuedRequests = n }
}

// WithObservationTimeout sets the deadline applied to each CaptureObservation attempt.
func WithObservationTimeout(d time.Duration) Option {
	return func(p *Poller) { p.observationTimeout = d }
}

// Poller - maintains queue of requestToPoll and periodically refreshes our observations by calling CaptureObservation.
// A request remains in the queue until its context is canceled to ensure that in case of reorg or errors we eventually capture
// valid observation. Example: CallContract should be polled until quorum of nodes has reached requested block.
// Request polls after initial, occur with a delay defined by pollPeriod.
//
// The number of tracked requests is bounded by maxQueuedRequests; canceled requests are dropped at the next hop and
// release their slot, so a caller that gives up frees capacity within one poll period.
type Poller struct {
	// service state management
	services.Service
	engine *services.Engine

	lggr               logger.SugaredLogger
	maxWorkers         uint
	pollPeriod         time.Duration
	maxQueuedRequests  int
	observationTimeout time.Duration
	metrics            metrics.ConsensusMetrics

	mutex       sync.Mutex
	inputNotify chan struct{}
	requests    *list.List[requestToPoll]
	requestsCh  chan requestToPoll
	retryQueue  *list.List[requestToRetry]
	// tracked counts every admitted request that has not yet been dropped, wherever it currently sits
	// (input queue, requestsCh, a worker, or the retry queue).
	tracked int
}

func NewPoller(lggr logger.Logger, metrics metrics.ConsensusMetrics, maxWorkers uint, pollPeriod time.Duration, opts ...Option) *Poller {
	p := &Poller{
		maxWorkers:         maxWorkers,
		pollPeriod:         pollPeriod,
		maxQueuedRequests:  DefaultMaxQueuedRequests,
		observationTimeout: DefaultObservationTimeout,
		metrics:            metrics,

		inputNotify: make(chan struct{}, 1),
		requests:    list.New[requestToPoll](),
		requestsCh:  make(chan requestToPoll, maxWorkers),
		retryQueue:  list.New[requestToRetry](),
	}
	for _, opt := range opts {
		opt(p)
	}

	p.Service, p.engine = services.Config{
		Name:  "Poller",
		Start: p.start,
		Close: p.close,
	}.NewServiceEngine(lggr)

	p.lggr = p.engine.SugaredLogger
	return p
}

// Enqueue admits request for polling until ctx is canceled. It returns ErrQueueFull when the poller already tracks
// maxQueuedRequests, so the caller can fail the request instead of queueing unbounded work.
func (p *Poller) Enqueue(ctx context.Context, request types.ObservableRequest) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.tracked >= p.maxQueuedRequests {
		p.metrics.IncQueueRejected(ctx)
		p.lggr.Warnw("rejecting request, poller queue is full", "requestID", request.ID(), "tracked", p.tracked, "maxQueuedRequests", p.maxQueuedRequests)
		return ErrQueueFull
	}
	p.tracked++
	p.enqueueUnsafe(ctx, requestToPoll{
		ObservableRequest: request,
		Ctx:               ctx,
	})
	return nil
}

// drop releases the slot held by a request whose context is done.
func (p *Poller) drop(request requestToPoll) {
	p.mutex.Lock()
	p.tracked--
	p.mutex.Unlock()
	p.lggr.Debugw("request was canceled - removing from queue", "requestID", request.ID())
}

func (p *Poller) popFirst(ctx context.Context) *requestToPoll {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.requests.Len() == 0 {
		return nil
	}

	result := p.requests.Remove(p.requests.Front())
	p.metrics.RecordQueueSize(ctx, p.requests.Len())
	return &result
}

func (p *Poller) enqueueUnsafe(ctx context.Context, rq requestToPoll) {
	p.requests.PushBack(rq)
	p.metrics.RecordQueueSize(ctx, p.requests.Len())
	select {
	case p.inputNotify <- struct{}{}:
	default:
	}
}

func (p *Poller) start(_ context.Context) error {
	p.engine.Go(p.scheduleProcessing)
	p.engine.Go(p.scheduleReprocessing)
	// spawn a goroutine per
	for range p.maxWorkers {
		p.engine.Go(p.processRequests)
	}
	return nil
}

func (p *Poller) close() error {
	return nil
}

// processRequest fetches observations and adds requestToPoll into retry queue if needed.
func (p *Poller) processRequest(request requestToPoll) {
	ctx, cancel := p.engine.Ctx(request.Ctx)
	defer cancel()
	if ctx.Err() != nil {
		p.drop(request)
		return
	}

	p.captureObservation(ctx, request)

	// The request may have completed while we were observing; do not hold a slot for it until the next tick.
	if request.Ctx.Err() != nil {
		p.drop(request)
		return
	}

	p.mutex.Lock()
	p.retryQueue.PushBack(requestToRetry{
		requestToPoll: request,
		LastAttemptAt: time.Now(),
	})
	p.metrics.RecordRetryQueueSize(ctx, p.retryQueue.Len())
	p.mutex.Unlock()
}

func (p *Poller) captureObservation(ctx context.Context, request requestToPoll) {
	ctx, cancel := context.WithTimeout(ctx, p.observationTimeout)
	defer cancel()

	err := request.CaptureObservation(ctx)
	if err != nil {
		p.lggr.Warnw("failed to capture observation", "err", err, "requestID", request.ID())
	} else {
		// TODO: some requests might need only one successful read (finalized data)
		p.lggr.Debugw("captured observation", "requestID", request.ID())
	}
}

func (p *Poller) processRequests(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case request, ok := <-p.requestsCh:
			if !ok {
				return
			}
			p.processRequest(request)
		}
	}
}

func (p *Poller) scheduleProcessing(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.inputNotify: // queue contains at least one element
		}

		// push whole queue to the channel for concurrent processing
		for {
			request := p.popFirst(ctx)
			if request == nil {
				break
			}

			select {
			case <-ctx.Done():
				return
			case p.requestsCh <- *request:
			}
		}
	}
}

func (p *Poller) scheduleReadyForReprocessing(ctx context.Context, now time.Time) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	for {
		if ctx.Err() != nil {
			return
		}
		request := p.retryQueue.Front()
		if request == nil {
			return
		}
		if request.Value.LastAttemptAt.Add(p.pollPeriod).After(now) {
			return
		}

		p.retryQueue.Remove(request)
		p.metrics.RecordRetryQueueSize(ctx, p.retryQueue.Len())

		// Canceled requests leave here instead of taking another trip through the workers.
		if request.Value.Ctx.Err() != nil {
			p.tracked--
			p.lggr.Debugw("request was canceled - removing from queue", "requestID", request.Value.ID())
			continue
		}

		p.enqueueUnsafe(ctx, request.Value.requestToPoll)
	}
}

func (p *Poller) scheduleReprocessing(ctx context.Context) {
	ticker := time.NewTicker(p.pollPeriod)
	defer ticker.Stop()
	for {
		var now time.Time
		select {
		case <-ctx.Done():
			return
		case now = <-ticker.C:
			p.scheduleReadyForReprocessing(ctx, now)
		}
	}
}
