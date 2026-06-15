package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/jonboulle/clockwork"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/triggers/cron"
	crontypedapi "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/cron"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/cron/server"
	"github.com/smartcontractkit/chainlink-common/pkg/custmsg"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/resourcemanager"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/services/orgresolver"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/events"
	meteringpb "github.com/smartcontractkit/chainlink-protos/metering/go"
)

const ServiceName = "CronCapabilities"

const defaultSendChannelBufferSize = 1000

var cronTriggerInfo = capabilities.MustNewCapabilityInfo(
	server.CronID,
	capabilities.CapabilityTypeTrigger,
	"A trigger that uses a cron schedule to run periodically at fixed times, dates, or intervals.",
)

const (
	// meteringService is the stable service constant for cron trigger
	// registrations on emitted MeterRecords and Snapshots. It must not encode
	// deployment environment or zone: those are discrete identity dimensions
	// sourced from deps at Initialise.
	meteringService = "cron-trigger"
	// meteringResource is the resource pool cron records apply to.
	meteringResource = "trigger_registrations"
	// meteringResourceType is the billing unit for cron registrations.
	meteringResourceType = "operations"
	// meteringProductFallback is used when the host has not injected a Product
	// (a legacy node or a boot path not yet updated to populate deps.Product).
	meteringProductFallback = "cre"
)

type Config struct {
	FastestScheduleIntervalSeconds int `json:"fastestScheduleIntervalSeconds"`
}

type Response struct {
	capabilities.TriggerEvent
	Payload cron.Payload
}

type cronTrigger struct {
	job        gocron.Job
	nextRun    time.Time
	workflowID string
	close      func()
}

type Service struct {
	services.Service
	srvcEng *services.Engine

	capabilities.CapabilityInfo
	limitsFactory           limits.Factory
	fastestScheduleInterval limits.TimeLimiter
	clock                   clockwork.Clock
	lggr                    logger.Logger
	scheduler               gocron.Scheduler
	triggers                *cronStore
	labeler                 custmsg.MessageEmitter
	metrics                 *Metrics
	meters                  *resourcemanager.ResourceManager
	// unregisterMeterable removes this Service from the ResourceManager's
	// snapshot registry; set at start, called at close. Nil until started.
	unregisterMeterable func()
	// base is the resourcemanager identity for cron registrations, built from
	// the host-injected deployment/node/DON dimensions at Initialise. ResourceID
	// is left empty here and set per trigger via base.WithResourceID(triggerID).
	base        resourcemanager.ResourceIdentity
	orgResolver orgresolver.OrgResolver
}

func (s *Service) RegisterLegacyTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *crontypedapi.Config) (<-chan capabilities.TriggerAndId[*crontypedapi.LegacyPayload], caperrors.Error) { //nolint:staticcheck
	ch, err := s.RegisterTrigger(ctx, triggerID, metadata, input)
	if err != nil {
		return nil, err
	}
	mapped := make(chan capabilities.TriggerAndId[*crontypedapi.LegacyPayload]) //nolint
	go func() {
		defer close(mapped)
		for {
			select {
			case <-ctx.Done():
				return
			case triggerEvent, ok := <-ch:
				if !ok {
					return
				}
				mapped <- capabilities.TriggerAndId[*crontypedapi.LegacyPayload]{ //nolint:staticcheck
					Id: triggerEvent.Id,
					Trigger: &crontypedapi.LegacyPayload{ //nolint:staticcheck
						ScheduledExecutionTime: triggerEvent.Trigger.ScheduledExecutionTime.AsTime().Format(time.RFC3339Nano),
					},
				}
			}
		}
	}()
	return mapped, nil
}

func (s *Service) UnregisterLegacyTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *crontypedapi.Config) caperrors.Error {
	return s.UnregisterTrigger(ctx, triggerID, metadata, input)
}

var (
	_ services.Service          = &Service{}
	_ resourcemanager.Meterable = &Service{}
)

