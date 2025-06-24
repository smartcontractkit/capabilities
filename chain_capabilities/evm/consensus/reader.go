package consensus

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/list"

	"github.com/smartcontractkit/chain_capabilities/evm/consensus/types"
)

type Poller interface {
	Enqueue(ctx context.Context, request types.EventuallyConsistentRequest)
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

func NewReader(lggr logger.Logger, unknownRequestTTL time.Duration) *Reader {
	r := &Reader{
		requests:                        newPriorityQueue(),
		unknownRequestsResultByID:       make(map[string]*unknownRequest),
		unknownRequestsOrderedByTimeout: list.New[*unknownRequest](),
		unknownRequestTTL:               unknownRequestTTL,
	}

	r.Service, r.engine = services.Config{
		Name:  "ConsensusRequestsStore",
		Start: r.start,
	}.NewServiceEngine(lggr)

	r.lggr = r.engine.SugaredLogger
	return r
}

func (s *Reader) SetPoller(poller Poller) {
	s.poller = poller
}

type unknownRequest struct {
	ID        string
	ExpiresAt time.Time
	Element   *list.Element[*unknownRequest]
	Result    []byte
}

type requestCtx struct {
	types.Request
	Ctx            context.Context
	Cancel         context.CancelFunc
	Observation    []byte
	HasObservation bool
	ResultChan     chan []byte
	Attempt        int
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

func (s *Reader) GetObservation(id string) (observation []byte, ok bool) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	request, ok := s.requests.GetByID(id)
	if !ok {
		return nil, false
	}
	return request.Observation, request.HasObservation
}

func (s *Reader) SetObservation(id string, observation []byte) {
	s.lock.Lock()
	defer s.lock.Unlock()
	request, ok := s.requests.GetByID(id)
	if !ok {
		return
	}
	request.Observation = observation
	request.HasObservation = true
}

func (s *Reader) CompleteRequest(id string, result []byte) {
	s.lock.Lock()
	defer s.lock.Unlock()
	request, ok := s.requests.GetByID(id)
	if !ok {
		uRequest := &unknownRequest{
			ID:        id,
			ExpiresAt: time.Now().Add(s.unknownRequestTTL),
			Result:    result,
		}
		uRequest.Element = s.unknownRequestsOrderedByTimeout.PushBack(uRequest)
		s.unknownRequestsResultByID[id] = uRequest
		return
	}

	s.requests.Remove(id)
	select {
	case request.ResultChan <- result: // non blocking as ResultChan is buffered
	}

	// cancel request to prevent further polling
	request.Cancel()
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
	case types.EventuallyConsistentRequest:
		s.poller.Enqueue(requestCtx.Ctx, tRequest)
	}
}

func (s *Reader) Update(newRequest types.Request) {
	s.lock.Lock()
	defer s.lock.Unlock()
	oldRequestCtx, ok := s.requests.Remove(newRequest.ID())
	if !ok {
		return
	}
	oldRequestCtx.Request = newRequest
	oldRequestCtx.Attempt = 0
	s.addRequestCtx(oldRequestCtx)
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
