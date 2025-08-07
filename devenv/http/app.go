package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink/v2/plugins"
)

type application struct {
	registrarConfig plugins.RegistrarConfig
	handler         *handler

	httpServer *http.Server

	logger      logger.Logger
	closeLogger func() error

	wg sync.WaitGroup
}

func newApp() *application {
	var (
		lggr, closeLggr = lggrCfg.New()

		loopReg = plugins.NewLoopRegistry(lggr, "", false, nil, nil, nil, nil, nil, "")

		cfg = plugins.NewRegistrarConfig(
			loop.NewGRPCOpts(nil),
			loopReg.Register,
			loopReg.Unregister,
		)
	)

	return &application{
		logger:          lggr,
		closeLogger:     func() error { return closeLggr },
		registrarConfig: cfg,
	}

}

// registerLOOP registers a LOOP plugin with the given cmd and loopID in the loop registry.
// Initializes the capabilities service for the LOOP plugin.
func (app *application) registerLOOP(ctx context.Context, cmd, loopID string) error {
	h, err := newHandler(app.logger, app.registrarConfig, cmd, loopID)
	if err != nil {
		return err
	}
	app.handler = h
	return app.handler.Start(ctx)
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
	var multierr error

	if app.httpServer != nil {
		app.logger.Debug("shutting down http server")
		if err := app.httpServer.Shutdown(ctx); err != nil {
			multierr = errors.Join(multierr, err)
		}
	}

	if app.handler != nil {
		app.logger.Debug("closing handler")
		multierr = errors.Join(multierr, app.handler.Close())
	}

	if app.closeLogger != nil {
		app.logger.Debug("closing logger")
		multierr = errors.Join(multierr, app.closeLogger())
	}

	return multierr
}
