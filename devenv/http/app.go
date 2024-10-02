package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities"
	"github.com/smartcontractkit/chainlink/v2/core/logger"
	"github.com/smartcontractkit/chainlink/v2/plugins"
)

type application struct {
	registrarConfig plugins.RegistrarConfig
	ids             []string // loop ids

	handler    *handler
	svc        *loop.StandardCapabilitiesService
	httpServer *http.Server

	logger      logger.Logger
	closeLogger func() error

	wg sync.WaitGroup
}

func newApp() *application {
	var (
		lggr, closeLggr = lggrCfg.New()

		loopReg = plugins.NewLoopRegistry(lggr, nil, nil)

		cfg = plugins.NewRegistrarConfig(
			loop.NewGRPCOpts(nil),
			loopReg.Register,
			loopReg.Unregister,
		)
	)

	return &application{
		logger:          lggr,
		closeLogger:     closeLggr,
		registrarConfig: cfg,
	}

}

// registerLOOP registers a LOOP plugin with the given cmd and loopID in the loop registry.
// Initializes the capabilities service for the LOOP plugin.
//
// TODO(mstreet3): Refactor so that registration, initialization and cleanup of the LOOP plugin are
// managed by the handler.
func (app *application) registerLOOP(ctx context.Context, cmd, loopID string) error {
	cmdFn, opts, err := app.registrarConfig.RegisterLOOP(plugins.CmdConfig{
		ID:  loopID,
		Cmd: cmd,
	})
	if err != nil {
		app.logger.Errorf("Failed to register LOOP plugin: %v", err)
		return err
	}

	app.ids = append(app.ids, loopID)

	var (
		capabilityRegistry = capabilities.NewRegistry(app.logger)
		kvstore            = NewStore(app.logger)
	)

	svc := loop.NewStandardCapabilitiesService(app.logger, opts, cmdFn)
	app.logger.Info("starting handler")
	if err := svc.Start(ctx); err != nil {
		app.logger.Errorf("Failed to start service: %v", err)
		return err
	}

	if err := svc.WaitCtx(ctx); err != nil {
		app.logger.Errorf("Failed to wait for service: %v", err)
		return err
	}

	app.logger.Info("initialising capabilities service")
	if err := svc.Service.Initialise(ctx, "", nil, kvstore, capabilityRegistry, nil, nil, nil); err != nil {
		app.logger.Errorf("Failed to initialise service: %v", err)
		return err
	}

	// Register the capabilities service for the LOOPP
	app.handler = newHandler(svc.Service, capabilityRegistry, kvstore, app.logger)
	app.svc = svc
	return nil
}

// startHTTPServer starts an http server on the given port with the given router.
// Cleanup is done by calling Close on the application.
func (app *application) startHTTPServer(port int, router http.Handler) {
	app.logger.Info(fmt.Sprintf("starting http server on :%d", port))

	app.httpServer = &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      router,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// start http server in a goroutine
	app.wg.Add(1)
	go func() {
		defer app.wg.Done()

		// always returns error. ErrServerClosed on graceful close
		if err := app.httpServer.ListenAndServe(); err != http.ErrServerClosed {
			// unexpected error.
			app.logger.Fatalf("ListenAndServe(): %v", err)
		}
	}()
}

// run is the blocking method of the application, it waits for the context to cancel and then
// shuts down the application.
func (app *application) run(ctx context.Context) {
	// cleanup app on signal
	app.wg.Add(1)
	go func() {
		defer app.wg.Done()

		<-ctx.Done()

		app.logger.Debug("shutting down app")

		ctxwt, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := app.Close(ctxwt); err != nil {
			panic(err)
		}

		app.logger.Debug("app shutdown successfully")
	}()

	// serve until signaled
	app.wg.Wait()
}

func (app *application) Close(ctx context.Context) error {
	for _, id := range app.ids {
		app.registrarConfig.UnregisterLOOP(id)
	}

	var multierr error

	if app.httpServer != nil {
		app.logger.Debug("shutting down http server")
		if err := app.httpServer.Shutdown(ctx); err != nil {
			multierr = errors.Join(multierr, err)
		}
	}

	if app.svc != nil {
		app.logger.Debug("shutting down capabilities service")
		// TODO(mstreet3): This should be a graceful shutdown, but plugin is not shutdown gracefully
		// Plugin process exits without error, but logs an error log.
		if err := app.svc.Close(); err != nil {
			multierr = errors.Join(multierr, err)
		}
	}

	if app.closeLogger != nil {
		app.logger.Debug("closing logger")
		multierr = errors.Join(multierr, app.closeLogger())
	}

	return multierr
}
