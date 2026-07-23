// Package capabilityrunner provides a standalone.BootstrapDependency for
// capability binaries launched by the node's capabilityrunner job (via the
// empty LOOP). It binds the required --http_port flag and provides a
// limits.Factory backed by CRE settings updates.
//
// Update protocol (must match core's capabilityrunner delegate): on each
// settings update the node dumps the settings TOML to
// os.TempDir()/cre_limits/<name> and hits
// http://localhost:{http_port}/reload/<name>. The Runner reads the file,
// stores it into its settings getter, and returns 2xx on success; any other
// status is treated as a failed reload by the node.
package capabilityrunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/libs/standalone"
)

// limitsDirName names the directory (under os.TempDir()) the node dumps
// settings updates to. Must match core's capabilityrunner delegate.
const limitsDirName = "cre_limits"

// limitsDir is the directory reload files are read from.
func limitsDir() string { return filepath.Join(os.TempDir(), limitsDirName) }

// Dependency returns a standalone.BootstrapDependency that binds the required
// --http_port flag and resolves a *Runner.
func Dependency(lggr logger.Logger) standalone.BootstrapDependency[*Runner] {
	// Wrap in OnceBootstrapper so Get runs at most once even if several
	// services resolve this dependency.
	return standalone.OnceBootstrapper[*Runner](&dependency{lggr: lggr})
}

type dependency struct {
	lggr     logger.Logger
	httpPort int
}

var _ standalone.BootstrapDependency[*Runner] = (*dependency)(nil)

func (d *dependency) AddCommands(cmd *cobra.Command) {
	cmd.PersistentFlags().IntVar(&d.httpPort, "http_port", 0, "port for the capability runner HTTP server (limits reload endpoint)")
	if err := cmd.MarkPersistentFlagRequired("http_port"); err != nil {
		panic(err) // only errors if the flag does not exist
	}
}

func (d *dependency) Get(ctx context.Context, cc standalone.CommonConfig) (*Runner, error) {
	if d.httpPort <= 0 || d.httpPort > 65535 {
		return nil, fmt.Errorf("invalid --http_port %d", d.httpPort)
	}
	return newRunner(d.lggr, d.httpPort), nil
}

// Runner serves the limits reload endpoint and exposes a limits.Factory backed
// by the reloaded settings. It is a services.Service: include it in the
// services returned to the bootstrapper so the HTTP server runs.
type Runner struct {
	services.Service
	eng *services.Engine

	httpPort int
	settings *loop.AtomicSettings

	// LimitsFactory builds limiters that follow the reloaded settings
	// dynamically; new values apply on the next limit evaluation.
	LimitsFactory limits.Factory

	server *http.Server
}

func newRunner(lggr logger.Logger, httpPort int) *Runner {
	r := &Runner{
		httpPort: httpPort,
		// Seeded with defaults (CL_CRE_SETTINGS env var, if set) until the
		// first reload arrives.
		settings: loop.NewAtomicSettings(cresettings.DefaultGetter),
	}
	r.LimitsFactory = limits.Factory{
		Settings: r.settings,
		Logger:   logger.Named(lggr, "LimitsFactory"),
	}
	r.Service, r.eng = services.Config{
		Name:  "CapabilityRunner",
		Start: r.start,
		Close: r.close,
	}.NewServiceEngine(lggr)
	return r
}

func (r *Runner) start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/reload/", r.handleReload)

	addr := fmt.Sprintf("localhost:%d", r.httpPort)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	r.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	r.eng.Go(func(context.Context) {
		r.eng.Infow("capability runner serving", "address", lis.Addr().String())
		if serr := r.server.Serve(lis); serr != nil && !errors.Is(serr, http.ErrServerClosed) {
			r.eng.Errorw("capability runner HTTP server stopped", "err", serr)
		}
	})
	return nil
}

func (r *Runner) close() error {
	if r.server != nil {
		return r.server.Close()
	}
	return nil
}

// handleReload reads the dumped settings file named by the URL suffix and
// stores it as the current settings. 2xx signals a successful reload.
func (r *Runner) handleReload(w http.ResponseWriter, req *http.Request) {
	name := path.Base(req.URL.Path)
	if name == "" || name == "." || name == "/" || strings.ContainsAny(name, `/\`) {
		http.Error(w, "invalid reload file name", http.StatusBadRequest)
		return
	}

	b, err := os.ReadFile(filepath.Join(limitsDir(), name))
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	shaSum := sha256.Sum256(b)
	update := core.SettingsUpdate{Settings: string(b), Hash: hex.EncodeToString(shaSum[:])}
	if err := r.settings.Store(update); err != nil {
		http.Error(w, fmt.Sprintf("invalid settings: %v", err), http.StatusUnprocessableEntity)
		return
	}

	r.eng.Infow("reloaded settings", "file", name, "hash", update.Hash)
	w.WriteHeader(http.StatusOK)
}
