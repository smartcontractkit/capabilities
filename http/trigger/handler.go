package trigger

import (
	"context"
	"fmt"
	"sync"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
)

const HandlerName = "HTTPTriggerHandler"

// TODO: move these to common package
const MethodHTTPTrigger = "http_trigger"

var _ core.GatewayConnectorHandler = &requestHandler{}

type requestHandler struct {
	services.StateMachine
	lggr             logger.Logger
	gatewayConnector core.GatewayConnector
	workflowsMu      sync.RWMutex
	workflows        map[string]workflow // workflowID -> workflow
	config           ServiceConfig
}

type workflow struct {
	authorizedKeys map[string]struct{}
	sendCh         chan<- capabilities.TriggerAndId[*http.Payload]
}

func NewRequestHandler(lggr logger.Logger, gc core.GatewayConnector, config ServiceConfig) *requestHandler {
	return &requestHandler{
		lggr:             logger.Named(lggr, HandlerName),
		gatewayConnector: gc,
		config:           config,
	}
}

func (h *requestHandler) Start(context.Context) error {
	h.lggr.Debug("Starting request handler")
	return h.StartOnce(HandlerName, func() error {
		return h.gatewayConnector.AddHandler([]string{MethodHTTPTrigger}, h)
	})
}

func (h *requestHandler) Close() error {
	return h.StopOnce(HandlerName, func() error {
		return nil
	})
}

func (h *requestHandler) ID() (string, error) {
	return HandlerName, nil
}

func (h *requestHandler) RegisterWorkflow(ctx context.Context, workflowID string, input *http.Config, sendCh chan<- capabilities.TriggerAndId[*http.Payload]) error {
	authorizedKeys := map[string]struct{}{}
	for _, key := range input.AuthorizedKeys {
		if key.GetEcdsa() != nil {
			authorizedKeys[key.GetEcdsa().PublicKey] = struct{}{}
		} else {
			return fmt.Errorf("unexpected key type: %T", key)
		}
	}

	h.workflowsMu.Lock()
	defer h.workflowsMu.Unlock()
	_, ok := h.workflows[workflowID]
	if ok {
		h.lggr.Debugw("Workflow already registered, re-registering", "workflowID", workflowID)
	}
	h.workflows[workflowID] = workflow{
		authorizedKeys: authorizedKeys,
		sendCh:         sendCh,
	}
	h.lggr.Debugf("Registered workflow %s", workflowID)
	return nil
}

func (h *requestHandler) UnregisterWorkflow(ctx context.Context, workflowID string) error {
	h.workflowsMu.Lock()
	defer h.workflowsMu.Unlock()
	workflow, ok := h.workflows[workflowID]
	if !ok {
		return fmt.Errorf("workflowID %s not registered", workflowID)
	}
	close(workflow.sendCh)
	delete(h.workflows, workflowID)
	h.lggr.Debugf("Unregistered workflow %s", workflowID)
	return nil
}

func (h *requestHandler) HandleGatewayMessage(ctx context.Context, gatewayID string, msg *gateway.Message) error {
	// TODO:
	// Validate msg method
	// Validate sender
	// Validate JWT signature
	// Rate-limit
	return nil
}
