package poller

import (
	"container/list"
	"context"
	"sync"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"

	"github.com/smartcontractkit/chain_capabilities/evm/consensus/types"
)

type ObservationsStore interface {
	SetObservation(id string, observation []byte)
}

type processedRequest struct {
	Request     types.EventuallyConsistentRequest
	ProcessedAt time.Time
}

// Poller - maintains queue of request and periodically refreshes our observations for those that request multiple observations
type Poller struct {
	// service state management
	services.Service
	engine *services.Engine

	lggr       logger.SugaredLogger
	store      ObservationsStore
	pollPeriod time.Duration
	maxWorkers int

	rwMutex        sync.RWMutex
	inputNotify    chan struct{}
	requests       *list.List
	requestsCh     chan types.EventuallyConsistentRequest
	processedQueue *list.List
}

func NewPoller(lggr logger.Logger, store ObservationsStore, maxWorkers int, pollPeriod time.Duration) *Poller {
	p := &Poller{
		requestsCh:  make(chan types.EventuallyConsistentRequest, maxWorkers),
		pollPeriod:  pollPeriod,
		inputNotify: make(chan struct{}, 1),
		store:       store,
		maxWorkers:  maxWorkers,
	}

	p.Service, p.engine = services.Config{
		Name:  "WorkerGroup",
		Start: p.start,
		Close: p.close,
	}.NewServiceEngine(lggr)

	p.lggr = p.engine.SugaredLogger
	return p
}

func (p *Poller) Enqueue(request types.EventuallyConsistentRequest) {
	p.rwMutex.Lock()
	p.enqueueUnsafe(request)
	p.rwMutex.Unlock()
}

func (p *Poller) enqueueUnsafe(request types.EventuallyConsistentRequest) {
	p.requests.PushBack(request)
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

// processRequest fetches observations and adds request into retry queue if needed.
func (p *Poller) processRequest(ctx context.Context, request types.EventuallyConsistentRequest) {
	if request.Ctx().Err() != nil {
		p.lggr.Debugw("request was canceled - removing from queue", "request", request.ID())
		return
	}
	observation, err := request.Observe(ctx)
	if err != nil {
		p.lggr.Warnw("failed to capture observation", "err", err, "request", request.ID())
	} else {
		// TODO: some requests might need only one successful read (finalized data)
		p.store.SetObservation(request.ID(), observation)
	}

	p.rwMutex.Lock()
	p.processedQueue.PushBack(processedRequest{
		Request:     request,
		ProcessedAt: time.Now(),
	})
	p.rwMutex.Unlock()
	return
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
			p.processRequest(ctx, request)
		}
	}
}

func (p *Poller) popFirst() types.EventuallyConsistentRequest {
	p.rwMutex.RLock()
	defer p.rwMutex.RUnlock()
	front := p.requests.Front()
	if front == nil {
		return nil
	}

	p.requests.Remove(front)
	return front.Value.(types.EventuallyConsistentRequest)
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
			case p.requestsCh <- request:
			}

		}
	}
}

func (p *Poller) scheduleReadyForReprocessing(ctx context.Context, now time.Time) {
	p.rwMutex.Lock()
	defer p.rwMutex.Unlock()
	for {
		if ctx.Err() != nil {
			return
		}
		front := p.processedQueue.Front()
		pRequest := front.Value.(processedRequest)
		if pRequest.ProcessedAt.Add(p.pollPeriod).Before(now) {
			return
		}

		p.enqueueUnsafe(pRequest.Request)
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