// NewTriggerService creates a new trigger service.  Optionally, a clock can be passed in for testing, if nil
// the system clock will be used. The orgResolver is optional and can be nil, but should be set in live environments.
// meters reports trigger registrations for billing; if nil, a disabled no-op manager is used.
func NewTriggerService(parentLggr logger.Logger, clock clockwork.Clock, limitsFactory limits.Factory, meters *resourcemanager.ResourceManager) (*Service, error) {
	lggr := logger.Named(parentLggr, "CRONTrigger")

	if meters == nil {
		meters = resourcemanager.NewResourceManager(lggr, resourcemanager.ResourceManagerConfig{})
	}

	metrics, err := NewMetrics()
	if err != nil {
		return nil, fmt.Errorf("error creating metrics: %w", err)
	}

	var options []gocron.SchedulerOption
	options = append(options, gocron.WithMonitor(NewCronMonitor(metrics)))
	// Set scheduler location to UTC for consistency across nodes.
	options = append(options, gocron.WithLocation(time.UTC))
	// Adapt chainlink logger to gocron logger interface.
	options = append(options, gocron.WithLogger(NewCronLogger(lggr)))
	// Allow injecting a clock for testing. Otherwise use system clock.
	if clock != nil {
		options = append(options, gocron.WithClock(clock))
	} else {
		clock = clockwork.NewRealClock()
	}

	scheduler, err := gocron.NewScheduler(options...)
	if err != nil {
		return nil, fmt.Errorf("error creating scheduler: %w", err)
	}

	s := &Service{
		lggr:           lggr,
		CapabilityInfo: cronTriggerInfo,
		limitsFactory:  limitsFactory,
		triggers:       NewCronStore(),
		scheduler:      scheduler,
		clock:          clock,
		labeler: custmsg.NewLabeler().With(
			"capabilityID", cronTriggerInfo.ID,
			"capabilityVersion", cronTriggerInfo.Version(),
			"capabilityName", cronTriggerInfo.ID,
		),
		metrics: metrics,
		meters:  meters,
	}

	// Adopt services.Engine so the trigger can host the ResourceManager as a
	// sub-service (the RM owns the snapshot tick) and shut down cleanly. The
	// scheduler is started/stopped in s.start / s.close.
	s.Service, s.srvcEng = services.Config{
		Name:           "CronTrigger",
		NewSubServices: func(logger.Logger) []services.Service { return []services.Service{meters} },
		Start:          s.start,
		Close:          s.close,
	}.NewServiceEngine(lggr)

	return s, nil
}

// identityFor returns the per-trigger metering identity: the base identity
// with ResourceID set to triggerID. resource_id is workflow-scoped (the
// trigger_id) for cron, which has no shared physical resource. The DON ID
// falls back to the consumer workflow's DON when the host has not injected a
// capability DON ID (deps.CapabilityDonID == 0).
func (s *Service) identityFor(triggerID string, workflowDonID uint32) resourcemanager.ResourceIdentity {
	id := s.base.WithResourceID(triggerID)
	if id.DONID == "" {
		id.DONID = strconv.FormatUint(uint64(workflowDonID), 10)
	}
	return id
}

// emitMeterRecord reports a change to this trigger's registration reservation
// for billing. The triggerID doubles as the idempotency event identity: a
// triggerID is registered at most once at a time, so retried emissions for the
// same registration dedup downstream. Emission is fail-open and never affects
// the registration itself.
func (s *Service) emitMeterRecord(ctx context.Context, action meteringpb.MeterAction, metadata capabilities.RequestMetadata, triggerID string) {
	id := s.identityFor(triggerID, metadata.WorkflowDonID)
	s.meters.EmitMeterRecord(ctx, id, action,
		resourcemanager.NewUtilization(id, action, 1, triggerID))
}

