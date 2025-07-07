package poller

import (
	"context"
	"sync"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/list"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/types"
)

type requestToPoll struct {
	types.ObservableRequest
	//nolint:containedctx // Justification: required to receive signal, that request no longer requires polling
	Ctx context.Context
}

type requestToRetry struct {
	requestToPoll
	LastAttemptAt time.Time
}

// Poller - maintains queue of requestToPoll and periodically refreshes our observations by calling CaptureObservation.
// A request remains in the queue until its context is canceled to ensure that in case of reorg or errors we eventually capture
// valid observation. Example: CallContract should be polled until quorum of nodes has reached requested block.
// Request polls after initial, occur with a delay defined by pollPeriod.
type Poller struct {
	// service state management
	services.Service
	engine *services.Engine

	lggr       logger.SugaredLogger
	maxWorkers int
	pollPeriod time.Duration

	mutex       sync.Mutex
	inputNotify chan struct{}
	requests    *list.List[requestToPoll]
	requestsCh  chan requestToPoll
	retryQueue  *list.List[requestToRetry]
}

func NewPoller(lggr logger.Logger, maxWorkers int, pollPeriod time.Duration) *Poller {
	p := &Poller{
		maxWorkers: maxWorkers,
		pollPeriod: pollPeriod,

		inputNotify: make(chan struct{}, 1),
		requests:    list.New[requestToPoll](),
		requestsCh:  make(chan requestToPoll, maxWorkers),
		retryQueue:  list.New[requestToRetry](),
	}

	p.Service, p.engine = services.Config{
		Name:  "Poller",
		Start: p.start,
		Close: p.close,
	}.NewServiceEngine(lggr)

	p.lggr = p.engine.SugaredLogger
	return p
}

func (p *Poller) Enqueue(ctx context.Context, request types.ObservableRequest) {
	p.mutex.Lock()
	p.enqueueUnsafe(requestToPoll{
		ObservableRequest: request,
		Ctx:               ctx,
	})
	p.mutex.Unlock()
}

func (p *Poller) popFirst() *requestToPoll {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.requests.Len() == 0 {
		return nil
	}

	// TODO PLEX-1572: report requests queue size to beholder
	result := p.requests.Remove(p.requests.Front())
	return &result
}

func (p *Poller) enqueueUnsafe(rq requestToPoll) {
	p.requests.PushBack(rq)
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
		p.lggr.Debugw("request was canceled - removing from queue", "requestID", request.ID())
		return
	}

	err := request.CaptureObservation(ctx)
	if err != nil {
		p.lggr.Warnw("failed to capture observation", "err", err, "requestID", request.ID())
	} else {
		// TODO: some requests might need only one successful read (finalized data)
		p.lggr.Debugw("captured observation", "requestID", request.ID())
	}

	p.mutex.Lock()
	p.retryQueue.PushBack(requestToRetry{
		requestToPoll: request,
		LastAttemptAt: time.Now(),
	})
	p.mutex.Unlock()
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
			request := p.popFirst()
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

		// TODO PLEX-1572: report retryQueue queue size to beholder
		p.retryQueue.Remove(request)

		p.enqueueUnsafe(request.Value.requestToPoll)
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
