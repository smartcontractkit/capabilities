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
	// delivered via loop.EnvConfig (see Service.Deployment).
	meteringService = "cron-trigger"
	// meteringResource is the resource pool cron records apply to.
	meteringResource = "trigger_registrations"
	// meteringResourceType is the billing unit for cron registrations.
	meteringResourceType = "operations"
)

type Config struct {
	FastestScheduleIntervalSeconds int `json:"fastestScheduleIntervalSeconds"`
}

type Response struct {
	capabilities.TriggerEvent
	Payload cron.Payload
}

type cronTrigger struct {
	job           gocron.Job
	nextRun       time.Time
	workflowID    string
	workflowDonID uint32
	workflowOwner string
	orgID         string
	close         func()
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
	// the deployment/node dimensions (Deployment) and DON dimension at Initialise.
	base resourcemanager.ResourceIdentity
	// Deployment carries the static deployment/node identity dimensions
	// delivered to the plugin process via loop.EnvConfig. It is set once at
	// startup (by main, before Initialise) and read when building the base
	// metering identity. The zero value is valid and leaves those dimensions
	// empty.
	Deployment  resourcemanager.DeploymentIdentity
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

// donID returns the DON identifier stamped on metering identity and event labels.
func (s *Service) donID() string {
	return s.base.DonID()
}

// utilizationFields builds the per-trigger billing fields shared by the record
// and snapshot paths. resource_id is the workflow-scoped trigger_id; org_id is
// resolved at registration time and stored on the cronTrigger. event_id is
// intentionally absent from the fields: for records the caller passes it
// explicitly (a deterministic cross-node id), and for snapshots the
// ResourceManager derives it from the bucket/resource/node key.
func (s *Service) utilizationFields(triggerID, orgID string) resourcemanager.UtilizationFields {
	return resourcemanager.UtilizationFields{
		ResourceType: meteringResourceType,
		ResourceID:   triggerID,
		OrgID:        orgID,
	}
}

// emitMeterRecord emits a signed delta MeterRecord (METER_ACTION_UPDATE) for a
// change to the durable cron-registration level: register bills +1, unregister
// bills -1. orgID is resolved by the caller before invoking this method.
// Emission is fail-open and never affects the registration itself.
func (s *Service) emitMeterRecord(ctx context.Context, delta int64, namespace, workflowID, triggerID, orgID string) {
	eventID := resourcemanager.EventID(namespace, workflowID, triggerID)
	s.meters.EmitDelta(ctx, s.base, eventID, delta, s.utilizationFields(triggerID, orgID))
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

	if dependencies.OrgResolver == nil {
		s.lggr.Warn("OrgResolver is nil, cron capability will not be able to fetch organization ID")
	} else {
		s.orgResolver = dependencies.OrgResolver
	}

	// Build the base metering identity. The deployment/node dimensions come from
	// s.Deployment (delivered via loop.EnvConfig, set by main before Initialise).
	s.base = resourcemanager.NewBaseIdentity(s.Deployment, meteringService, meteringResource)
	if dependencies.CapabilityDonID != 0 {
		s.base = s.base.WithDonID(strconv.FormatUint(uint64(dependencies.CapabilityDonID), 10))
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
					events.KeyDonID, s.donID(),
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
			// Re-check existence atomically with the write: an unregister that
			// ran during this callback (after the Read above) deletes the
			// trigger, and resurrecting it here would keep the resource billed
			// via snapshots after the caller stopped it. WriteIfPresent skips
			// the write when the trigger is already gone.
			if written := s.triggers.WriteIfPresent(triggerID, cronTrigger{
				job:           job,
				nextRun:       nextExecutionTime,
				workflowID:    metadata.WorkflowID,
				workflowDonID: metadata.WorkflowDonID,
				workflowOwner: metadata.WorkflowOwner,
				orgID:         trigger.orgID,
				close:         closeCh,
			}); !written {
				return // unregistered concurrently; do not resurrect or send
			}

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

	var orgID string
	if s.orgResolver != nil && metadata.WorkflowOwner != "" {
		if resolved, err := s.orgResolver.Get(ctx, metadata.WorkflowOwner); err != nil {
			logger.Sugared(s.lggr).Warnw("failed to resolve org ID for metering", "owner", metadata.WorkflowOwner, "err", err)
		} else {
			orgID = resolved
		}
	}

	s.triggers.Write(triggerID, cronTrigger{
		job:           job,
		nextRun:       firstRunTime,
		workflowID:    metadata.WorkflowID,
		workflowDonID: metadata.WorkflowDonID,
		workflowOwner: metadata.WorkflowOwner,
		orgID:         orgID,
		close:         closeCh,
	})

	// Register bills a +1 delta to the durable trigger-registration level.
	s.emitMeterRecord(ctx, 1, "cron-register", metadata.WorkflowID, triggerID, orgID)

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

	// Unregister bills a -1 delta. workflowID and orgID come from the STORED
	// trigger (not the unregister request's metadata) so the delta reverses
	// the exact identity the register +1 billed.
	s.emitMeterRecord(ctx, -1, "cron-unregister", trigger.workflowID, triggerID, trigger.orgID)

	s.lggr.Debugw("UnregisterTrigger", "triggerId", triggerID, "jobId", jobID)
	s.metrics.DecActiveTriggersGauge(ctx)
	return nil
}

// start is the services.Engine start hook. The ResourceManager sub-service has
// already been started by the engine, so start registers this Service as a
// Meterable (the RM polls it once per snapshot tick) and starts the scheduler,
// refreshing next-run times for any registrations that survived a restart.
func (s *Service) start(ctx context.Context) error {
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
			job:           trigger.job,
			nextRun:       nextExecutionTime,
			workflowID:    trigger.workflowID,
			workflowDonID: trigger.workflowDonID,
			workflowOwner: trigger.workflowOwner,
			orgID:         trigger.orgID,
			close:         trigger.close,
		})
		if err != nil {
			s.lggr.Errorw("Unable to get next run time", "err", err, "triggerID", triggerID)
		}
	}

	return nil
}

// close is the services.Engine close hook. After this the Service cannot be
// started again; it must be re-built to schedule again. There are NO
// process-lifecycle metering emissions: a graceful shutdown emits nothing, and
// billing releases each still-active registration by its absence from the next
// snapshot. close deregisters the Meterable from the ResourceManager FIRST (so
// no snapshot can run after the store is torn down), then shuts the scheduler
// down. The ResourceManager sub-service is closed by the engine afterwards.
func (s *Service) close() error {
	if s.scheduler == nil {
		return errors.New("service has shutdown, it must be built again to restart")
	}

	// Deregister from the snapshot registry before anything else so no snapshot
	// tick can observe a half-torn-down service.
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
// six-dimension identity (per-resource billing fields are set per active
// trigger in
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
	for triggerID, trigger := range triggers {
		// Use stored orgID resolved at registration time.
		entries = append(entries, resourcemanager.SnapshotEntry{
			Identity: s.base,
			Utilizations: []*meteringpb.Utilization{
				resourcemanager.NewUtilizationInt(1, s.utilizationFields(triggerID, trigger.orgID)),
			},
		})
	}
	return entries
}
