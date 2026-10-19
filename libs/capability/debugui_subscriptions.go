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

	events []debugResult
	closed bool
}

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
	s.change(func() { s.byID[id].events = append(s.byID[id].events, r) })
}

func (s *debugSubscriptions) close(id string) {
	s.change(func() { s.byID[id].closed = true })
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
	type state struct {
		events int
		closed bool
	}

	sent := map[string]state{}
	for {
		s.mu.Lock()
		var views []debugSubscriptionView
		for _, sub := range s.all {
			now := state{events: len(sub.events), closed: sub.closed}
			before, known := sent[sub.id]
			if known && before == now {
				continue
			}

			views = append(views, debugSubscriptionView{
				TriggerID: sub.id,
				Method:    sub.method,
				Input:     sub.input,
				Closed:    sub.closed,
				Events:    sub.events[before.events:],
				Total:     now.events,
			})
			sent[sub.id] = now
		}

		for id := range sent {
			if _, ok := s.byID[id]; !ok {
				views = append(views, debugSubscriptionView{TriggerID: id, Deleted: true})
				delete(sent, id)
			}
		}

		update := s.update
		s.mu.Unlock()

		for _, v := range views {
			b, err := json.Marshal(v)
			if err != nil {
				return
			}

			if _, err = fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return
			}
		}

		if http.NewResponseController(w).Flush() != nil {
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
