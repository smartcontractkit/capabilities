package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	common "github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
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

type server struct {
	capabilities       loop.StandardCapabilities
	capabilityRegistry *capabilities.Registry
	logger             logger.Logger
	closeLogger        func() error
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
	)

	lggr.Info("starting capabilities service")
	if err := svc.Start(ctx); err != nil {
		lggr.Fatalf("Failed to start service: %v", err)
	}

	if err := svc.WaitCtx(ctx); err != nil {
		lggr.Fatalf("Failed to wait for service: %v", err)
	}

	lggr.Info("initialising capabilities service")
	if err := svc.Service.Initialise(ctx, "", nil, nil, capReg, nil, nil, nil); err != nil {
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

func (s *server) GetCapabilityInfo(w http.ResponseWriter, r *http.Request) {
	type body struct {
		ID string `json:"id"`
	}
	var b body
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		s.logger.Error(err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cap, err := s.capabilityRegistry.Get(r.Context(), b.ID)
	if err != nil {
		s.logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	info, err := cap.Info(r.Context())
	if err != nil {
		s.logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

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

	w.Write(desc)
}

// TODO(mstreet3): actually pass input request to the target
func (s *server) TargetExecute(w http.ResponseWriter, r *http.Request) {
	type body struct {
		ID      string `json:"id"`
		Request string `json:"request"`
	}
	var b body
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		s.logger.Error(err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cap, err := s.capabilityRegistry.GetTarget(r.Context(), b.ID)
	if err != nil {
		s.logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	res, err := cap.Execute(r.Context(), common.CapabilityRequest{
		Metadata: common.RequestMetadata{},
		Inputs: &values.Map{
			Underlying: map[string]values.Value{
				"signed_report": values.NewString(""), // will fail, not a valid signed report
			},
		},
	})
	if err != nil {
		s.logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var out map[string]interface{}
	if err := res.Value.UnwrapTo(&out); err != nil {
		s.logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	desc, err := json.Marshal(out)
	if err != nil {
		s.logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(desc)
}
