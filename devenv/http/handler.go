package main

import (
	"encoding/json"
	"net/http"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities"
)

type Handler interface {
	// Infos returns the capability info for all registered capabilities
	Infos(w http.ResponseWriter, r *http.Request)

	// GetCapabilitiyInfo returns the capability info for a given capability ID
	GetCapabilityInfo(w http.ResponseWriter, r *http.Request)

	// TargetExecute calls the Execute method on the target capability for a given capability ID
	TargetExecute(w http.ResponseWriter, r *http.Request)
}

type handler struct {
	svc                loop.StandardCapabilities
	capabilityRegistry *capabilities.Registry
	kvstore            core.KeyValueStore
	logger             logger.Logger
}

func newHandler(
	svc loop.StandardCapabilities,
	capabilityRegistry *capabilities.Registry,
	kvstore core.KeyValueStore,
	logger logger.Logger,
) *handler {
	return &handler{
		svc:                svc,
		capabilityRegistry: capabilityRegistry,
		kvstore:            kvstore,
		logger:             logger,
	}
}

func (h *handler) Infos(w http.ResponseWriter, r *http.Request) {
	infos, err := h.svc.Infos(r.Context())
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

func (h *handler) TargetExecute(w http.ResponseWriter, r *http.Request) {
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

	cap, err := h.capabilityRegistry.GetTarget(r.Context(), b.ID)
	if err != nil {
		h.logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	res, err := cap.Execute(r.Context(), req)
	if err != nil {
		h.logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var out map[string]interface{}
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
