package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities"
	"github.com/smartcontractkit/chainlink/v2/core/logger"
	"github.com/smartcontractkit/chainlink/v2/plugins"

	"go.uber.org/zap/zapcore"
)

type server struct {
	capabilities       loop.StandardCapabilities
	capabilityRegistry *capabilities.Registry
	logger             logger.Logger
	closeLogger        func() error
}

var loggerCfg logger.Config

func init() {
	logger.InitColor(true)
	loggerCfg = logger.Config{
		LogLevel: zapcore.DebugLevel,
	}
}

func setup() *server {
	l, closeLogger := loggerCfg.New()

	loopReg := plugins.NewLoopRegistry(l, nil, nil)

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
		svc    = loop.NewStandardCapabilitiesService(l, opts, cmdFn)
		capReg = capabilities.NewRegistry(l)
	)

	l.Info("starting capabilities service")
	if err := svc.Start(ctx); err != nil {
		l.Fatalf("Failed to start service: %v", err)
	}

	if err := svc.WaitCtx(ctx); err != nil {
		l.Fatalf("Failed to wait for service: %v", err)
	}

	l.Info("initialising capabilities service")
	if err := svc.Service.Initialise(ctx, "", nil, nil, capReg, nil, nil, nil); err != nil {
		l.Fatalf("Failed to initialise service: %v", err)
	}

	l.Info("capabilities server started successfully")
	return &server{
		capabilities:       svc.Service,
		capabilityRegistry: capReg,
		logger:             l,
		closeLogger:        closeLogger,
	}
}

type capabilityInfo struct {
	ID             string `json:"id"`
	CapabilityType string `json:"capability_type"`
	Description    string `json:"description"`
}

func (s *server) Infos(w http.ResponseWriter, r *http.Request) {
	infos, err := s.capabilities.Infos(r.Context())
	if err != nil {
		s.logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.logger.Info("handling infos request")

	for _, info := range infos {
		desc, err := json.Marshal(capabilityInfo{
			ID:             info.ID,
			CapabilityType: string(info.CapabilityType),
			Description:    info.Description,
		})
		if err != nil {
			s.logger.Error(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write([]byte(desc))
	}
}

func main() {
	s := setup()
	defer s.closeLogger()

	s.logger.Info("starting http server on :8080")
	http.HandleFunc("/infos", s.Infos)
	if err := http.ListenAndServe(":8080", nil); err != nil {
		s.logger.Fatal(err)
	}
}
