package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/go-chi/chi/v5"
	"github.com/moby/moby/pkg/namesgenerator"
	"go.uber.org/zap/zapcore"
)

var (
	lggrCfg     logger.Config
	defaultName string
)

func init() {
	lggrCfg = logger.Config{
		Level: zapcore.DebugLevel,
	}

	// Set a default name for the loopp ID, which is used to identify the loop in the registry
	//
	// TODO(mstreet3): Handle multiple loop registrations
	defaultName = namesgenerator.GetRandomName(0)
}

func main() {
	// Parse command line flags
	var (
		port   = flag.Int("port", 80, "port to run the server on")
		cmd    = flag.String("cmd", "kvstore", "name of the plugin binary installed in GOBIN")
		loopID = flag.String("name", defaultName, "name of the LOOP")
	)
	flag.Parse()

	// handle signals for app shutdown
	signals := []os.Signal{os.Interrupt, os.Kill, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP}
	ctx, stop := signal.NotifyContext(context.Background(), signals...)
	defer stop()

	// create a new app
	app := newApp()

	// register single loop plugin
	//
	// TODO(mstreet3): Add feature to register multiple loop plugins/capablities
	if err := app.registerLOOP(ctx, *cmd, *loopID); err != nil {
		app.logger.Fatalf("failed to register loop: %v", err)
	}

	app.startHTTPServer(*port, createRouter(app.handler))

	app.run(ctx)
}

// createRouter creates a new router and links the handler to the routes
func createRouter(h Handler) *chi.Mux {
	router := chi.NewRouter()
	router.Get("/infos", h.Infos)
	router.Post("/capability/get/info", h.GetCapabilityInfo)
	router.Post("/capability/execute", h.Execute)
	return router
}
