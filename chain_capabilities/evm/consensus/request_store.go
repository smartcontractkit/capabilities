package consensus

import (
	"container/list"
	"sync"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/chain_capabilities/evm/consensus/types"
)

type Reader interface {
	GetRequestIDs(limit int) ([]string, error)
	// GetRequest returns request for specified ID. Return nil, request is not available or expired.
	GetRequest(id string) types.Request
	// GetObservation - returns bytes representation of request's observation. Returns false, if there is no such request or observation is not yet available.
	GetObservation(id string) (observation []byte, ok bool)
	SetObservation(id string, observation []byte)
}

type notReceivedRequest struct {
	ID        string
	ExpiresAt time.Time
}

type internalRequest struct {
	types.Request
	Observation    []byte
	HasObservation bool
	ResultChan     chan []byte
	Attempt        int
}

type requestStore struct {
	lggr                      logger.SugaredLogger
	lock                      sync.RWMutex
	requestsOrderedByAttempts *priorityQueue

	unknownRequestsResultByID       map[string][]byte
	unknownRequestsOrderedByTimeout *list.List
}

func newRequestStore(lggr logger.Logger) *requestStore {
	return &requestStore{
		lggr:                            logger.Sugared(lggr),
		requestsOrderedByAttempts:       newPriorityQueue(),
		unknownRequestsResultByID:       make(map[string][]byte),
		unknownRequestsOrderedByTimeout: list.New(),
	}
}

// GetRequestIDs - returns `limit` of request IDs in ascending order by number of attempts. Requests remain in the queue.
func (s *requestStore) GetRequestIDs(limit int) ([]string, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	requestIDs := make([]string, 0, limit)
	requests := make([]*internalRequest, 0, limit)
	for len(requestIDs) < limit && s.requestsOrderedByAttempts.Len() > 0 {
		request := s.requestsOrderedByAttempts.Pop()
		if request.Ctx().Err() != nil {
			continue
		}
		requestIDs = append(requestIDs, request.ID())
		requests = append(requests, request)
	}

	// add requests back to the queue, as we can remove them only once they are fully processed
	for _, request := range requests {
		s.requestsOrderedByAttempts.Push(request)
	}

	return requestIDs, nil
}

func (s *requestStore) MarkAttempted(requestIDs []string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	for _, id := range requestIDs {
		s.requestsOrderedByAttempts.IncreaseAttempt(id)
	}
}

func (s *requestStore) GetRequest(id string) (types.Request, bool) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.requestsOrderedByAttempts.GetByID(id)
}

func (s *requestStore) GetObservation(id string) (observation []byte, ok bool) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	request, ok := s.requestsOrderedByAttempts.GetByID(id)
	if !ok {
		return nil, false
	}
	return request.Observation, request.HasObservation
}

func (s *requestStore) SetObservation(id string, observation []byte) {
	s.lock.Lock()
	defer s.lock.Unlock()
	request, ok := s.requestsOrderedByAttempts.GetByID(id)
	if !ok {
		return
	}
	request.Observation = observation
	request.HasObservation = true
}

func (s *requestStore) CompleteRequest(id string, result []byte) {
	s.lock.Lock()
	defer s.lock.Unlock()
	resultChan, ok := s.resultChanByIDs[id]
	if !ok {
		return
	}
	// it's ok to
	s.cleanUpRequest(id)
	resultChan <- result

}

//Update(request types.Request)
//
