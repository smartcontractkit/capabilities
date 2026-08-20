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

// Mount serves this instance's debug page on mux, and adds it to fleet so the
// fan-out page can reach it.
//
// prefix roots both pages: prefix+"/ui/" is the form for this instance's own
// capabilities, and prefix+"/request" is the fan-out over every instance. Both are
// mounted on every instance, so whichever port a browser lands on can drive the
// whole process.
func Mount(mux *http.ServeMux, prefix string, s *Server, fleet *Fleet, index int, title string) error {
	if mux == nil {
		return fmt.Errorf("a mux is required")
	}
	if prefix == "" {
		prefix = DefaultPrefix
	}
	prefix = "/" + strings.Trim(prefix, "/")

	served, err := customJS(s)
	if err != nil {
		return err
	}

	page := standalone.Handler(
		s,
		title,
		s.Methods(),
		s.Files(),
		standalone.AddCSSFile(cssFileName(), func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(pageCSS)), nil
		}),
		standalone.AddJSFile(jsFileName(served), func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(served)), nil
		}),
		// The metadata a capability is called with comes from the browser, so
		// every header a RequestMetadata field travels in has to reach Invoke.
		standalone.PreserveHeaders(HeaderNames()),
	)

	uiPath := prefix + "/ui"
	mux.Handle(uiPath+"/", http.StripPrefix(uiPath, page))

	fleet.Add(&Instance{
		Index:   index,
		Label:   fmt.Sprintf("instance %d", index+1),
		handler: page,
	})

	f := &fanout{fleet: fleet, prefix: prefix, uiPath: uiPath, server: s}
	mux.HandleFunc(prefix+"/request", f.page)
	mux.HandleFunc(prefix+"/request/", f.page)
	mux.HandleFunc(prefix+"/request/fanout", f.invoke)
	mux.HandleFunc(prefix+"/request/s/", f.asset)

	// A bare prefix is otherwise a 404, which reads as the page not being there.
	mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, uiPath+"/", http.StatusFound)
	})
	return nil
}

// fanout serves the page that sends one or more requests across every instance.
type fanout struct {
	fleet  *Fleet
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
	return pageConfig{
		Instances: f.fleet.List(),
		Services:  services,
		UIPath:    f.uiPath,
		Prefix:    f.prefix,
		Metadata:  Fields(),
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
		CSSFile string
		JSFile  string
		UIPath  string
		Prefix  string
		Config  template.JS
	}{
		CSSFile: cssFileName(),
		JSFile:  requestJSFileName(),
		UIPath:  f.uiPath,
		Prefix:  f.prefix,
		Config:  template.JS(encoded),
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
		_, _ = io.WriteString(w, requestJS)
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

// instanceResult is one row of the fan-out. Status is "ok" when the instance
// answered, "error" when the call failed, and "na" when it was not addressed.
type instanceResult struct {
	Instance int             `json:"instance"`
	Label    string          `json:"label"`
	Status   string          `json:"status"`
	Group    int             `json:"group"`
	Response json.RawMessage `json:"response,omitempty"`
	Error    string          `json:"error,omitempty"`
}

type fanoutResponse struct {
	Method  string           `json:"method"`
	Results []instanceResult `json:"results"`
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
	metadata, err := MetadataFromHeaders(func(name string) []string { return req.Metadata[name] })
	if err != nil {
		writeError(w, err)
		return
	}
	header := HeadersFromMetadata(metadata)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(f.run(req, header)); err != nil {
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

	rows := make(map[int]instanceResult, len(jobs))
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
			} else {
				row.Response = response
			}

			mu.Lock()
			defer mu.Unlock()
			rows[j.instance.Index] = row
		}(j)
	}
	wg.Wait()

	out := fanoutResponse{Method: req.Method, Results: make([]instanceResult, 0, len(all))}
	for _, in := range all {
		if row, ok := rows[in.Index]; ok {
			out.Results = append(out.Results, row)
			continue
		}
		out.Results = append(out.Results, instanceResult{Instance: in.Index, Label: in.Label, Status: "na"})
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
