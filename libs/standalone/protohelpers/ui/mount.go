package ui

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/fullstorydev/grpcui/standalone"
)

// grpcui keeps these unexported, so the names are repeated here: the fan-out
// speaks the same CSRF scheme as the page it calls into.
const (
	csrfCookieName = "_grpcui_csrf_token"
	csrfHeaderName = "x-grpcui-csrf-token"

	// The fan-out page gets its own names. grpcui scopes its cookie to the page's
	// own path, so a same-named cookie higher up would be sent alongside it and
	// could shadow the page's token.
	fanoutCookieName = "_cre_debug_csrf_token"
	fanoutHeaderName = "x-cre-debug-csrf-token"
)

// DefaultPrefix is where the debug pages are mounted.
const DefaultPrefix = "/debug/capabilities"

// Options is what mounting one instance's debug page needs.
type Options struct {
	// Mux is what the pages are served on: the instance's own HTTP server, so a
	// browser on any instance's port can drive the whole process.
	Mux *http.ServeMux
	// Prefix roots both pages. Empty means DefaultPrefix.
	Prefix string
	// Server is this instance's capabilities.
	Server *Server
	// Fleet is every instance's page, so the fan-out can reach a sibling.
	Fleet *Fleet
	// Hub holds the subscriptions. Shared with every other instance, so a trigger
	// registered across several of them is one subscription with a column each.
	Hub *Hub
	// Index is this instance's number, which names it on the fan-out page and on
	// every event it delivers.
	Index int
	// Title is what the per-instance page calls itself.
	Title string
}

// Mount serves this instance's debug page, and adds it to the fleet so the
// fan-out page can reach it.
//
// prefix roots both pages: prefix+"/ui/" is the form for this instance's own
// capabilities, and prefix+"/request" is the fan-out over every instance. Both are
// mounted on every instance, so whichever port a browser lands on can drive the
// whole process.
func Mount(o Options) error {
	if o.Mux == nil {
		return fmt.Errorf("a mux is required")
	}
	if o.Server == nil {
		return fmt.Errorf("a server is required")
	}
	if o.Fleet == nil {
		return fmt.Errorf("a fleet is required")
	}
	if o.Hub == nil {
		return fmt.Errorf("a subscription hub is required")
	}

	prefix := o.Prefix
	if prefix == "" {
		prefix = DefaultPrefix
	}
	prefix = "/" + strings.Trim(prefix, "/")

	mux, s := o.Mux, o.Server
	label := fmt.Sprintf("instance %d", o.Index+1)

	// Which instance this is, and where its subscriptions go. Set here rather than
	// passed to New because this is where an instance's identity is known: New is
	// given capabilities, and a capability does not know which instance is hosting
	// it.
	s.hub = o.Hub
	s.index = o.Index
	s.label = label
	s.prefix = prefix

	served, err := customJS(s)
	if err != nil {
		return err
	}

	page := standalone.Handler(
		s,
		o.Title,
		s.Methods(),
		s.Files(),
		standalone.AddCSSFile(cssFileName(), func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(pageCSS)), nil
		}),
		standalone.AddJSFile(jsFileName(served), func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(served)), nil
		}),
		// The metadata a capability is called with comes from the browser, and so
		// does the trigger ID a subscription is identified by, so every header
		// either travels in has to reach Invoke.
		standalone.PreserveHeaders(PreservedHeaders()),
	)

	uiPath := prefix + "/ui"
	mux.Handle(uiPath+"/", http.StripPrefix(uiPath, page))

	o.Fleet.Add(&Instance{
		Index:   o.Index,
		Label:   label,
		handler: page,
	})

	f := &fanout{fleet: o.Fleet, hub: o.Hub, prefix: prefix, uiPath: uiPath, server: s}
	mux.HandleFunc(prefix+"/request", f.page)
	mux.HandleFunc(prefix+"/request/", f.page)
	mux.HandleFunc(prefix+"/request/fanout", f.invoke)
	mux.HandleFunc(prefix+"/request/s/", f.asset)

	// The subscriptions: what is running, what they have delivered, and the two
	// things a reader does about it.
	mux.HandleFunc(prefix+"/request/subscriptions", f.subscriptions)
	mux.HandleFunc(prefix+"/request/subscriptions/stream", f.stream)
	mux.HandleFunc(prefix+"/request/subscriptions/ack", f.ack)
	mux.HandleFunc(prefix+"/request/subscriptions/close", f.unsubscribe)
	mux.HandleFunc(prefix+"/request/trigger-id", f.triggerID)

	// A bare prefix is otherwise a 404, which reads as the page not being there.
	mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, uiPath+"/", http.StatusFound)
	})
	return nil
}

// fanout serves the page that sends one or more requests across every instance.
type fanout struct {
	fleet  *Fleet
	hub    *Hub
	prefix string
	uiPath string
	server *Server

	once     sync.Once
	template *template.Template
	tmplErr  error
}

