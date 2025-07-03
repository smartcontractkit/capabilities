package consensus

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/requests"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/list"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/types"
)

type Poller interface {
	Enqueue(ctx context.Context, request types.ObservableRequest)
}

type Handler struct {
	// service state management
	services.Service
	engine *services.Engine

	lggr     logger.SugaredLogger
	lock     sync.RWMutex
	requests *requests.Store[*requestCtx]
	poller   Poller

	unknownRequestsResultByID       map[string]*unknownRequest
	unknownRequestsOrderedByTimeout *list.List[*unknownRequest]
	unknownRequestTTL               time.Duration
}

func NewHandler(lggr logger.Logger, poller Poller, unknownRequestTTL time.Duration) *Handler {
	r := &Handler{
		requests:                        requests.NewStore[*requestCtx](),
		unknownRequestsResultByID:       make(map[string]*unknownRequest),
		unknownRequestsOrderedByTimeout: list.New[*unknownRequest](),
		unknownRequestTTL:               unknownRequestTTL,
		poller:                          poller,
	}

	r.Service, r.engine = services.Config{
		Name:  "EVMConsensusHandler",
		Start: r.start,
	}.NewServiceEngine(lggr)

	r.lggr = r.engine.SugaredLogger
	return r
}

type unknownRequest struct {
	ID        string
	ExpiresAt time.Time
	Element   *list.Element[*unknownRequest]
	Result    any
}

type requestCtx struct {
	types.Request
	//nolint:containedctx // Justification: required to track request's timeout
	Ctx        context.Context
	Cancel     context.CancelFunc
	ResultChan chan any
}

func (r *requestCtx) ID() string {
	return r.Request.ID()
}

func (r *requestCtx) Copy() *requestCtx {
	return &requestCtx{
		Request: r.Request.Copy(),
		// explicitly not copying as usage is thread safe
		Ctx:        r.Ctx,
		Cancel:     r.Cancel,
		ResultChan: r.ResultChan,
	}
}

func (s *Handler) start(Ctx context.Context) error {
	s.engine.GoTick(services.TickerConfig{Initial: time.Second}.NewTicker(time.Second), s.removeExpiredRequests)
	return nil
}

// GetRequestIDs - returns `limit` of request IDs in ascending order by number of attempts. Requests remain in the queue.
func (s *Handler) GetRequestIDs(limit int) ([]string, error) {
	request, err := s.requests.FirstN(limit)
	if err != nil {
		return nil, err
	}
	requestIDs := make([]string, 0, limit)
	for _, r := range request {
		if r.Ctx.Err() != nil {
			s.requests.Evict(r.ID())
			continue
		}
		requestIDs = append(requestIDs, r.ID())
	}

	return requestIDs, nil
}

func (s *Handler) GetRequest(id string) (types.Request, bool) {
	rq := s.requests.Get(id)
	if rq == nil {
		return nil, false
	}
	return rq.Request, true
}

func (s *Handler) CompleteRequest(id string, report *types.RequestReport) error {
	switch report.Report.(type) {
	case *types.RequestReport_Aggregatable:
		return s.completeRequest(id, report.GetAggregatable())
	case *types.RequestReport_LockableToBlock:
		return s.completeLockableRequest(id, report.GetLockableToBlock())
	case *types.RequestReport_EventuallyConsistent:
		return s.completeRequest(id, report.GetEventuallyConsistent())
	default:
		return fmt.Errorf("unknown request type %T", report.Report)
	}
}

func (s *Handler) completeLockableRequest(id string, height *types.ChainHeight) error {
	if height == nil {
		return fmt.Errorf("chain height is nil for report with requestID %s", id)
	}

	s.lock.Lock()
	defer s.lock.Unlock()
	rawRequest := s.requests.Get(id)
	if rawRequest == nil {
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
	oldRequestCtx, ok := s.requests.Evict(newRequest.ID())
	if !ok {
		s.lggr.Warnf("lockable to a block request %s not found while removing", id)
		return nil
	}
	newRequestCtx := oldRequestCtx.Copy()
	newRequestCtx.Request = newRequest
	err := s.addRequestCtx(newRequestCtx)
	if err != nil {
		return fmt.Errorf("failed to readd locked request %s: %w", newRequest.ID(), err)
	}
	s.lggr.Infof("locked request %s to height %v", id, height)
	return nil
}

func (s *Handler) completeRequest(id string, value any) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	request := s.requests.Get(id)
	if request == nil {
		uRequest := &unknownRequest{
			ID:        id,
			ExpiresAt: time.Now().Add(s.unknownRequestTTL),
			Result:    value,
		}
		uRequest.Element = s.unknownRequestsOrderedByTimeout.PushBack(uRequest)
		s.unknownRequestsResultByID[id] = uRequest
		return nil
	}

	s.requests.Evict(id)
	request.ResultChan <- value // non blocking as ResultChan is buffered

	// cancel request to prevent further polling
	request.Cancel()
	return nil
}

func (s *Handler) Handle(ctx context.Context, request types.Request) (<-chan any, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	ch := make(chan any, 1)
	uRequest, ok := s.unknownRequestsResultByID[request.ID()]
	if ok {
		ch <- uRequest.Result // non-blocking as ch is buffered
		delete(s.unknownRequestsResultByID, request.ID())
		s.unknownRequestsOrderedByTimeout.Remove(uRequest.Element)
		return ch, nil
	}

	ctx, cancel := s.engine.Ctx(ctx)
	err := s.addRequestCtx(&requestCtx{
		Request:    request,
		Ctx:        ctx,
		Cancel:     cancel,
		ResultChan: ch,
	})
	if err != nil {
		return nil, err
	}

	return ch, nil
}

func (s *Handler) addRequestCtx(requestCtx *requestCtx) error {
	err := s.requests.Add(requestCtx)
	if err != nil {
		return fmt.Errorf("failed to add request %s: %w", requestCtx.ID(), err)
	}
	switch tRequest := requestCtx.Request.(type) {
	case *types.EventuallyConsistentRequest:
		s.poller.Enqueue(requestCtx.Ctx, tRequest)
	case *types.LockableToBlockRequest:
	case *types.AggregatableRequest:
		s.poller.Enqueue(requestCtx.Ctx, tRequest)
	default:
		return fmt.Errorf("unknown request type %T", tRequest)
	}
	return nil
}

func (s *Handler) removeExpiredRequests(ctx context.Context) {
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
