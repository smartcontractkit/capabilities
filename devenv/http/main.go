package main

import (
	"context"
	"log"
	"net/http"

	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities"
	"github.com/smartcontractkit/chainlink/v2/core/logger"
	"github.com/smartcontractkit/chainlink/v2/plugins"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap/zapcore"
)

var lggrCfg logger.Config

func init() {
	logger.InitColor(true)
	lggrCfg = logger.Config{
		LogLevel: zapcore.DebugLevel,
	}
}

func main() {
	// setup capabilities server
	srv := setup()
	defer srv.closeLogger()

	// setup http routing and delegate requests to capabilities server
	router := chi.NewRouter()
	router.Get("/infos", srv.Infos)
	router.Post("/capability/get/info", srv.GetCapabilityInfo)
	router.Post("/capability/target/execute", srv.TargetExecute)

	// start http server
	srv.logger.Info("starting http server on :8080")
	if err := http.ListenAndServe(":8080", router); err != nil {
		srv.logger.Fatal(err)
	}
}

func setup() *server {
	lggr, closeLggr := lggrCfg.New()

	loopReg := plugins.NewLoopRegistry(lggr, nil, nil)

	prCfg := plugins.NewRegistrarConfig(
		loop.NewGRPCOpts(nil),
		loopReg.Register,
		loopReg.Unregister,
	)
	cmdFn, opts, err := prCfg.RegisterLOOP(plugins.CmdConfig{
		ID:  "KVStoreStandardCapability",
		Cmd: "kvstore", // name of the plugin binary installed in GOBIN
		Env: nil,
	})
	if err != nil {
		log.Fatal(err)
	}

	var (
		ctx    = context.Background()
		svc    = loop.NewStandardCapabilitiesService(lggr, opts, cmdFn)
		capReg = capabilities.NewRegistry(lggr)
		store  = NewStore(lggr)
	)

	lggr.Info("starting capabilities service")
	if err := svc.Start(ctx); err != nil {
		lggr.Fatalf("Failed to start service: %v", err)
	}

	if err := svc.WaitCtx(ctx); err != nil {
		lggr.Fatalf("Failed to wait for service: %v", err)
	}

	lggr.Info("initialising capabilities service")
	if err := svc.Service.Initialise(ctx, "", nil, store, capReg, nil, nil, nil); err != nil {
		lggr.Fatalf("Failed to initialise service: %v", err)
	}

	lggr.Info("capabilities server started successfully")
	return &server{
		capabilities:       svc.Service,
		capabilityRegistry: capReg,
		logger:             lggr,
		closeLogger:        closeLggr,
	}
}
