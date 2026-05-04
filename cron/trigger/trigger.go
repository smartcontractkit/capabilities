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
	"github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/custmsg"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/services/orgresolver"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/events"
)

const ServiceName = "CronCapabilities"

const defaultSendChannelBufferSize = 1000

var cronTriggerInfo = capabilities.MustNewCapabilityInfo(
	server.CronID,
	capabilities.CapabilityTypeTrigger,
	"A trigger that uses a cron schedule to run periodically at fixed times, dates, or intervals.",
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
	capabilities.CapabilityInfo
	limitsFactory           limits.Factory
	fastestScheduleInterval limits.TimeLimiter
	multiTriggerFlag        limits.RangeLimiter[config.Timestamp] // TODO(CRE-2774): remove when fully rolled out
	clock                   clockwork.Clock
	lggr                    logger.Logger
	scheduler               gocron.Scheduler
	triggers                *cronStore
	labeler                 custmsg.MessageEmitter
	metrics                 *Metrics
	orgResolver             orgresolver.OrgResolver
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
// the system clock will be used. The orgResolver is optional and can be nil, but should be set in live environments.
func NewTriggerService(parentLggr logger.Logger, clock clockwork.Clock, limitsFactory limits.Factory) (*Service, error) {
	lggr := logger.Named(parentLggr, "Service")

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

	return &Service{
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
	}, nil
}

func (s *Service) Initialise(ctx context.Context, dependencies core.StandardCapabilitiesDependencies) error {
	s.lggr.Debugf("Initialising %s", ServiceName)

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

	s.multiTriggerFlag, err = limits.MakeRangeLimiter(s.limitsFactory, cresettings.Default.PerWorkflow.FeatureMultiTriggerExecutionIDsActivePeriod)
	if err != nil {
		return fmt.Errorf("failed to create rangelimiter: %w", err)
	}

	s.orgResolver = dependencies.OrgResolver
	if s.orgResolver == nil {
		s.lggr.Warn("OrgResolver is nil, cron capability will not be able to fetch organization ID")
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
			var workflowExecutionID string
			var execIDErr error
			isLegacyExecutionID := true
			// NOTE: Relying on local time is not ideal but we don't have access to DONTime at this stage.
			if s.multiTriggerFlag.Check(ctx, config.NewTimestamp(currentTimeUTC)) == nil {
				workflowExecutionID, execIDErr = workflows.GenerateExecutionIDWithTriggerIndex(trigger.workflowID, response.Id, triggerIndex)
				isLegacyExecutionID = false
			} else { // legacy behavior
				workflowExecutionID, execIDErr = workflows.EncodeExecutionID(trigger.workflowID, response.Id) //nolint:staticcheck
			}

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

			s.lggr.Debugw("task callback sending trigger response", "executionID", workflowExecutionID, "isLegacyExecutionID", isLegacyExecutionID, "triggerID", triggerID, "scheduledExecTimeUTC", scheduledExecutionTimeUTC.Format(time.RFC3339Nano), "actualExecTimeUTC", currentTimeUTC.Format(time.RFC3339Nano))

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
		s.lggr.Warnf("triggerId %s not found", triggerID)
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

	s.lggr.Debugw("UnregisterTrigger", "triggerId", triggerID, "jobId", jobID)
	s.metrics.DecActiveTriggersGauge(ctx)
	return nil
}

// Start the service.
func (s *Service) Start(ctx context.Context) error {
	if s.scheduler == nil {
		return errors.New("service has shutdown, it must be built again to restart")
	}

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

	s.lggr.Info(s.Name() + " started")

	return nil
}

// Close stops the Service.
// After this call the Service cannot be started again,
// The service will need to be re-built to start scheduling again.
func (s *Service) Close() error {
	if s.scheduler == nil {
		return errors.New("service has shutdown, it must be built again to restart")
	}

	err := s.scheduler.Shutdown()
	if err != nil {
		return fmt.Errorf("scheduler shutdown encountered a problem: %s", err)
	}

	// After .Shutdown() the scheduler cannot be started again,
	// but calling .Start() on it will not error. Set to nil to mark closed.
	s.scheduler = nil

	s.lggr.Info(s.Name() + " closed")

	return nil
}

func (s *Service) Ready() error {
	return nil
}

func (s *Service) HealthReport() map[string]error {
	return map[string]error{s.Name(): nil}
}

func (s *Service) Name() string {
	return s.lggr.Name()
}

func (s *Service) Description() string {
	return "Cron Trigger Capability"
}
