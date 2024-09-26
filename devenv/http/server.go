package main

import (
	"encoding/json"
	"net/http"

	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities"
	"github.com/smartcontractkit/chainlink/v2/core/logger"
)

type server struct {
	capabilities       loop.StandardCapabilities
	capabilityRegistry *capabilities.Registry
	logger             logger.Logger
	closeLogger        func() error
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

func (s *server) TargetExecute(w http.ResponseWriter, r *http.Request) {
	var b body
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		s.logger.Error(err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	req, err := b.toCapabilityRequest()
	if err != nil {
		s.logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	cap, err := s.capabilityRegistry.GetTarget(r.Context(), b.ID)
	if err != nil {
		s.logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	res, err := cap.Execute(r.Context(), req)
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
