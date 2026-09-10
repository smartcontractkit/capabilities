package ui

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// The page's own stylesheet and scripts. Both pages share the stylesheet, and the
// form page's script is served from grpcui's asset route while the fan-out page's
// is served from ours.
var (
	//go:embed resources/page.css
	pageCSS string

	//go:embed resources/form.js
	formJS string

	// The arithmetic for the special value types, shared by both pages: the
	// encoding has to match Go's big.Int and decimal.Decimal exactly, and two
	// copies of that would be two things to keep right. Prepended to each page's
	// own script rather than served separately, so it cannot load second.
	//go:embed resources/values.js
	valuesJS string

	//go:embed resources/request.js
	requestJS string

	// The subscription sidebar and its tables. Its own file rather than more of
	// request.js: it is driven by what arrives on a stream rather than by the form,
	// so the only thing the two share is the page they are on.
	//go:embed resources/subscriptions.js
	subscriptionsJS string

	//go:embed resources/request.html
	requestHTML string
)

// Assets are served under content-hashed names.
//
// grpcui serves what it is given with "Cache-Control: private, max-age=3600" and
// only revalidates its index, so at a fixed name a rebuilt binary would not reach
// an already-open browser for an hour - which looks exactly like the page being
// broken. A name that changes with the content cannot be served stale.
func hashedName(base, content, ext string) string {
	sum := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%s.%s.%s", base, hex.EncodeToString(sum[:])[:12], ext)
}

func cssFileName() string             { return hashedName("cre-debug", pageCSS, "css") }
func jsFileName(served string) string { return hashedName("cre-debug", served, "js") }
func requestJSFileName() string {
	return hashedName("cre-debug-request", valuesJS+requestJS, "js")
}
func subscriptionsJSFileName() string {
	return hashedName("cre-debug-subscriptions", subscriptionsJS, "js")
}

// customJS is form.js with the page's configuration prepended, so the browser has
// it without a second request.
//
// The configuration is what keeps the page from guessing: the metadata fields come
// from the RequestMetadata type, and the methods from the descriptors the
// capabilities were generated against.
func customJS(s *Server) (string, error) {
	cfg := struct {
		Metadata []Field `json:"metadata"`
		Prefix   string  `json:"headerPrefix"`
		// Subscriptions are the services whose methods register a trigger, and
		// TriggerIDHeader is what the trigger ID travels in. Both come from the
		// descriptors rather than the page working out which is which from a name.
		Subscriptions   []string `json:"subscriptions"`
		TriggerIDHeader string   `json:"triggerIdHeader"`
		// Path is where the pages are mounted, so the form can link to the fan-out
		// page - which is where a subscription's events are shown.
		Path string `json:"prefix"`
		// Special are the messages the form offers a number for instead of their
		// fields, and where a response holds them. See special.go.
		Special SpecialConfig `json:"special"`
	}{
		Metadata:        Fields(),
		Prefix:          HeaderPrefix,
		Subscriptions:   s.subscriptionServices(),
		TriggerIDHeader: TriggerIDHeader,
		Path:            s.prefix,
		Special:         s.specialConfig(),
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("encoding the debug page config: %w", err)
	}
	return "window.__CRE_DEBUG__ = " + string(encoded) + ";\n" + valuesJS + "\n" + formJS, nil
}
