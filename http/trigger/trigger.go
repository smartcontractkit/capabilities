package trigger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/smartcontractkit/capabilities/http/protos"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/services/orgresolver"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
)

const ServiceName = "HTTPTriggerCapability"

var _ protos.HTTPCapability = &service{}

type WorkflowRegistrationInput struct {
	WorkflowSelector gateway.WorkflowSelector
	Config           *protos.Config
	Metadata         WorkflowRegistrationMetadata
}

type WorkflowRegistrationMetadata struct {
	WorkflowRegistryChainSelector string
	WorkflowRegistryAddress       string
	EngineVersion                 string
	WorkflowDONID                 uint32
	ReferenceID                   string
	// DecodedWorkflowName is the human-readable workflow name
	DecodedWorkflowName string
}

type ConnectorHandler interface {
	services.Service
	RegisterWorkflow(ctx context.Context, input WorkflowRegistrationInput, sendCh chan<- capabilities.TriggerAndId[*protos.Payload]) error
	UnregisterWorkflow(ctx context.Context, workflowID string) error
}

type service struct {
	services.StateMachine
	lggr             logger.SugaredLogger
	cfg              ServiceConfig
	connectorHandler ConnectorHandler
	metrics          *Metrics
	limitsFactory    limits.Factory
	orgResolver      orgresolver.OrgResolver
}

// Dependencies are what this capability cannot make for itself: the gateway it
// talks to, the store its request cache survives a restart in, and what the
// process it runs in knows about the DON it belongs to.
type Dependencies struct {
	// Connector is the gateway connection. Trigger requests arrive over it and
	// responses go back over it, and it is what signs on this node's behalf.
	Connector core.GatewayConnector

	// Store is where a request that has been answered is remembered, so a retry of
	// the same request is answered again rather than run again.
	Store core.KeyValueStore

	// CapabilityDonID is the on-chain DON this process serves, used to label the
	// events it emits with the sending DON. Zero falls back to the workflow's own DON,
	// which is what a host that could not resolve one leaves behind.
	CapabilityDonID uint32

	// OrgResolver says which organisation a workflow belongs to, for the limits that
	// are per-organisation. Nil means those limits cannot be resolved, which is a
	// warning rather than a refusal to start.
	OrgResolver orgresolver.OrgResolver

	// LimitsFactory is where this capability's own limits come from.
	LimitsFactory limits.Factory
}

// NewService returns the HTTP trigger capability, configured and wired to the
// gateway, ready to be started.
//
// Everything it needs is an argument: there is no Initialise step, because the
// process hosting this is the binary it lives in rather than a node handing it
// dependencies after the fact.
func NewService(lggr logger.Logger, cfg ServiceConfig, deps Dependencies) (*service, error) {
	if deps.Connector == nil {
		return nil, errors.New("the HTTP trigger capability needs a gateway connector: trigger requests reach it over one")
	}
	// Required, not defaulted to something in memory: this is where an answered
	// request is remembered, and a cache that a restart empties is one that runs a
	// customer's workflow twice.
	if deps.Store == nil {
		return nil, errors.New("the HTTP trigger capability needs a store: it is where an answered request is remembered, so that a retry is answered rather than run again")
	}

	sugared := logger.Sugared(logger.Named(lggr, ServiceName))
	if deps.OrgResolver == nil {
		sugared.Warn("OrgResolver is nil, HTTP trigger capability will not be able to fetch organization ID")
	}

	s := &service{
		lggr:          sugared,
		cfg:           applyDefaults(cfg),
		limitsFactory: deps.LimitsFactory,
		orgResolver:   deps.OrgResolver,
	}

	metrics, err := NewMetrics()
	if err != nil {
		return nil, err
	}
	s.metrics = metrics

	workflowStore := newWorkflowStore(s.lggr)
	metadataPublisher := NewGatewayMetadataPublisher(s.lggr, deps.Connector, workflowStore, s.cfg, s.metrics)
	requestCache := newRequestCache(s.lggr, deps.Store, time.Duration(s.cfg.RequestCacheTTL)*time.Second)

	s.connectorHandler, err = NewConnectorHandler(s.lggr, deps.Connector, s.cfg, deps.CapabilityDonID, workflowStore,
		metadataPublisher, requestCache, s.metrics, s.orgResolver, s.limitsFactory)
	if err != nil {
		return nil, err
	}

	return s, nil
}

