package consensus

import (
	"context"
	"fmt"
	"sync"
	"time"

	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/list"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/types"
)

type Poller interface {
	Enqueue(ctx context.Context, request types.ObservableRequest)
}

type Reader struct {
	// service state management
	services.Service
	engine *services.Engine

	lggr     logger.SugaredLogger
	lock     sync.RWMutex
	requests *priorityQueue
	poller   Poller

	unknownRequestsResultByID       map[string]*unknownRequest
	unknownRequestsOrderedByTimeout *list.List[*unknownRequest]
	unknownRequestTTL               time.Duration
}

func NewReader(lggr logger.Logger, poller Poller, unknownRequestTTL time.Duration) *Reader {
	r := &Reader{
		requests:                        newPriorityQueue(),
		unknownRequestsResultByID:       make(map[string]*unknownRequest),
		unknownRequestsOrderedByTimeout: list.New[*unknownRequest](),
		unknownRequestTTL:               unknownRequestTTL,
		poller:                          poller,
	}

	r.Service, r.engine = services.Config{
		Name:  "ConsensusRequestsStore",
		Start: r.start,
	}.NewServiceEngine(lggr)

	r.lggr = r.engine.SugaredLogger
	return r
}

type unknownRequest struct {
	ID        string
	ExpiresAt time.Time
	Element   *list.Element[*unknownRequest]
	Result    []byte
}

type requestCtx struct {
	types.Request
	//nolint:containedctx // Justification: required to track request's timeout
	Ctx        context.Context
	Cancel     context.CancelFunc
	ResultChan chan []byte
	Attempt    int
}

func (s *Reader) start(Ctx context.Context) error {
	s.engine.GoTick(services.TickerConfig{Initial: time.Second}.NewTicker(time.Second), s.removeExpiredRequests)
	return nil
}

// GetRequestIDs - returns `limit` of request IDs in ascending order by number of attempts. Requests remain in the queue.
func (s *Reader) GetRequestIDs(limit int) ([]string, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	requestIDs := make([]string, 0, limit)
	requests := make([]*requestCtx, 0, limit)
	for len(requestIDs) < limit && s.requests.Len() > 0 {
		request := s.requests.Pop()
		if request.Ctx.Err() != nil {
			continue
		}
		requestIDs = append(requestIDs, request.ID())
		requests = append(requests, request)
	}

	// add requests back to the queue, as we can remove them only once they are fully processed
	for _, request := range requests {
		s.requests.Push(request)
	}

	return requestIDs, nil
}

func (s *Reader) MarkAttempted(requestID string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.requests.IncreaseAttempt(requestID)
}

func (s *Reader) GetRequest(id string) (types.Request, bool) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.requests.GetByID(id)
}

func (s *Reader) CompleteRequest(id string, report *evmservice.RequestReport) error {
	switch report.Report.(type) {
	case *evmservice.RequestReport_LockableToBlock:
		return s.completeLockableRequest(id, report.GetLockableToBlock())
	case *evmservice.RequestReport_EventuallyConsistent:
		return s.completeEventuallyConsistentRequest(id, report.GetEventuallyConsistent())
	default:
		return fmt.Errorf("unknown request type %T", report.Report)
	}
}

func (s *Reader) completeLockableRequest(id string, height *evmservice.ChainHeight) error {
	if height == nil {
		return fmt.Errorf("chain height is nil for report with requestID %s", id)
	}

	s.lock.Lock()
	defer s.lock.Unlock()
	rawRequest, ok := s.requests.GetByID(id)
	if !ok {
		s.lggr.Infof("lockable to a block request %s not found", id)
		return nil
	}

	request, ok := rawRequest.Request.(*types.LockableToBlockRequest)
	if !ok {
		// might be because we've already converted it to eventually consistent
		s.lggr.Infof("lockable to a block request %s is of a different type %T", id, rawRequest.Request)
		return nil
	}

	newRequest := request.ToEventuallyConsistent(height)
	oldRequestCtx, ok := s.requests.Remove(newRequest.ID())
	if !ok {
		s.lggr.Warnf("lockable to a block request %s not found while removing", id)
		return nil
	}
	oldRequestCtx.Request = newRequest
	oldRequestCtx.Attempt = 0
	s.addRequestCtx(oldRequestCtx)
	s.lggr.Infof("locked request %s to height %v", id, height)
	return nil
}

func (s *Reader) completeEventuallyConsistentRequest(id string, value []byte) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	request, ok := s.requests.GetByID(id)
	if !ok {
		uRequest := &unknownRequest{
			ID:        id,
			ExpiresAt: time.Now().Add(s.unknownRequestTTL),
			Result:    value,
		}
		uRequest.Element = s.unknownRequestsOrderedByTimeout.PushBack(uRequest)
		s.unknownRequestsResultByID[id] = uRequest
		return nil
	}

	s.requests.Remove(id)
	request.ResultChan <- value // non blocking as ResultChan is buffered

	// cancel request to prevent further polling
	request.Cancel()
	return nil
}

func (s *Reader) Read(ctx context.Context, request types.Request) (<-chan []byte, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	ch := make(chan []byte, 1)
	uRequest, ok := s.unknownRequestsResultByID[request.ID()]
	if ok {
		ch <- uRequest.Result // non-blocking as ch is buffered
		delete(s.unknownRequestsResultByID, request.ID())
		s.unknownRequestsOrderedByTimeout.Remove(uRequest.Element)
		return ch, nil
	}

	_, ok = s.requests.GetByID(request.ID())
	if ok {
		return nil, fmt.Errorf("request with id %s already exists", request.ID())
	}

	ctx, cancel := s.engine.Ctx(ctx)
	s.addRequestCtx(&requestCtx{
		Request:    request,
		Ctx:        ctx,
		Cancel:     cancel,
		ResultChan: ch,
	})

	return ch, nil
}

func (s *Reader) addRequestCtx(requestCtx *requestCtx) {
	s.requests.Push(requestCtx)
	switch tRequest := requestCtx.Request.(type) {
	case *types.EventuallyConsistentRequest:
		s.poller.Enqueue(requestCtx.Ctx, tRequest)
	}
}

func (s *Reader) removeExpiredRequests(ctx context.Context) {
	s.lock.Lock()
	defer s.lock.Unlock()
	now := time.Now()
	for s.unknownRequestsOrderedByTimeout.Len() > 0 {
		request := s.unknownRequestsOrderedByTimeout.Front()
		if request.Value.ExpiresAt.After(now) {
			break
		}
		delete(s.unknownRequestsResultByID, request.Value.ID)
		s.unknownRequestsOrderedByTimeout.Remove(request)
	}
}
