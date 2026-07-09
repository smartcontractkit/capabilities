package trigger

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/resourcemanager"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/services/orgresolver"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
)

const ServiceName = "HTTPTriggerCapability"

// Metering identity constants for the HTTP trigger. Service is the stable
// service constant (it must not encode environment or zone); resource pool and
// utilization resource_type identify the HTTP workflow-registration pool and
// its billing
// unit.
const (
	meterService      = "http-trigger"
	meterResource     = "http_workflows"
	meterResourceType = "operations"
	// meterProductFallback is used when the host did not inject a Product
	// dimension (legacy node or a boot path not yet updated).
	meterProductFallback = "cre"
)

var _ server.HTTPCapability = &service{}

type WorkflowRegistrationInput struct {
	WorkflowSelector gateway.WorkflowSelector
	Config           *http.Config
	Metadata         WorkflowRegistrationMetadata
}

type WorkflowRegistrationMetadata struct {
	WorkflowRegistryChainSelector string
	WorkflowRegistryAddress       string
	EngineVersion                 string
	WorkflowDONID                 uint32
	ReferenceID                   string
	OrganizationID                string
	// DecodedWorkflowName is the human-readable workflow name
	DecodedWorkflowName string
}

type ConnectorHandler interface {
	services.Service
	RegisterWorkflow(ctx context.Context, input WorkflowRegistrationInput, sendCh chan<- capabilities.TriggerAndId[*http.Payload]) error
	UnregisterWorkflow(ctx context.Context, workflowID string) error
}

// MeteringConfig carries emission toggles and deployment/node identity
// dimensions for the HTTP trigger's ResourceManager.
type MeteringConfig struct {
	MeterRecordsEnabled   bool
	MeterSnapshotsEnabled bool
	Deployment            resourcemanager.DeploymentIdentity
}

func (m MeteringConfig) resourceManagerConfig() resourcemanager.ResourceManagerConfig {
	return resourcemanager.ResourceManagerConfig{
		MeterRecordsEnabled:   m.MeterRecordsEnabled,
		MeterSnapshotsEnabled: m.MeterSnapshotsEnabled,
		Emitter:               beholder.GetEmitter(),
		SnapshotInterval:      resourcemanager.DefaultSnapshotInterval,
	}
}

type service struct {
	services.StateMachine
	lggr             logger.SugaredLogger
	cfg              ServiceConfig
	connectorHandler ConnectorHandler
	metrics          *Metrics
	limitsFactory    limits.Factory
	orgResolver      orgresolver.OrgResolver
	// metering carries static deployment/node identity dimensions plus metering
	// emission toggles delivered via loop.EnvConfig.
	metering MeteringConfig
}

func NewService(lggr logger.Logger, limitsFactory limits.Factory, metering MeteringConfig) *service {
	return &service{
		lggr:          logger.Sugared(logger.Named(lggr, ServiceName)),
		limitsFactory: limitsFactory,
		metering:      metering,
	}
}

func (s *service) Initialise(ctx context.Context, dependencies core.StandardCapabilitiesDependencies) error {
	s.lggr.Debugw("Initialising http trigger capability", "config", dependencies.Config)

	var serviceConfig ServiceConfig
	if dependencies.Config != "" {
		err := json.Unmarshal([]byte(dependencies.Config), &serviceConfig)
		if err != nil {
			return err
		}
	}
	s.cfg = applyDefaults(serviceConfig)
	s.orgResolver = dependencies.OrgResolver
	if s.orgResolver == nil {
		s.lggr.Warn("OrgResolver is nil, HTTP trigger capability will not be able to fetch organization ID")
	}
	workflowStore := newWorkflowStore(s.lggr)
	var err error
	s.metrics, err = NewMetrics()
	if err != nil {
		return err
	}
	metadataPublisher := NewGatewayMetadataPublisher(s.lggr, dependencies.GatewayConnector, workflowStore, s.cfg, s.metrics)
	requestCache := newRequestCache(s.lggr, dependencies.Store, time.Duration(s.cfg.RequestCacheTTL)*time.Second)
	resourceManager := resourcemanager.NewResourceManager(s.lggr, s.metering.resourceManagerConfig())
	baseIdentity := baseMeterIdentity(dependencies, s.metering.Deployment)
	s.connectorHandler, err = NewConnectorHandler(s.lggr, dependencies.GatewayConnector, s.cfg, workflowStore, metadataPublisher, requestCache, s.metrics, s.orgResolver, resourceManager, baseIdentity)
	if err != nil {
		return err
	}
	return s.Start(ctx)
}

// baseMeterIdentity builds the HTTP trigger's base metering identity. The
// deployment/node dimensions come from deployment (delivered via
// loop.EnvConfig); the DON dimension comes from the host-injected
// CapabilityDonID. The service-level resource_pool is fixed here; the
// per-workflow billing fields are set on each Utilization.
//
// The DON identifier is the capability DON the trigger LOOP was spawned for
// (deps.CapabilityDonID, host-injected via capabilities#619). When the host has
// not populated it (0), the DON identifier is left empty here and resolved per registration
// from the workflow DON at emit time (see connectorHandler.donID). Product
// falls back to a constant when the host did not inject one.
func baseMeterIdentity(deps core.StandardCapabilitiesDependencies, deployment resourcemanager.DeploymentIdentity) resourcemanager.ResourceIdentity {
	product := deployment.Product
	if product == "" {
		product = meterProductFallback
	}
	var donID string
	if deps.CapabilityDonID != 0 {
		donID = strconv.FormatUint(uint64(deps.CapabilityDonID), 10)
	}
	var donIdentity *resourcemanager.DonIdentity
	if donID != "" || deployment.NodeID != "" {
		donIdentity = &resourcemanager.DonIdentity{
			DonID:  donID,
			NodeID: deployment.NodeID,
		}
	}
	return resourcemanager.ResourceIdentity{
		Product:         product,
		Tenant:          deployment.Tenant,
		NumericTenantID: deployment.NumericTenantID,
		Environment:     deployment.Environment,
		Zone:            deployment.Zone,
		Don:             donIdentity,
		Service:         meterService,
		ResourcePool:    meterResource,
	}
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

func (s *service) RegisterTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *http.Config) (<-chan capabilities.TriggerAndId[*http.Payload], caperrors.Error) {
	s.lggr.Infow("RegisterTrigger called",
		"triggerID", triggerID,
		"workflowID", metadata.WorkflowID,
		"workflowOwner", metadata.WorkflowOwner,
		"workflowName", metadata.WorkflowName,
		"workflowTag", metadata.WorkflowTag)
	sendCh := make(chan capabilities.TriggerAndId[*http.Payload], s.cfg.SendChannelBufferSize)
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

func (s *service) UnregisterTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *http.Config) caperrors.Error {
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
