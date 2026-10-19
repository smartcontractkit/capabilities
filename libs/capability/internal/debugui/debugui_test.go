// Package debugui tests the debug UI serving the capability newcapability scaffolds with a trigger and an action,
// over HTTP, as the page uses it.
package debugui

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"

	"github.com/smartcontractkit/capabilities/libs/capability"
	"github.com/smartcontractkit/capabilities/libs/capability/capabilitytest"
	"github.com/smartcontractkit/capabilities/libs/capability/feature"
	"github.com/smartcontractkit/capabilities/libs/capability/internal/example/capability/protos"
	"github.com/smartcontractkit/capabilities/libs/capability/internal/example/capability/service"
)

// result is what the page gets back from invoking a method.
type result struct {
	TriggerID string
	ID        string
	Output    json.RawMessage
	Error     string
	Instances []result
}

// subscription is what the page is sent about a trigger it registered.
type subscription struct {
	TriggerID string
	Method    string
	Input     json.RawMessage
	Closed    bool
	Deleted   bool
	Events    []result
}

// start serves the debug UI for n instances of the example capability.
func start(t *testing.T, n int) *httptest.Server {
	t.Helper()
	return serve(t, capabilitytest.Start(t, service.New, n, capability.WithProvider(func(i *capability.Injector) error {
		return i.ProvideValue(&service.Config{})
	})))
}

func serve[T capability.Capability](t *testing.T, cs []T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	require.NoError(t, capability.ServeDebugUI(t.Context(), mux, cs))
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func request(t *testing.T, method, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })
	return res
}

func post(t *testing.T, s *httptest.Server, path, body string) *http.Response {
	t.Helper()
	return request(t, http.MethodPost, s.URL+capability.DebugPath+path, body)
}

func invoke(t *testing.T, s *httptest.Server, method, input string) result {
	t.Helper()
	res := post(t, s, "/invoke", `{"Method": "`+method+`", "Input": `+input+`}`)
	require.Equal(t, http.StatusOK, res.StatusCode)

	var r result
	require.NoError(t, json.NewDecoder(res.Body).Decode(&r))
	return r
}

// events reads the page's event stream until done.
func events(t *testing.T, s *httptest.Server, done func(subscription) bool) {
	t.Helper()
	lines := bufio.NewScanner(request(t, http.MethodGet, s.URL+capability.DebugPath+"/events", "").Body)
	for lines.Scan() {
		data, ok := strings.CutPrefix(lines.Text(), "data: ")
		if !ok {
			continue
		}

		var v subscription
		require.NoError(t, json.Unmarshal([]byte(data), &v))
		if done(v) {
			return
		}
	}

	t.Fatal("the stream ended")
}

func TestPage(t *testing.T) {
	s := start(t, 2)
	res := request(t, http.MethodGet, s.URL+capability.DebugPath+"/", "")
	require.Equal(t, http.StatusOK, res.StatusCode)
	var page strings.Builder
	_, err := bufio.NewReader(res.Body).WriteTo(&page)
	require.NoError(t, err)
	assert.Contains(t, page.String(), protos.ClientID)
	assert.Contains(t, page.String(), "capabilities.example.capability.x.v1.Request", "the page has the methods' messages")

	assert.Equal(t, http.StatusNotFound, request(t, http.MethodGet, s.URL+capability.DebugPath+"/missing", "").StatusCode)
	assert.Equal(t, http.StatusMethodNotAllowed, request(t, http.MethodGet, s.URL+capability.DebugPath+"/invoke", "").StatusCode)

	// Without the slash, the page's relative requests would miss it.
	res = request(t, http.MethodGet, s.URL+capability.DebugPath, "")
	assert.Equal(t, s.URL+capability.DebugPath+"/", res.Request.URL.String())
}

func TestExecute(t *testing.T) {
	r := invoke(t, start(t, 2), "Execute", `{"message": "hello"}`)
	assert.Empty(t, r.Error)
	assert.JSONEq(t, `{"message": "hello"}`, string(r.Output))
}

// disagree replies with something else than the capability it wraps, so its instances disagree.
type disagree struct {
	*protos.ClientServer
}

func (d disagree) Execute(ctx context.Context, r capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	reply, err := feature.MarshalAny(&protos.Reply{Message: "other"}, false)
	return capabilities.CapabilityResponse{Payload: reply}, err
}

func TestExecuteMismatch(t *testing.T) {
	cs := capabilitytest.Start(t, service.New, 2, capability.WithProvider(func(i *capability.Injector) error {
		return i.ProvideValue(&service.Config{})
	}))
	s := serve(t, []capability.Capability{cs[0], disagree{cs[1]}})

	r := invoke(t, s, "Execute", `{"message": "hello"}`)
	assert.Equal(t, "instances disagree", r.Error)
	require.Len(t, r.Instances, 2)
	assert.JSONEq(t, `{"message": "hello"}`, string(r.Instances[0].Output))
	assert.JSONEq(t, `{"message": "other"}`, string(r.Instances[1].Output))
}

func TestBadInput(t *testing.T) {
	s := start(t, 1)
	assert.Equal(t, http.StatusBadRequest, post(t, s, "/invoke", `{"Method": "Execute", "Input": {"message": 1}}`).StatusCode)
	assert.Equal(t, http.StatusBadRequest, post(t, s, "/invoke", `{"Method": "Missing", "Input": {}}`).StatusCode)
}

func TestTrigger(t *testing.T) {
	s := start(t, 2)
	first := invoke(t, s, "Trigger", `{"message": "one"}`).TriggerID
	second := invoke(t, s, "Trigger", `{"message": "two"}`).TriggerID
	require.NotEmpty(t, first)
	require.NotEqual(t, first, second)

	seen := map[string]subscription{}
	events(t, s, func(v subscription) bool {
		if len(v.Events) > 0 {
			seen[v.TriggerID] = v
		}
		return len(seen) == 2
	})
	assert.Equal(t, "Trigger", seen[first].Method)
	assert.JSONEq(t, `{"message": "one"}`, string(seen[first].Input))
	assert.Equal(t, first, seen[first].Events[0].ID, "the example names its event after its trigger")
	assert.JSONEq(t, `{"message": "one"}`, string(seen[first].Events[0].Output))
	assert.JSONEq(t, `{"message": "two"}`, string(seen[second].Events[0].Output))
	// The example does not implement acks, which the page shows with the event.
	assert.Contains(t, seen[first].Events[0].Error, "failed to ack")
}

func TestUnsubscribeAndDelete(t *testing.T) {
	s := start(t, 1)
	id := invoke(t, s, "Trigger", `{"message": "x"}`).TriggerID

	// The example's trigger keeps running after its event, so it must be stopped before it is deleted.
	assert.Equal(t, http.StatusBadRequest, post(t, s, "/delete", `{"TriggerID": "`+id+`"}`).StatusCode)

	require.Equal(t, http.StatusOK, post(t, s, "/unsubscribe", `{"TriggerID": "`+id+`"}`).StatusCode)
	deleted := false
	events(t, s, func(v subscription) bool {
		if v.TriggerID == id && v.Closed && !deleted {
			deleted = true
			require.Equal(t, http.StatusOK, post(t, s, "/delete", `{"TriggerID": "`+id+`"}`).StatusCode)
		}
		// Open pages are told it is gone.
		return v.TriggerID == id && v.Deleted
	})
	assert.Equal(t, http.StatusBadRequest, post(t, s, "/delete", `{"TriggerID": "`+id+`"}`).StatusCode)
}
