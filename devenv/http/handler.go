package main

import (
	"context"
	"encoding/json"
	"net/http"

	common "github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities"
	"github.com/smartcontractkit/chainlink/v2/plugins"
)

type Handler interface {
	// Infos returns the capability info for all registered capabilities
	Infos(w http.ResponseWriter, r *http.Request)

	// GetCapabilitiyInfo returns the capability info for a given capability ID
	GetCapabilityInfo(w http.ResponseWriter, r *http.Request)

	// Execute calls the Execute method on a capability for a given capability ID.  Fails if the
	// capability is not executable.
	Execute(w http.ResponseWriter, r *http.Request)
}

// handler is a wrapper for a Standard Capabilities Service
type handler struct {
	svc                *loop.StandardCapabilitiesService
	registrarConfig    plugins.RegistrarConfig
	capabilityRegistry *capabilities.Registry
	kvstore            core.KeyValueStore
	logger             logger.Logger
	id                 string
}

func newHandler(
	logger logger.Logger,
	rc plugins.RegistrarConfig,
	cmd, loopID string,
) (*handler, error) {
	cmdFn, opts, err := rc.RegisterLOOP(plugins.CmdConfig{
		ID:  loopID,
		Cmd: cmd,
	})
	if err != nil {
		return nil, err
	}

	var (
		capabilityRegistry = capabilities.NewRegistry(logger)
		kvstore            = NewStore(logger)
		svc                = loop.NewStandardCapabilitiesService(logger, opts, cmdFn)
	)

	return &handler{
		svc:                svc,
		capabilityRegistry: capabilityRegistry,
		registrarConfig:    rc,
		kvstore:            kvstore,
		logger:             logger,
		id:                 loopID,
	}, nil
}

func (h *handler) Start(ctx context.Context) error {
	h.logger.Info("starting handler")
	if err := h.svc.Start(ctx); err != nil {
		h.logger.Errorf("Failed to start service: %v", err)
		return err
	}

	if err := h.svc.WaitCtx(ctx); err != nil {
		h.logger.Errorf("Failed to wait for service: %v", err)
		return err
	}

	h.logger.Info("initialising capabilities service")
	if err := h.svc.Service.Initialise(ctx, core.StandardCapabilitiesDependencies{
		Config:             "",
		Store:              h.kvstore,
		CapabilityRegistry: h.capabilityRegistry,
	}); err != nil {
		h.logger.Errorf("Failed to initialise service: %v", err)
		return err
	}
	return nil
}

func (h *handler) Close() error {
	h.logger.Debugf("unregistering loop %s", h.id)
	h.registrarConfig.UnregisterLOOP(h.id)

	// TODO(mstreet3): This should be a graceful shutdown, but plugin is not shutdown gracefully
	// Plugin process exits without error, but logs an error log.
	if err := h.svc.Close(); err != nil {
		h.logger.Errorf("Failed to close service: %v", err)
		return err
	}
	return nil
}

func (h *handler) Infos(w http.ResponseWriter, r *http.Request) {
	infos, err := h.svc.Service.Infos(r.Context())
	if err != nil {
		h.logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.logger.Info("handling infos request")

	for _, info := range infos {
		desc, err := json.Marshal(capabilityInfo{
			ID:             info.ID,
			CapabilityType: string(info.CapabilityType),
			Description:    info.Description,
		})
		if err != nil {
			h.logger.Error(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write([]byte(desc))
	}
}

func (h *handler) GetCapabilityInfo(w http.ResponseWriter, r *http.Request) {
	var b body
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		h.logger.Error(err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cap, err := h.capabilityRegistry.Get(r.Context(), b.ID)
	if err != nil {
		h.logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	info, err := cap.Info(r.Context())
	if err != nil {
		h.logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	desc, err := json.Marshal(capabilityInfo{
		ID:             info.ID,
		CapabilityType: string(info.CapabilityType),
		Description:    info.Description,
	})
	if err != nil {
		h.logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(desc)
}

func (h *handler) Execute(w http.ResponseWriter, r *http.Request) {
	var b body
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		h.logger.Error(err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	req, err := b.toCapabilityRequest()
	if err != nil {
		h.logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	cap, err := h.capabilityRegistry.Get(r.Context(), b.ID)
	if err != nil {
		h.logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	executable, ok := cap.(common.Executable)
	if !ok {
		h.logger.Error("capability is not executable")
		http.Error(w, "capability is not executable", http.StatusBadRequest)
		return
	}

	res, err := executable.Execute(r.Context(), req)
	if err != nil {
		h.logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var out map[string]any
	if err := res.Value.UnwrapTo(&out); err != nil {
		h.logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	desc, err := json.Marshal(out)
	if err != nil {
		h.logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(desc)
}
