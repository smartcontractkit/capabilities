package capability

import (
	"context"
	"fmt"
	"net/http"

	"github.com/smartcontractkit/capabilities/libs/standalone/protohelpers/ui"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// mountDebugUI serves the debug page for the capability this binary hosts.
//
// The page calls the capability through the registry, as any caller would, so what it exercises
// is the path a workflow takes rather than a way around it.
//
// ctx only reads what the capability already knows: New builds the page's model from Info and the
// service descriptor, and nothing is started.
//
// The fleet and the hub are one each, which is all a run hosting one capability needs. They are
// the shared forms an embed run fans out over - one fleet of every instance's page, one hub of
// every instance's subscriptions - so embed takes these over rather than inventing its own.
func mountDebugUI(ctx context.Context, lggr logger.Logger, mux *http.ServeMux, registry ui.Registry, c Capability) error {
	server, err := ui.New(ctx, registry, c)
	if err != nil {
		return fmt.Errorf("failed to build the capability debug UI: %w", err)
	}

	if err := ui.Mount(ui.Options{
		Mux:    mux,
		Server: server,
		Fleet:  &ui.Fleet{},
		Hub:    ui.NewHub(),
		Title:  "Capability debug",
	}); err != nil {
		return fmt.Errorf("failed to mount the capability debug UI: %w", err)
	}

	lggr.Infow("Serving the capability debug UI", "path", ui.DefaultPrefix+"/ui/", "fanout", ui.DefaultPrefix+"/request")
	return nil
}
