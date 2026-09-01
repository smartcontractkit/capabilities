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
	crontypedapi "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/cron"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/cron/server"
	"github.com/smartcontractkit/chainlink-common/pkg/custmsg"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/resourcemanager"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/events"

	"github.com/smartcontractkit/capabilities/libs/triggermeter"
)

const ServiceName = "CronCapabilities"

const defaultSendChannelBufferSize = 1000

var cronTriggerInfo = capabilities.MustNewCapabilityInfo(
	server.CronID,
	capabilities.CapabilityTypeTrigger,
	"A trigger that uses a cron schedule to run periodically at fixed times, dates, or intervals.",
)

// meteringConfig carries the cron trigger's metering identity constants:
// the stable service constant (it must not encode deployment environment or
// zone — those are discrete identity dimensions delivered via loop.EnvConfig,
// see Service.Deployment), the resource pool snapshots apply to, and the
// billing unit per registration.
var meteringConfig = triggermeter.Config{
	Service:      "cron-trigger",
	ResourcePool: "trigger_registrations",
	ResourceType: "operations",
}

type Config struct {
	FastestScheduleIntervalSeconds int `json:"fastestScheduleIntervalSeconds"`
}

type Payload struct {
	ScheduledExecutionTime string `json:"ScheduledExecutionTime" yaml:"ScheduledExecutionTime" mapstructure:"ScheduledExecutionTime"`
}

type Response struct {
	capabilities.TriggerEvent
	Payload Payload
}

type cronTrigger struct {
	job           gocron.Job
	nextRun       time.Time
	workflowID    string
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
	// rm is the ResourceManager handed to NewTriggerService (possibly nil:
	// metering off); Initialise wraps it in the meter, which owns its
	// lifecycle from then on.
	rm *resourcemanager.ResourceManager
	// meter owns every metering concern (RM lifecycle, identity, org
	// resolution, snapshot registration). Nil-receiver-safe: it is nil until
	// Initialise and a no-op when metering is off.
	meter *triggermeter.TriggerMeter
	// Deployment carries the static deployment/node identity dimensions
	// delivered to the plugin process via loop.EnvConfig. It is set once at
	// startup (by main, before Initialise) and read when building the base
	// metering identity. The zero value is valid and leaves those dimensions
	// empty.
	Deployment resourcemanager.DeploymentIdentity
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

var _ services.Service = &Service{}

// NewTriggerService creates a new trigger service.  Optionally, a clock can be passed in for testing, if nil
// the system clock will be used.
// meters reports trigger registrations for billing; nil means metering is off.
func NewTriggerService(parentLggr logger.Logger, clock clockwork.Clock, limitsFactory limits.Factory, meters *resourcemanager.ResourceManager) (*Service, error) {
	lggr := logger.Named(parentLggr, "CRONTrigger")

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
		rm:      meters,
	}

	// The scheduler is started/stopped in s.start / s.close; the meter (built
	// at Initialise) owns the ResourceManager lifecycle from those same hooks.
	s.Service, s.srvcEng = services.Config{
		Name:  "CronTrigger",
		Start: s.start,
		Close: s.close,
	}.NewServiceEngine(lggr)

	return s, nil
}