func (s *Service) Initialise(ctx context.Context, dependencies core.StandardCapabilitiesDependencies) error {
	s.lggr.Debugw("Initialising cron trigger capability", "serviceName", ServiceName)

	var cronConfig Config
	if len(dependencies.Config) > 0 {
		err := json.Unmarshal([]byte(dependencies.Config), &cronConfig)
		if err != nil {
			return fmt.Errorf("failed to unmarshal config: %s %w", dependencies.Config, err)
		}
	}

	limit := cresettings.Default.PerWorkflow.CRONTrigger.FastestScheduleInterval // copy
	if cronConfig.FastestScheduleIntervalSeconds > 0 {
		limit.DefaultValue = time.Duration(cronConfig.FastestScheduleIntervalSeconds) * time.Second
	}
	limiter, err := s.limitsFactory.MakeTimeLimiter(limit)
	if err != nil {
		return fmt.Errorf("failed to create limiter: %w", err)
	}
	s.fastestScheduleInterval = limiter

	s.orgResolver = dependencies.OrgResolver
	if s.orgResolver == nil {
		s.lggr.Warn("OrgResolver is nil, cron capability will not be able to fetch organization ID")
	}

	// Build the base metering identity from the host-injected deployment, node,
	// and DON dimensions. These arrive via the standardized Initialise channel
	// (mirroring how capabilities#619 injects CapabilityDonID). Any may be
	// empty/zero until the host is updated to populate them; DONID falls back to
	// the consumer workflow DON at emit time (see identityFor).
	product := dependencies.Product
	if product == "" {
		product = meteringProductFallback
	}
	var donID string
	if dependencies.CapabilityDonID != 0 {
		donID = strconv.FormatUint(uint64(dependencies.CapabilityDonID), 10)
	}
	s.base = resourcemanager.ResourceIdentity{
		Product:      product,
		Environment:  dependencies.Environment,
		Zone:         dependencies.Zone,
		DONID:        donID,
		NodeID:       dependencies.NodeID,
		Service:      meteringService,
		Resource:     meteringResource,
		ResourceType: meteringResourceType,
	}

	err = s.Start(ctx)
	if err != nil {
		return fmt.Errorf("error when starting trigger service: %w", err)
	}

	return nil
}

