package service

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/jonboulle/clockwork"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/smartcontractkit/capabilities/scheduler/cron/protos"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/custmsg"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/services/orgresolver"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/events"
)

const defaultSendChannelBufferSize = 1000

var cronTriggerInfo = capabilities.MustNewCapabilityInfo(
	protos.CronID,
	capabilities.CapabilityTypeTrigger,
	"A trigger that uses a cron schedule to run periodically at fixed times, dates, or intervals.",
)

type Config struct {
	// FastestScheduleIntervalSeconds is the shortest interval a schedule may run at. Unset uses the
	// CRE settings default.
	FastestScheduleIntervalSeconds int `json:"fastestScheduleIntervalSeconds"`
}

type cronTrigger struct {
	job        gocron.Job
	nextRun    time.Time
	workflowID string
	close      func()
}

type service struct {
	services.StateMachine
	capabilities.CapabilityInfo
	fastestScheduleInterval limits.TimeLimiter
	clock                   clockwork.Clock
	lggr                    logger.Logger
	scheduler               gocron.Scheduler
	cronLogger              *CronLogger
	triggers                *cronStore
	labeler                 custmsg.MessageEmitter
	metrics                 *Metrics
	orgResolver             orgresolver.OrgResolver
}

var _ services.Service = &service{}

// newService creates a new trigger service, scheduling on clock. The orgResolver is optional and can be nil,
// but should be set in live environments.
func newService(parentLggr logger.Logger, clock clockwork.Clock, limitsFactory limits.Factory, cronConfig Config, orgResolver orgresolver.OrgResolver) (*service, error) {
	lggr := logger.Named(parentLggr, "CRONTrigger")

	metrics, err := NewMetrics()
	if err != nil {
		return nil, fmt.Errorf("error creating metrics: %w", err)
	}

	limit := cresettings.Default.PerWorkflow.CRONTrigger.FastestScheduleInterval // copy
	if cronConfig.FastestScheduleIntervalSeconds > 0 {
		limit.DefaultValue = time.Duration(cronConfig.FastestScheduleIntervalSeconds) * time.Second
	}
	limiter, err := limitsFactory.MakeTimeLimiter(limit)
	if err != nil {
		return nil, fmt.Errorf("failed to create limiter: %w", err)
	}

	if orgResolver == nil {
		lggr.Warn("OrgResolver is nil, cron capability will not be able to fetch organization ID")
	}

	var options []gocron.SchedulerOption
	options = append(options, gocron.WithMonitor(NewCronMonitor(metrics)))
	// Set scheduler location to UTC for consistency across nodes.
	options = append(options, gocron.WithLocation(time.UTC))
	// Adapt chainlink logger to gocron logger interface.
	cronLogger := NewCronLogger(lggr)
	options = append(options, gocron.WithLogger(cronLogger))
	options = append(options, gocron.WithClock(clock))

	scheduler, err := gocron.NewScheduler(options...)
	if err != nil {
		return nil, fmt.Errorf("error creating scheduler: %w", err)
	}

	return &service{
		lggr:                    lggr,
		CapabilityInfo:          cronTriggerInfo,
		fastestScheduleInterval: limiter,
		triggers:                NewCronStore(),
		scheduler:               scheduler,
		cronLogger:              cronLogger,
		clock:                   clock,
		labeler: custmsg.NewLabeler().With(
			"capabilityID", cronTriggerInfo.ID,
			"capabilityVersion", cronTriggerInfo.Version(),
			"capabilityName", cronTriggerInfo.ID,
		),
		metrics:     metrics,
		orgResolver: orgResolver,
	}, nil
}