// snapshotRows reports the absolute state of every currently active cron
// registration for the meter's snapshot tick: one row per trigger, each at
// value 1 (a registration is a single reserved unit), with the org that was
// resolved and stored at registration time. It is a cheap in-memory read of
// the store snapshot. Cron emits NO MeterRecord deltas: billing follows the
// snapshot level, and a registration is released by its absence from the next
// snapshot.
func (s *Service) snapshotRows(context.Context) []triggermeter.SnapshotRow {
	triggers := s.triggers.ReadAll()
	rows := make([]triggermeter.SnapshotRow, 0, len(triggers))
	for triggerID, trigger := range triggers {
		rows = append(rows, triggermeter.SnapshotRow{
			Value:      1,
			ResourceID: triggerID,
			OrgID:      trigger.orgID,
		})
	}
	return rows
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
	}

	// Build the meter: it owns the ResourceManager lifecycle, the base metering
	// identity (deployment/node dimensions from s.Deployment via loop.EnvConfig,
	// DON dimension from the host-injected CapabilityDonID), org resolution, and
	// the snapshot registration over snapshotRows.
	s.meter = triggermeter.New(s.lggr, s.rm, s.Deployment, dependencies.CapabilityDonID, meteringConfig, dependencies.OrgResolver, s.snapshotRows)

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
				// Try to fetch organization ID (fail-open, panic-safe in the meter).
				orgID := s.meter.ResolveOrg(ctx, metadata.WorkflowOwner)

				// CRE-4409: event labels prefer the capability DON ID but fall
				// back to the consumer workflow's DON ID when it is not (yet)
				// initialised — a best-effort label beats an absent one.
				// Metering deliberately does NOT share this fallback (see
				// triggermeter.TriggerMeter.DonID).
				donIDLabel, donIDErr := s.meter.DonID()
				if donIDErr != nil && metadata.WorkflowDonID != 0 {
					donIDLabel = strconv.FormatUint(uint64(metadata.WorkflowDonID), 10)
				}

				// Emit TriggerExecutionStarted event
				labeler := custmsg.NewLabeler().With(
					events.KeyTriggerID, response.Id,
					events.KeyWorkflowID, trigger.workflowID,
					events.KeyWorkflowExecutionID, workflowExecutionID,
					events.KeyWorkflowOwner, metadata.WorkflowOwner,
					events.KeyWorkflowName, displayWorkflowName,
					events.KeyDonID, donIDLabel,
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

	// Resolve the org once at registration and store it: the snapshot path
	// (snapshotRows) must be network-free, so it reads this stored value.
	orgID := s.meter.ResolveOrg(ctx, metadata.WorkflowOwner)

	s.triggers.Write(triggerID, cronTrigger{
		job:           job,
		nextRun:       firstRunTime,
		workflowID:    metadata.WorkflowID,
		workflowOwner: metadata.WorkflowOwner,
		orgID:         orgID,
		close:         closeCh,
	})

	// No MeterRecord delta: billing observes the new registration in the next
	// snapshot (snapshotRows). Trigger capabilities are snapshot-only producers
	// — see the triggermeter package doc for why deltas are structurally
	// unsound here.

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

	// Remove from triggers context. No MeterRecord delta: billing releases the
	// registration by its absence from the next snapshot (snapshotRows).
	s.triggers.Delete(triggerID)

	s.lggr.Debugw("UnregisterTrigger", "triggerId", triggerID, "jobId", jobID)
	s.metrics.DecActiveTriggersGauge(ctx)
	return nil
}

// start is the services.Engine start hook. It starts the meter (which owns
// the ResourceManager lifecycle and snapshot registration; fail-open, so a
// metering failure never gates the trigger) and the scheduler, refreshing
// next-run times for any registrations that survived a restart.
func (s *Service) start(ctx context.Context) error {
	if s.scheduler == nil {
		return errors.New("service has shutdown, it must be built again to restart")
	}

	if err := s.meter.Start(ctx); err != nil {
		return err
	}

	s.scheduler.Start()

	for triggerID, trigger := range s.triggers.ReadAll() {
		nextExecutionTime, err := trigger.job.NextRun()
		s.triggers.Write(triggerID, cronTrigger{
			job:           trigger.job,
			nextRun:       nextExecutionTime,
			workflowID:    trigger.workflowID,
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
// snapshot. close closes the meter FIRST (deregistering the snapshot Meterable
// so no tick can observe a half-torn-down service, then closing the
// ResourceManager), then shuts the scheduler down.
func (s *Service) close() error {
	if s.scheduler == nil {
		return errors.New("service has shutdown, it must be built again to restart")
	}

	meterErr := s.meter.Close()

	err := s.scheduler.Shutdown()
	if err != nil {
		return errors.Join(meterErr, fmt.Errorf("scheduler shutdown encountered a problem: %s", err))
	}
	if meterErr != nil {
		return meterErr
	}

	// After .Shutdown() the scheduler cannot be started again,
	// but calling .Start() on it will not error. Set to nil to mark closed.
	s.scheduler = nil

	return nil
}

func (s *Service) Description() string {
	return "Cron Trigger Capability"
}
