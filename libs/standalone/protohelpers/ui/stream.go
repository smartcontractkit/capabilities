package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Events reach the browser over one long-lived response per subscription, as
// server-sent events.
//
// One-way is all this needs: the page subscribes and acknowledges over ordinary
// requests, and the only thing that has to be pushed is a trigger firing. So there
// is no second protocol here and no dependency to add - and, more usefully, a
// dropped connection is an ordinary request ending, which is what makes closing
// the window close the subscription.

// client is one reader of one subscription.
type client struct {
	// messages is buffered, and a client that fills it is dropped rather than
	// slowing the delivery down. The browser reconnects and is sent a fresh
	// snapshot, so being dropped costs a reader nothing except the gap it had
	// already fallen behind by.
	messages chan Message
	done     chan struct{}
}

func newClient() *client {
	return &client{messages: make(chan Message, clientBuffer), done: make(chan struct{})}
}

// send hands over a message, or reports that this client is too far behind.
func (c *client) send(m Message) bool {
	select {
	case c.messages <- m:
		return true
	default:
		return false
	}
}

// addClient attaches a reader and returns the snapshot it opens with.
//
// Attaching cancels the grace timer: a reader arriving is what "somebody came
// back" means, whether it is a reload of the page that subscribed or a different
// tab entirely.
func (s *subscription) addClient(c *client) Status {
	s.mu.Lock()
	s.clients[c] = struct{}{}
	if s.graceTimer != nil {
		s.graceTimer.Stop()
		s.graceTimer = nil
	}
	closed := s.closed
	s.mu.Unlock()

	status := s.status(closed)
	status.Rows = s.rows()
	return status
}

// removeClient detaches a reader, and starts the grace clock if it was the last.
func (s *subscription) removeClient(c *client) {
	s.mu.Lock()
	delete(s.clients, c)
	last := len(s.clients) == 0 && !s.closed
	s.mu.Unlock()

	if last {
		s.startGrace()
	}
}

// startGrace arms the timer that unregisters this subscription if nobody comes
// back.
func (s *subscription) startGrace() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed || s.graceTimer != nil {
		return
	}
	s.graceTimer = time.AfterFunc(s.hub.grace, func() {
		s.mu.Lock()
		// A reader that arrived while the timer was firing wins: it stopped the
		// timer, and the subscription it came back to should still be there.
		abandoned := len(s.clients) == 0 && !s.closed
		s.mu.Unlock()

		if !abandoned {
			return
		}
		s.hub.remove(s)
		_ = s.close()
	})
}

// broadcast sends a message to every reader, dropping any that has fallen behind.
func (s *subscription) broadcast(m Message) {
	s.mu.Lock()
	clients := make([]*client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()

	for _, c := range clients {
		if !c.send(m) {
			s.drop(c)
		}
	}
}

// drop disconnects one reader.
func (s *subscription) drop(c *client) {
	s.mu.Lock()
	_, present := s.clients[c]
	delete(s.clients, c)
	last := len(s.clients) == 0 && !s.closed
	s.mu.Unlock()

	if present {
		close(c.done)
	}
	if last {
		s.startGrace()
	}
}

// disconnectAll ends every reader's response, which is what a closed subscription
// means to a browser: the stream finishes and the table stops updating.
func (s *subscription) disconnectAll() {
	s.mu.Lock()
	clients := make([]*client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.clients = map[*client]struct{}{}
	s.mu.Unlock()

	for _, c := range clients {
		close(c.done)
	}
}

// Stream serves one subscription's events to one reader.
//
// The response never completes while the subscription is live, so this is also
// how the subscription's lifetime is known: the request's context ends when the
// browser goes away, and that is what starts the grace clock.
func (h *Hub) Stream(w http.ResponseWriter, r *http.Request, triggerID string) {
	s, err := h.get(triggerID)
	if err != nil {
		writeError(w, err)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, systemErrorf("the HTTP server cannot stream: %T is not an http.Flusher", w))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	// Or a proxy in front buffers the whole stream and nothing arrives until it
	// ends, which for this stream is never.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	c := newClient()
	snapshot := s.addClient(c)
	defer s.removeClient(c)

	if !write(w, flusher, Message{Type: MessageSnapshot, TriggerID: triggerID, Status: &snapshot}) {
		return
	}

	// A comment line every so often, so a connection nothing has fired on is not
	// closed by something in the middle for being idle.
	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-c.done:
			return
		case m := <-c.messages:
			if !write(w, flusher, m) {
				return
			}
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// write sends one message, and reports whether the reader is still there.
func write(w http.ResponseWriter, flusher http.Flusher, m Message) bool {
	encoded, err := json.Marshal(m)
	if err != nil {
		// Nothing can be done for the reader here, and the connection is still
		// good: skipping one message beats ending the stream.
		return true
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", encoded); err != nil {
		return false
	}
	flusher.Flush()
	return true
}
