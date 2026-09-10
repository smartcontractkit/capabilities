package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
)

// Instance is one instance's debug page, as the fan-out reaches it.
//
// It holds the instance's handler rather than its address. Instances of an embed
// run share a process, so a request to a sibling is a call into its handler: no
// port to work out, no socket, and nothing to go wrong between them. It also
// means the fan-out works when the gRPC servers were given port 0 and there is no
// arithmetic that could have found them.
type Instance struct {
	Index int    `json:"index"`
	Label string `json:"label"`

	handler http.Handler
}

// Fleet is every instance's page, shared by pointer between the configured
// dependency and the embedded form each instance resolves - the same way the
// embedded config is shared - so each instance adds itself to one list and any of
// them can reach the rest.
//
// Instances are constructed one after another and register during construction,
// so the list is complete before the process is serving. The mutex is for that
// construction, not for the requests that read it afterwards.
type Fleet struct {
	mu        sync.Mutex
	instances []*Instance
}

// Add registers an instance's page. Called once per instance, during construction.
func (f *Fleet) Add(in *Instance) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.instances = append(f.instances, in)
}

// List is every registered instance, in the order they were added, which is
// instance order.
func (f *Fleet) List() []*Instance {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*Instance, len(f.instances))
	copy(out, f.instances)
	return out
}

// invoke calls one method on this instance and returns what its page answered.
//
// The call goes through the instance's own handler, so it takes exactly the path a
// browser's request would: the same decoding of the posted JSON into the method's
// message, the same CSRF check, the same wrapping into a CapabilityRequest. Only
// the transport is skipped.
func (in *Instance) invoke(method string, body []byte, header http.Header) (json.RawMessage, error) {
	// The index page is what issues the CSRF cookie, exactly as it would over a
	// socket. Asking for it here keeps the handler's own check meaningful rather
	// than working around it.
	index := httptest.NewRecorder()
	in.handler.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/", nil))

	token := ""
	for _, c := range index.Result().Cookies() {
		if c.Name == csrfCookieName {
			token = c.Value
			break
		}
	}
	if token == "" {
		return nil, fmt.Errorf("instance %d did not issue a %s cookie", in.Index, csrfCookieName)
	}

	req := httptest.NewRequest(http.MethodPost, "/invoke/"+method, bytes.NewReader(body))
	for name, values := range header {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrfHeaderName, token)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})

	rec := httptest.NewRecorder()
	in.handler.ServeHTTP(rec, req)

	result := rec.Result()
	defer result.Body.Close()
	if result.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("instance %d returned %d: %s", in.Index, result.StatusCode, bytes.TrimSpace(rec.Body.Bytes()))
	}
	return json.RawMessage(rec.Body.Bytes()), nil
}
