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

	//go:embed resources/request.js
	requestJS string

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
func requestJSFileName() string       { return hashedName("cre-debug-request", requestJS, "js") }

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
	}{
		Metadata: Fields(),
		Prefix:   HeaderPrefix,
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("encoding the debug page config: %w", err)
	}
	return "window.__CRE_DEBUG__ = " + string(encoded) + ";\n" + formJS, nil
}