// pageConfig is what request.js needs to build the page.
type pageConfig struct {
	Instances []*Instance         `json:"instances"`
	Services  map[string][]string `json:"services"`
	UIPath    string              `json:"uiPath"`
	Prefix    string              `json:"prefix"`
	// Metadata is every RequestMetadata field, which is what the page's Advanced
	// section is built from rather than a hand-written list of inputs.
	Metadata []Field `json:"metadata"`

	// Subscriptions are the services whose methods register a trigger rather than
	// calling it. Invoking one opens a subscription instead of returning a
	// response, so the page has to know which it is looking at.
	Subscriptions []string `json:"subscriptions"`
	// TriggerIDHeader is what the trigger ID travels in, so the page does not
	// repeat the name.
	TriggerIDHeader string `json:"triggerIdHeader"`
	// Special are the messages shown as the number they stand for rather than as
	// the fields they are made of, and where each method's response holds them.
	// See special.go.
	Special SpecialConfig `json:"special"`
}

func (f *fanout) config() pageConfig {
	services := map[string][]string{}
	for key := range f.server.calls {
		service, method, found := strings.Cut(key, "/")
		if !found {
			continue
		}
		services[service] = append(services[service], method)
	}
	for _, methods := range services {
		sort.Strings(methods)
	}

	return pageConfig{
		Instances:       f.fleet.List(),
		Services:        services,
		UIPath:          f.uiPath,
		Prefix:          f.prefix,
		Metadata:        Fields(),
		Subscriptions:   f.server.subscriptionServices(),
		TriggerIDHeader: TriggerIDHeader,
		Special:         f.server.specialConfig(),
	}
}

func (f *fanout) page(w http.ResponseWriter, r *http.Request) {
	f.once.Do(func() {
		f.template, f.tmplErr = template.New("request.html").Parse(requestHTML)
	})
	if f.tmplErr != nil {
		writeError(w, systemErrorf("the debug page template is invalid: %w", f.tmplErr))
		return
	}

	ensureCSRFCookie(w, r)

	encoded, err := json.Marshal(f.config())
	if err != nil {
		writeError(w, systemErrorf("failed to encode the debug page config: %w", err))
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Must revalidate, or a rebuild would not reach an open browser.
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")

	data := struct {
		CSSFile           string
		JSFile            string
		SubscriptionsFile string
		UIPath            string
		Prefix            string
		Config            template.JS
	}{
		CSSFile:           cssFileName(),
		JSFile:            requestJSFileName(),
		SubscriptionsFile: subscriptionsJSFileName(),
		UIPath:            f.uiPath,
		Prefix:            f.prefix,
		Config:            template.JS(encoded),
	}
	if err := f.template.Execute(w, data); err != nil {
		return
	}
}

// asset serves the fan-out page's own script, and the shared stylesheet, under
// their content-hashed names.
func (f *fanout) asset(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, max-age=3600")
	switch path.Base(r.URL.Path) {
	case requestJSFileName():
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		// The shared arithmetic first, so the page's own script can rely on it.
		_, _ = io.WriteString(w, valuesJS+"\n"+requestJS)
	case subscriptionsJSFileName():
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = io.WriteString(w, subscriptionsJS)
	case cssFileName():
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = io.WriteString(w, pageCSS)
	default:
		http.NotFound(w, r)
	}
}

// requestGroup is one request body plus the instances it is addressed to.
type requestGroup struct {
	Instances []int           `json:"instances"`
	Body      json.RawMessage `json:"body"`
}

type fanoutRequest struct {
	// Method is dot-separated, the way grpcui's invoke route expects it.
	Method string         `json:"method"`
	Groups []requestGroup `json:"groups"`
	// Metadata is the request metadata every group is sent with. The page fills it
	// in before sending, so each instance is called with the same metadata and a
	// difference between their answers is the capability's rather than the call's.
	Metadata map[string][]string `json:"metadata"`
}

// instanceResult is one instance's answer. Status is "ok" when the instance
// answered, "error" when the call failed, and "na" when it was not addressed.
type instanceResult struct {
	Instance int    `json:"instance"`
	Label    string `json:"label"`
	Status   string `json:"status"`
	Group    int    `json:"group"`
	// ResponseID is the hash of what this instance answered, and ResponseIndex is
	// which of the fan-out's distinct responses that is.
	//
	// The response itself is on the fan-out rather than here, for the same reason a
	// trigger event's payload is on its row: instances answering identically is the
	// normal case, and holding it per instance would repeat the same JSON once per
	// instance to say they matched.
	ResponseID    string `json:"responseId,omitempty"`
	ResponseIndex int    `json:"responseIndex"`
	Error         string `json:"error,omitempty"`
}

type fanoutResponse struct {
	Method  string           `json:"method"`
	Results []instanceResult `json:"results"`
	// TriggerID is the subscription every group was registered under, for a
	// fan-out that subscribed rather than called. Reported back because the page
	// needs it to open the stream, and because a caller that named none still has
	// to be told which one it got.
	TriggerID string `json:"triggerId,omitempty"`

	// The distinct responses, and whether the instances disagreed. Same shape as a
	// trigger event's row, because it is the same question: what did each instance
	// say, and did they all say it.
	payloadSet
}

func (f *fanout) invoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	// The same CSRF shape grpcui uses, so this is no easier to drive from a
	// hostile page than the pages behind it.
	cookie, err := r.Cookie(fanoutCookieName)
	if err != nil || cookie.Value == "" || cookie.Value != r.Header.Get(fanoutHeaderName) {
		http.Error(w, "incorrect CSRF token", http.StatusUnauthorized)
		return
	}

	var req fanoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, userErrorf("bad request body: %s", err))
		return
	}
	if req.Method == "" {
		writeError(w, userErrorf("method is required"))
		return
	}

	// Resolved here, once, rather than letting each instance fill in its own
	// defaults: the unspecified fields include the execution ID, so instances left
	// to their own devices would each invent a different one and what the user
	// asked for as a single request would arrive as several. Doing it here also
	// means a value that will not parse is one 400 rather than the same complaint
	// from every instance.
	get := func(name string) []string { return req.Metadata[name] }

	metadata, err := MetadataFromHeaders(get)
	if err != nil {
		writeError(w, err)
		return
	}
	header := HeadersFromMetadata(metadata)

	// Settled here for the same reason, and it matters more: the trigger ID is
	// what identifies a subscription, so instances left to mint their own would
	// each start a subscription of their own and the one table the user asked for
	// would be four.
	triggerID := TriggerIDFromHeaders(get)
	header.Set(TriggerIDHeader, triggerID)

	response := f.run(req, header)

	// The trigger ID is reported only when it names a subscription that exists.
	// A registration every instance refused leaves none behind - see Hub.subscribe -
	// and the refusal arrives as an answer rather than as a failed call, so the rows
	// above can all say "ok" with nothing registered. Naming it anyway would put a
	// row in the sidebar for something that is not running.
	if f.hub != nil && f.hub.Live(triggerID) {
		response.TriggerID = triggerID
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		return
	}
}