func (s *service) Start(ctx context.Context) error {
	s.lggr.Debug("Service starting...")
	return s.StartOnce(ServiceName, func() error {
		return s.connectorHandler.Start(ctx)
	})
}

func (s *service) Close() error {
	s.lggr.Debug("Service closing...")
	return s.StopOnce(ServiceName, func() error {
		return s.connectorHandler.Close()
	})
}

func (s *service) HealthReport() map[string]error {
	return map[string]error{s.Name(): s.Healthy()}
}

func (s *service) Ready() error {
	return s.StateMachine.Healthy()
}

func (s *service) Name() string {
	return s.lggr.Name()
}

func (s *service) Description() string {
	return "HTTP Trigger Service"
}

func (s *service) RegisterTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *protos.Config) (<-chan capabilities.TriggerAndId[*protos.Payload], caperrors.Error) {
	s.lggr.Infow("RegisterTrigger called",
		"triggerID", triggerID,
		"workflowID", metadata.WorkflowID,
		"workflowOwner", metadata.WorkflowOwner,
		"workflowName", metadata.WorkflowName,
		"workflowTag", metadata.WorkflowTag)
	sendCh := make(chan capabilities.TriggerAndId[*protos.Payload], s.cfg.SendChannelBufferSize)
	// TODO: remove this when testing frameworks (local CRE, capabilities integration tests framework) migrate to WR v2
	if metadata.WorkflowTag == "" {
		metadata.WorkflowTag = "TEMP_TAG"
	}
	workflowSelector := gateway.WorkflowSelector{
		WorkflowID:    strings.ToLower(ensureHexPrefix(metadata.WorkflowID)),
		WorkflowOwner: strings.ToLower(ensureHexPrefix(metadata.WorkflowOwner)),
		WorkflowName:  strings.ToLower(ensureHexPrefix(metadata.WorkflowName)),
		WorkflowTag:   metadata.WorkflowTag,
	}

	registrationInput := WorkflowRegistrationInput{
		WorkflowSelector: workflowSelector,
		Config:           input,
		Metadata: WorkflowRegistrationMetadata{
			WorkflowRegistryChainSelector: metadata.WorkflowRegistryChainSelector,
			WorkflowRegistryAddress:       metadata.WorkflowRegistryAddress,
			EngineVersion:                 metadata.EngineVersion,
			WorkflowDONID:                 metadata.WorkflowDonID,
			ReferenceID:                   metadata.ReferenceID,
			DecodedWorkflowName:           metadata.DecodedWorkflowName,
		},
	}

	err := s.connectorHandler.RegisterWorkflow(ctx, registrationInput, sendCh)
	if err != nil {
		s.metrics.IncrementRegisterFailureCount(ctx, s.lggr)
		return nil, caperrors.NewPublicSystemError(
			fmt.Errorf("failed to register workflowID %s (Owner: %s, Name: %s, Tag: %s): %w", metadata.WorkflowID, metadata.WorkflowOwner, metadata.WorkflowName, metadata.WorkflowTag, err),
			caperrors.Internal)
	}
	s.metrics.IncrementRegisterCount(ctx, s.lggr)
	return sendCh, nil
}

func (s *service) UnregisterTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *protos.Config) caperrors.Error {
	s.lggr.Infow("UnregisterTrigger called",
		"triggerID", triggerID,
		"workflowID", metadata.WorkflowID,
		"workflowOwner", metadata.WorkflowOwner,
		"workflowName", metadata.WorkflowName,
		"workflowTag", metadata.WorkflowTag)
	err := s.connectorHandler.UnregisterWorkflow(ctx, ensureHexPrefix(metadata.WorkflowID))
	if err != nil {
		s.lggr.Errorf("Failed to unregister workflow %s: %v", metadata.WorkflowID, err)
		s.metrics.IncrementDeregisterFailureCount(ctx, s.lggr)
		return caperrors.NewPublicSystemError(
			fmt.Errorf("failed to unregister workflowID %s (Owner: %s, Name: %s, Tag: %s): %w", metadata.WorkflowID, metadata.WorkflowOwner, metadata.WorkflowName, metadata.WorkflowTag, err),
			caperrors.Internal)
	}
	s.metrics.IncrementDeregisterCount(ctx, s.lggr)
	return nil
}

func (s *service) AckEvent(ctx context.Context, triggerID string, eventID string, method string) caperrors.Error {
	return nil
}

func ensureHexPrefix(s string) string {
	if len(s) >= 2 && s[:2] == "0x" {
		return s
	}
	return "0x" + s
}