func (s *Service) RegisterTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *crontypedapi.Config) (<-chan capabilities.TriggerAndId[*crontypedapi.Payload], caperrors.Error) {
	ctx = metadata.ContextWithCRE(ctx)
	var muCh sync.RWMutex // extra synchronization to prevent the cron task from racing to send on the closed chan and re-register itself
	// hold the lock until we call triggers.Write
	muCh.Lock()
	defer muCh.Unlock()

	_, ok := s.triggers.Read(triggerID)
	if ok {
		return nil, caperrors.NewPublicSystemError(fmt.Errorf("triggerId %s already registered", triggerID), caperrors.Internal)
	}

	var job gocron.Job
	callbackCh := make(chan capabilities.TriggerAndId[*crontypedapi.Payload], defaultSendChannelBufferSize)

	closeCh := func() {
		muCh.Lock()
		defer muCh.Unlock()
		close(callbackCh)
		callbackCh = nil
	}

	allowSeconds := true
	jobDef := gocron.CronJob(input.Schedule, allowSeconds)

	limit, err := s.fastestScheduleInterval.Limit(ctx)
	if err != nil {
		return nil, caperrors.NewPublicSystemError(fmt.Errorf("failed to look up fastest schedule interval: %w", err), caperrors.Internal)
	}
	capErr := enforceFastestSchedule(s.lggr, jobDef, limit)
	if capErr != nil {
		return nil, capErr
	}

	triggerIndex, err := workflows.GetTriggerIndexFromReferenceID(metadata.ReferenceID)
	if err != nil {
		s.lggr.Errorw("failed to get trigger index from reference ID", "err", err, "triggerID", triggerID, "workflowID", metadata.WorkflowID, "refID", metadata.ReferenceID)
		// continue with execution even if we can't get trigger index
		triggerIndex = 0
	}

	task := gocron.NewTask(
		// Task callback, executed at next run time
		func() {
			defer func() {
				if r := recover(); r != nil {
					s.lggr.Errorw("panic in gocron.NewTask function", "err", r, "stack", string(debug.Stack()))
				}
			}()
			trigger, ok := s.triggers.Read(triggerID)
			if !ok {
				// Invariant: The trigger should always exist, as unregistering the trigger removes the job
				s.lggr.Errorw("task callback invariant: trigger no longer exists", "triggerID", triggerID)
				return
			}

			s.metrics.RecordTriggerExecutionTime(ctx)
			scheduledExecutionTimeUTC := trigger.nextRun.UTC()
			currentTimeUTC := s.clock.Now().UTC()

			response := createTriggerResponse(scheduledExecutionTimeUTC)

			displayWorkflowName := metadata.DecodedWorkflowName
			if displayWorkflowName == "" {
				displayWorkflowName = metadata.WorkflowName
			}

			workflowExecutionID, execIDErr := workflows.GenerateExecutionIDWithTriggerIndex(trigger.workflowID, response.Id, triggerIndex)

			if execIDErr != nil {
				s.lggr.Errorw("failed to generate execution ID", "err", execIDErr, "triggerID", triggerID, "workflowID", trigger.workflowID, "triggerEventID", response.Id)
				// Continue with execution even if we can't generate ID or emit event
			} else {
				// Try to fetch organization ID if org resolver is available
				var orgID string
				if s.orgResolver != nil && metadata.WorkflowOwner != "" {
					func() {
						defer func() {
							if r := recover(); r != nil {
								s.lggr.Warnw("Panic while fetching organization ID from org resolver", "workflowOwner", metadata.WorkflowOwner, "panic", r)
							}
						}()
						if fetchedOrgID, orgErr := s.orgResolver.Get(ctx, metadata.WorkflowOwner); orgErr != nil {
							s.lggr.Warnw("Failed to fetch organization ID from org resolver", "workflowOwner", metadata.WorkflowOwner, "error", orgErr)
						} else if fetchedOrgID != "" {
							orgID = fetchedOrgID
							s.lggr.Debugw("Successfully fetched organization ID", "workflowOwner", metadata.WorkflowOwner, "orgID", orgID)
						}
					}()
				}

				// Emit TriggerExecutionStarted event
				labeler := custmsg.NewLabeler().With(
					events.KeyTriggerID, response.Id,
					events.KeyWorkflowID, trigger.workflowID,
					events.KeyWorkflowExecutionID, workflowExecutionID,
					events.KeyWorkflowOwner, metadata.WorkflowOwner,
					events.KeyWorkflowName, displayWorkflowName,
					events.KeyDonID, strconv.Itoa(int(metadata.WorkflowDonID)),
					events.KeyDonVersion, strconv.Itoa(int(metadata.WorkflowDonConfigVersion)),
					events.KeyOrganizationID, orgID,
					events.KeyWorkflowRegistryChainSelector, metadata.WorkflowRegistryChainSelector,
					events.KeyWorkflowRegistryAddress, metadata.WorkflowRegistryAddress,
					events.KeyEngineVersion, metadata.EngineVersion,
				)
				if emitErr := events.EmitTriggerExecutionStarted(ctx, labeler); emitErr != nil {
					s.lggr.Errorw("failed to emit trigger execution started event", "err", emitErr, "triggerID", triggerID, "workflowExecutionID", workflowExecutionID)
					// Continue with execution even if event emission fails
				}
			}

			s.lggr.Debugw("task callback sending trigger response", "executionID", workflowExecutionID, "isLegacyExecutionID", false, "triggerID", triggerID, "scheduledExecTimeUTC", scheduledExecutionTimeUTC.Format(time.RFC3339Nano), "actualExecTimeUTC", currentTimeUTC.Format(time.RFC3339Nano))

			nextExecutionTime, nextRunErr := job.NextRun()
			if nextRunErr != nil {
				// .NextRun() will error if the job no longer exists
				// or if there is no next run to schedule, which shouldn't happen with cron jobs
				s.lggr.Errorw("task callback failed to schedule next run", "executionID", workflowExecutionID, "triggerID", triggerID)
			}

			muCh.RLock()
			defer muCh.RUnlock()
			if callbackCh == nil {
				return // unregistered already
			}
			s.triggers.Write(triggerID, cronTrigger{
				job:        job,
				nextRun:    nextExecutionTime,
				workflowID: metadata.WorkflowID,
				close:      closeCh,
			})

			select {
			case callbackCh <- response:
			default:
				s.lggr.Errorw("callback channel full, dropping event", "executionID", workflowExecutionID, "triggerID", triggerID, "eventID", response.Id)

				lblErr := s.labeler.With(
					"workflowOwner", metadata.WorkflowOwner,
					"workflowName", displayWorkflowName,
					"workflowID", metadata.WorkflowID,
				).Emit(ctx, "callback channel full, dropping event")
				if lblErr != nil {
					s.lggr.Errorw("cannot emit custom event", "executionID", workflowExecutionID, "triggerID", triggerID, "eventID", response.Id, "err", lblErr)
				}
			}
		})

	if s.scheduler == nil {
		return nil, caperrors.NewPublicSystemError(errors.New("cannot register a new trigger, service has been closed"), caperrors.Internal)
	}

	// If service has already started, job will be scheduled immediately
	job, err = s.scheduler.NewJob(jobDef, task, gocron.WithName(triggerID))
	if err != nil {
		s.lggr.Errorw("failed to create new job", "err", err)
		return nil, caperrors.NewPublicSystemError(fmt.Errorf("RegisterTrigger failed to create new job: %s", err), caperrors.Internal)
	}

	firstRunTime, err := job.NextRun()
	if err != nil {
		// errors if job no longer exists on scheduler
		s.lggr.Errorw("failed to get next run time", "err", err)
		// ensure that it is out of scheduler
		err := s.scheduler.RemoveJob(job.ID())
		return nil, caperrors.NewPublicSystemError(fmt.Errorf("RegisterTrigger failed to remove job: %s", err), caperrors.Internal)
	}

	s.triggers.Write(triggerID, cronTrigger{
		job:        job,
		nextRun:    firstRunTime,
		workflowID: metadata.WorkflowID,
		close:      closeCh,
	})

	s.emitMeterRecord(ctx, meteringpb.MeterAction_METER_ACTION_RESERVE, metadata, triggerID)

	s.lggr.Debugw("Trigger registered", "workflowId", metadata.WorkflowID, "triggerId", triggerID, "jobId", job.ID())
	s.metrics.IncActiveTriggersGauge(ctx)
	return callbackCh, nil
}

