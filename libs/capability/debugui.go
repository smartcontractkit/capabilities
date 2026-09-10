package capability

import (
	"context"
	"fmt"
	"net/http"

	"github.com/smartcontractkit/capabilities/libs/standalone/protohelpers/ui"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// mountDebugUI serves the debug page for the capability this binary hosts.
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
