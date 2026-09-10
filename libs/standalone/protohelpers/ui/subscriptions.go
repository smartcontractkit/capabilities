package ui

import (
	"encoding/json"
	"net/http"
)

// The endpoints behind the subscription sidebar. Registering a trigger happens
// through the form like anything else - see shim.go - so what is left is the
// things a reader does with a subscription that already exists: list them, watch
// one, acknowledge an event, close it.

// subscriptions lists every live subscription, which is the sidebar.
func (f *fanout) subscriptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	// A subscription can appear or be closed at any moment, so this is never worth
	// serving from a cache.
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(struct {
		Subscriptions []Status `json:"subscriptions"`
	}{Subscriptions: f.hub.List()})
}

// stream is one reader watching one subscription.
//
// Also how a subscription's lifetime is decided: the response ends when the
// browser goes away, and a subscription nobody is watching is unregistered once
// the grace period passes. Closing the window is the intended way to close a
// subscription, and a reload is a window closing - hence the grace rather than
// closing on the spot.
func (f *fanout) stream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}

	triggerID := r.URL.Query().Get("trigger")
	if triggerID == "" {
		writeError(w, userErrorf("trigger is required"))
		return
	}
	f.hub.Stream(w, r, triggerID)
}

// ackRequest is one event a reader has seen.
type ackRequest struct {
	TriggerID string `json:"triggerId"`
	EventID   string `json:"eventId"`
}

// ack forwards a reader's acknowledgement to the instances that delivered the
// event.
//
// The browser acks rather than this acking on delivery: a capability that
// redelivers what was not acknowledged is behaving correctly, and acknowledging
// an event nobody has been shown would hide the redelivery being looked for.
func (f *fanout) ack(w http.ResponseWriter, r *http.Request) {
	var req ackRequest
	if !f.readCommand(w, r, &req) {
		return
	}
	if req.TriggerID == "" || req.EventID == "" {
		writeError(w, userErrorf("triggerId and eventId are required"))
		return
	}

	if err := f.hub.Ack(req.TriggerID, req.EventID); err != nil {
		writeError(w, err)
		return
	}
	ok(w)
}

// unsubscribeRequest closes a subscription, or detaches the instances named.
type unsubscribeRequest struct {
	TriggerID string `json:"triggerId"`
	// Instances empty means the whole subscription, which is what the sidebar's
	// close does. Naming instances detaches those and leaves the rest running.
	Instances []int `json:"instances"`
}

func (f *fanout) unsubscribe(w http.ResponseWriter, r *http.Request) {
	var req unsubscribeRequest
	if !f.readCommand(w, r, &req) {
		return
	}
	if req.TriggerID == "" {
		writeError(w, userErrorf("triggerId is required"))
		return
	}

	if err := f.hub.Unsubscribe(req.TriggerID, req.Instances); err != nil {
		writeError(w, err)
		return
	}
	ok(w)
}

// triggerID mints one, for a page that is about to subscribe without naming it.
//
// The page asks rather than being told afterwards, so it knows the ID before the
// first event can arrive: the subscription it is about to open a stream on is the
// one it is registering.
func (f *fanout) triggerID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(struct {
		TriggerID string `json:"triggerId"`
	}{TriggerID: NewTriggerID()})
}

// readCommand checks a POST and decodes its body, answering the request itself if
// either fails.
//
// The same CSRF check the fan-out uses, because these do the same kind of thing:
// they reach a capability, so they are no more open to a hostile page than
// invoking one is.
func (f *fanout) readCommand(w http.ResponseWriter, r *http.Request, into any) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return false
	}

	cookie, err := r.Cookie(fanoutCookieName)
	if err != nil || cookie.Value == "" || cookie.Value != r.Header.Get(fanoutHeaderName) {
		http.Error(w, "incorrect CSRF token", http.StatusUnauthorized)
		return false
	}

	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		writeError(w, userErrorf("bad request body: %s", err))
		return false
	}
	return true
}

// ok answers a command that did what it was asked.
func ok(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}