func (s *service) RegisterTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *protos.Config) (<-chan capabilities.TriggerAndId[*protos.Payload], caperrors.Error) {
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
	callbackCh := make(chan capabilities.TriggerAndId[*protos.Payload], defaultSendChannelBufferSize)

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
	capErr := enforceFastestSchedule(s.lggr, gocron.NewScheduler, jobDef, limit)
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
				// Send trigger event even if we can't generate execution ID. Here the ID is used only for observability.
			}

			s.lggr.Debugw("sending trigger event", "executionID", workflowExecutionID, "triggerID", triggerID, "scheduledExecTimeUTC", scheduledExecutionTimeUTC.Format(time.RFC3339Nano), "actualExecTimeUTC", currentTimeUTC.Format(time.RFC3339Nano))

			// gocron only drops a scheduled time once it is in the past, so a run on its exact
			// scheduled instant can still be next. Skip it, or the next event repeats this one's time.
			nextExecutionTime, nextRunErr := nextRunAfter(job, scheduledExecutionTimeUTC)
			if nextRunErr != nil {
				// .NextRun() will error if the job no longer exists
				// or if there is no next run to schedule, which shouldn't happen with cron jobs
				s.lggr.Errorw("task callback failed to schedule next run", "executionID", workflowExecutionID, "triggerID", triggerID)
			}

			func() {
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
			}()

			if execIDErr != nil {
				return // don't emit anything if we couldn't generate execution ID
			}

			// Org resolution and event emission are done last, after trigger generation and
			// bookkeeping above, since they can involve slow network calls (org resolver lookup,
			// event bus) that must not delay the actual trigger or the next scheduled run.

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
			}
		})

	// If service has already started, job will be scheduled immediately
	if !s.IfNotStopped(func() { job, err = s.scheduler.NewJob(jobDef, task, gocron.WithName(triggerID)) }) {
		return nil, caperrors.NewPublicSystemError(errors.New("cannot register a new trigger, service has been closed"), caperrors.Internal)
	}
	if err != nil {
		s.lggr.Errorw("failed to create new job", "err", err)
		return nil, caperrors.NewPublicSystemError(fmt.Errorf("RegisterTrigger failed to create new job: %s", err), caperrors.Internal)
	}

	firstRunTime, err := job.NextRun()
	if err != nil {
		// errors if job no longer exists on scheduler
		s.lggr.Errorw("failed to get next run time", "err", err)
		// ensure that it is out of scheduler
		s.IfNotStopped(func() { err = s.scheduler.RemoveJob(job.ID()) })
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

func createTriggerResponse(scheduledExecutionTime time.Time) capabilities.TriggerAndId[*protos.Payload] {
	// Ensure UTC time is used for consistency across nodes.
	scheduledExecutionTimeUTC := scheduledExecutionTime.UTC()

	// Use the scheduled execution time as a deterministic identifier.
	// Since cron schedules only go to second granularity this should never have ms.
	// Just in case, truncate on seconds by formatting to ensure consistency across nodes.
	scheduledExecutionTimeFormatted := scheduledExecutionTimeUTC.Format(time.RFC3339)
	triggerEventID := scheduledExecutionTimeFormatted

	return capabilities.TriggerAndId[*protos.Payload]{
		Trigger: &protos.Payload{
			ScheduledExecutionTime: timestamppb.New(scheduledExecutionTimeUTC),
		},
		Id: triggerEventID,
	}
}

func (s *service) AckEvent(ctx context.Context, triggerID string, eventID string, method string) caperrors.Error {
	return nil
}

func (s *service) UnregisterTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *protos.Config) caperrors.Error {
	trigger, ok := s.triggers.Read(triggerID)
	if !ok {
		s.lggr.Warnw("trigger not found", "triggerID", triggerID)
		return nil
	}

	jobID := trigger.job.ID()

	// Remove job from scheduler
	var err error
	if !s.IfNotStopped(func() { err = s.scheduler.RemoveJob(jobID) }) {
		return caperrors.NewPublicSystemError(errors.New("cannot unregister a new trigger, service has been closed"), caperrors.Internal)
	}
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
func (s *service) Start(ctx context.Context) error {
	return s.StartOnce(s.Name(), s.start)
}

func (s *service) start() error {
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
// The service will need to be re-built to start scheduling again. Closing it again does nothing.
func (s *service) Close() error {
	err := s.StopOnce(s.Name(), s.close)
	switch {
	case errors.Is(err, services.ErrCannotStopUnstarted):
		// gocron runs a goroutine from construction rather than from Start, so a service closed
		// without having started, e.g. when Initialise fails, still has to shut the scheduler down.
		return s.close()
	case errors.Is(err, services.ErrAlreadyStopped):
		return nil
	}
	return err
}

func (s *service) close() error {
	err := s.scheduler.Shutdown()
	s.cronLogger.stop()
	if err != nil {
		return fmt.Errorf("scheduler shutdown encountered a problem: %s", err)
	}

	s.lggr.Info(s.Name() + " closed")

	return nil
}

func (s *service) HealthReport() map[string]error {
	return map[string]error{s.Name(): s.Healthy()}
}

func (s *service) Name() string {
	return s.lggr.Name()
}

func (s *service) Description() string {
	return "Cron Trigger Capability"
}

// nextRunAfter is the job's first scheduled run after t.
func nextRunAfter(job gocron.Job, t time.Time) (time.Time, error) {
	runs, err := job.NextRuns(2)
	if err != nil {
		return time.Time{}, err
	}
	for _, run := range runs {
		if run.After(t) {
			return run, nil
		}
	}
	return time.Time{}, fmt.Errorf("no run scheduled after %s", t)
}