func createTriggerResponse(scheduledExecutionTime time.Time) capabilities.TriggerAndId[*crontypedapi.Payload] {
	// Ensure UTC time is used for consistency across nodes.
	scheduledExecutionTimeUTC := scheduledExecutionTime.UTC()

	// Use the scheduled execution time as a deterministic identifier.
	// Since cron schedules only go to second granularity this should never have ms.
	// Just in case, truncate on seconds by formatting to ensure consistency across nodes.
	scheduledExecutionTimeFormatted := scheduledExecutionTimeUTC.Format(time.RFC3339)
	triggerEventID := scheduledExecutionTimeFormatted

	return capabilities.TriggerAndId[*crontypedapi.Payload]{
		Trigger: &crontypedapi.Payload{
			ScheduledExecutionTime: timestamppb.New(scheduledExecutionTimeUTC),
		},
		Id: triggerEventID,
	}
}

func (s *Service) AckEvent(ctx context.Context, triggerID string, eventID string, method string) caperrors.Error {
	return nil
}

func (s *Service) UnregisterTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *crontypedapi.Config) caperrors.Error {
	trigger, ok := s.triggers.Read(triggerID)
	if !ok {
		s.lggr.Warnw("trigger not found", "triggerID", triggerID)
		return nil
	}

	jobID := trigger.job.ID()

	// Remove job from scheduler
	if s.scheduler == nil {
		return caperrors.NewPublicSystemError(errors.New("cannot unregister a new trigger, service has been closed"), caperrors.Internal)
	}
	err := s.scheduler.RemoveJob(jobID)
	if err != nil {
		return caperrors.NewPublicSystemError(fmt.Errorf("UnregisterTrigger failed to remove job from scheduler: %s", err), caperrors.Internal)
	}

	// Close callback channel
	trigger.close()

	// Remove from triggers context
	s.triggers.Delete(triggerID)

	s.emitMeterRecord(ctx, meteringpb.MeterAction_METER_ACTION_RELEASE, metadata, triggerID)

	s.lggr.Debugw("UnregisterTrigger", "triggerId", triggerID, "jobId", jobID)
	s.metrics.DecActiveTriggersGauge(ctx)
	return nil
}