// run sends each group to the instances it names, and returns one row per
// instance so an unaddressed one still reports N/A.
//
// Every instance is called at once, not one after another. An OCR capability
// cannot answer one instance until enough of the others have joined the same
// round, so asking them in turn would leave the first waiting for a quorum that
// has not been invited yet - the call would time out rather than return. Even for
// a capability that could answer alone, "all of them at once" is what a fan-out
// is for.
func (f *fanout) run(req fanoutRequest, header http.Header) fanoutResponse {
	all := f.fleet.List()
	byIndex := map[int]*Instance{}
	for _, in := range all {
		byIndex[in.Index] = in
	}

	// The work is collected before any of it runs, so the whole fan-out starts
	// together rather than a group at a time.
	type job struct {
		instance *Instance
		group    int
		body     []byte
	}
	var jobs []job
	claimed := map[int]bool{}
	for gi, group := range req.Groups {
		for _, idx := range group.Instances {
			in, ok := byIndex[idx]
			if !ok || claimed[idx] {
				// Unknown instance, or one an earlier group already claimed: the
				// page keeps the groups disjoint, this is the backstop.
				continue
			}
			claimed[idx] = true
			jobs = append(jobs, job{instance: in, group: gi + 1, body: group.Body})
		}
	}

	type answer struct {
		result   instanceResult
		response json.RawMessage
	}

	answers := make(map[int]answer, len(jobs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()

			row := instanceResult{
				Instance: j.instance.Index,
				Label:    j.instance.Label,
				Group:    j.group,
				Status:   "ok",
			}
			response, err := j.instance.invoke(req.Method, j.body, header)
			if err != nil {
				row.Status = "error"
				row.Error = err.Error()
				response = nil
			}

			mu.Lock()
			defer mu.Unlock()
			answers[j.instance.Index] = answer{result: row, response: response}
		}(j)
	}
	wg.Wait()

	// Collected in instance order rather than as they arrived, so the responses are
	// numbered the same way twice in a row for the same fan-out. Concurrency makes
	// arrival order arbitrary, and a debug page that renumbers its columns between
	// two identical runs is a page that looks like it found something.
	out := fanoutResponse{Method: req.Method, Results: make([]instanceResult, 0, len(all))}
	for _, in := range all {
		got, ok := answers[in.Index]
		if !ok {
			out.Results = append(out.Results, instanceResult{
				Instance:      in.Index,
				Label:         in.Label,
				Status:        "na",
				ResponseIndex: -1,
			})
			continue
		}

		// The hash is of the bytes the instance's page produced, which is what the
		// form generator rendered and what is about to be shown.
		row := got.result
		if len(got.response) > 0 {
			row.ResponseID = shortHash(got.response)
		}
		row.ResponseIndex = out.add(row.ResponseID, got.response)
		out.Results = append(out.Results, row)
	}
	return out
}

// ensureCSRFCookie mirrors what grpcui does for its own pages, so the fan-out page
// has a token to send back.
func ensureCSRFCookie(w http.ResponseWriter, r *http.Request) {
	if _, err := r.Cookie(fanoutCookieName); err == nil {
		return
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:  fanoutCookieName,
		Value: base64.RawURLEncoding.EncodeToString(buf),
		Path:  "/",
	})
}
