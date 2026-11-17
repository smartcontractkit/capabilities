package capability

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"
)

type debugSubscriptions struct {
	done <-chan struct{}

	mu     sync.Mutex
	all    []*debugSubscription
	byID   map[string]*debugSubscription
	update chan struct{}
}

type debugSubscription struct {
	id     string
	method string
	input  json.RawMessage
	stop   context.CancelFunc
	done   chan struct{}

	// unregister is retried by unsubscribe while unregisterErr says it failed, so the subscription isn't closed
	// while it may still be registered.
	unregister    func(context.Context) error
	unregisterErr error
	// retrying serializes retries, so concurrent Stops don't unregister at once or overwrite each other's outcome.
	retrying sync.Mutex

	// events are the latest, and dropped how many before them were let go.
	events  []debugResult
	dropped int
	closed  bool
}

// maxDebugEvents is how many of a subscription's events are kept, so one that keeps firing can't grow without bound.
const maxDebugEvents = 1000

func newDebugSubscriptions(done <-chan struct{}) debugSubscriptions {
	return debugSubscriptions{done: done, byID: map[string]*debugSubscription{}, update: make(chan struct{})}
}

func (s *debugSubscriptions) add(sub *debugSubscription) {
	s.change(func() {
		s.all = append(s.all, sub)
		s.byID[sub.id] = sub
	})
}

func (s *debugSubscriptions) publish(id string, r debugResult) {
	s.change(func() {
		sub := s.byID[id]
		sub.events = append(sub.events, r)
		if over := len(sub.events) - maxDebugEvents; over > 0 {
			sub.events = slices.Delete(sub.events, 0, over)
			sub.dropped += over
		}
	})
}

func (s *debugSubscriptions) stopped(sub *debugSubscription, err error) {
	s.change(func() {
		sub.unregisterErr = err
		sub.closed = err == nil
	})
}

func (s *debugSubscriptions) retryUnregister(ctx context.Context, sub *debugSubscription) error {
	sub.retrying.Lock()
	defer sub.retrying.Unlock()

	s.mu.Lock()
	failed := sub.unregisterErr != nil
	s.mu.Unlock()
	if !failed {
		return nil
	}

	err := sub.unregister(ctx)
	s.stopped(sub, err)
	return err
}

func triggerID(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body struct{ TriggerID string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", false
	}

	return body.TriggerID, true
}

func (s *debugSubscriptions) remove(w http.ResponseWriter, r *http.Request) {
	id, ok := triggerID(w, r)
	if !ok {
		return
	}

	var err string
	s.change(func() {
		sub, ok := s.byID[id]
		switch {
		case !ok:
			err = "no subscription " + id
		case !sub.closed:
			err = "stop " + id + " before deleting it"
		default:
			delete(s.byID, sub.id)
			s.all = slices.DeleteFunc(s.all, func(other *debugSubscription) bool { return other == sub })
		}
	})

	if err != "" {
		http.Error(w, err, http.StatusBadRequest)
		return
	}

	writeDebugJSON(w, struct{}{})
}

func (s *debugSubscriptions) change(f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f()
	close(s.update)
	s.update = make(chan struct{})
}

func (s *debugSubscriptions) unsubscribe(w http.ResponseWriter, r *http.Request) {
	id, ok := triggerID(w, r)
	if !ok {
		return
	}

	s.mu.Lock()
	sub, ok := s.byID[id]
	s.mu.Unlock()
	if !ok {
		http.Error(w, "no subscription "+id, http.StatusBadRequest)
		return
	}

	sub.stop()
	<-sub.done
	if err := s.retryUnregister(r.Context(), sub); err != nil {
		http.Error(w, "failed to unregister "+id+": "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeDebugJSON(w, struct{}{})
}

type debugSubscriptionView struct {
	TriggerID string
	Method    string
	Input     json.RawMessage
	Closed    bool
	Deleted   bool
	// Events are only those after the ones the page already has.
	Events []debugResult
	Total  int
}

func (s *debugSubscriptions) stream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	sent := map[string]sentState{}
	for {
		views, update := s.changes(sent)
		if !send(w, views) {
			return
		}

		select {
		case <-update:
		case <-r.Context().Done():
			return
		case <-s.done:
			return
		}
	}
}

// sentState is how much of a subscription a stream has sent.
type sentState struct {
	events int
	closed bool
}

// changes returns what changed since sent, updating it, and the channel closed on the next change.
func (s *debugSubscriptions) changes(sent map[string]sentState) ([]debugSubscriptionView, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var views []debugSubscriptionView
	for _, sub := range s.all {
		now := sentState{events: sub.dropped + len(sub.events), closed: sub.closed}
		before, known := sent[sub.id]
		if known && before == now {
			continue
		}

		views = append(views, debugSubscriptionView{
			TriggerID: sub.id,
			Method:    sub.method,
			Input:     sub.input,
			Closed:    sub.closed,
			// A copy, as publish shifts the events once there are too many.
			Events: slices.Clone(sub.events[max(before.events-sub.dropped, 0):]),
			Total:  now.events,
		})

		sent[sub.id] = now
	}

	for id := range sent {
		if _, ok := s.byID[id]; !ok {
			views = append(views, debugSubscriptionView{TriggerID: id, Deleted: true})
			delete(sent, id)
		}
	}

	return views, s.update
}

// send writes views as server-sent events, reporting whether the stream is still open.
func send(w http.ResponseWriter, views []debugSubscriptionView) bool {
	for _, v := range views {
		b, err := json.Marshal(v)
		if err != nil {
			return false
		}

		if _, err = fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return false
		}
	}

	return http.NewResponseController(w).Flush() == nil
}