// start is the services.Engine start hook. The ResourceManager sub-service has
// already been started by the engine, so start registers this Service as a
// Meterable (the RM polls it once per snapshot tick) and starts the scheduler,
// refreshing next-run times for any registrations that survived a restart.
func (s *Service) start(_ context.Context) error {
	if s.scheduler == nil {
		return errors.New("service has shutdown, it must be built again to restart")
	}

	// Register for snapshots. The RM owns the tick; we only supply state via
	// the Meterable interface. unregisterMeterable is called in close.
	s.unregisterMeterable = s.meters.Register(s)

	s.scheduler.Start()

	for triggerID, trigger := range s.triggers.ReadAll() {
		nextExecutionTime, err := trigger.job.NextRun()
		s.triggers.Write(triggerID, cronTrigger{
			job:        trigger.job,
			nextRun:    nextExecutionTime,
			workflowID: trigger.workflowID,
			close:      trigger.close,
		})
		if err != nil {
			s.lggr.Errorw("Unable to get next run time", "err", err, "triggerID", triggerID)
		}
	}

	return nil
}

// close is the services.Engine close hook. After this the Service cannot be
// started again; it must be re-built to schedule again. close drains a RELEASE
// for every still-active registration (so a graceful shutdown does not leak
// reservations in billing), unregisters from the snapshot registry, then shuts
// the scheduler down. The ResourceManager sub-service is closed by the engine
// afterwards.
func (s *Service) close() error {
	if s.scheduler == nil {
		return errors.New("service has shutdown, it must be built again to restart")
	}

	// Graceful-close RELEASEs. Use a background context: the engine's start
	// context is already cancelled by the time close runs. Emission is
	// fail-open, so a metering failure never blocks shutdown.
	ctx := context.Background()
	for triggerID := range s.triggers.ReadAll() {
		id := s.base.WithResourceID(triggerID)
		s.meters.EmitMeterRecord(ctx, id, meteringpb.MeterAction_METER_ACTION_RELEASE,
			resourcemanager.NewUtilization(id, meteringpb.MeterAction_METER_ACTION_RELEASE, 1, triggerID))
	}

	if s.unregisterMeterable != nil {
		s.unregisterMeterable()
		s.unregisterMeterable = nil
	}

	err := s.scheduler.Shutdown()
	if err != nil {
		return fmt.Errorf("scheduler shutdown encountered a problem: %s", err)
	}

	// After .Shutdown() the scheduler cannot be started again,
	// but calling .Start() on it will not error. Set to nil to mark closed.
	s.scheduler = nil

	return nil
}

func (s *Service) Description() string {
	return "Cron Trigger Capability"
}

// ResourceIdentity implements resourcemanager.Meterable: it returns the base
// six-dimension identity (resource_id left empty; set per active trigger in
// GetUtilization).
func (s *Service) ResourceIdentity() resourcemanager.ResourceIdentity {
	return s.base
}

// GetUtilization implements resourcemanager.Meterable: it returns the absolute
// state of every currently active cron registration, one SnapshotEntry per
// trigger, each at value 1 (a registration is a single reserved unit). It is a
// cheap in-memory read of the store snapshot and tolerates ctx cancellation.
func (s *Service) GetUtilization(ctx context.Context) []resourcemanager.SnapshotEntry {
	if ctx.Err() != nil {
		return nil
	}
	triggers := s.triggers.ReadAll()
	entries := make([]resourcemanager.SnapshotEntry, 0, len(triggers))
	for triggerID := range triggers {
		entries = append(entries, resourcemanager.SnapshotEntry{
			Identity: s.base.WithResourceID(triggerID),
			Value:    1,
		})
	}
	return entries
}
