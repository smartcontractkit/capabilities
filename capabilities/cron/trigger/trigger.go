package trigger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/jonboulle/clockwork"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows"
)

const ID = "cron-trigger@1.0.0"

const defaultSendChannelBufferSize = 1000

var cronTriggerInfo = capabilities.MustNewCapabilityInfo(
	ID,
	capabilities.CapabilityTypeTrigger,
	"A trigger that uses a cron schedule to run periodically at fixed times, dates, or intervals.",
)

type Config struct {
	// The cron schedule expression to evaluate for scheduling.
	// Supports an optional sixth entry for second granularity.
	Schedule string `json:"schedule"`
}

type Input struct{}

type Metadata struct{}

type Payload struct {
	// Time that cron trigger's task execution occurred (RFC3339Nano formatted)
	ActualExecutionTime string
	// Time that cron trigger's task execution had been scheduled to occur (RFC3339Nano formatted)
	ScheduledExecutionTime string
}

type Response struct {
	capabilities.TriggerEvent
	Metadata Metadata
	Payload  Payload
}

type cronTrigger struct {
	ch      chan<- capabilities.TriggerResponse
	job     gocron.Job
	nextRun time.Time
}

type Service struct {
	capabilities.CapabilityInfo
	capabilities.Validator[Config, Input, capabilities.TriggerResponse]
	clock     clockwork.Clock
	lggr      logger.Logger
	scheduler gocron.Scheduler
	triggers  *cronStore
}

type Params struct {
	Logger logger.Logger
	Clock  clockwork.Clock
}

var _ capabilities.TriggerCapability = (*Service)(nil)
var _ services.Service = &Service{}

// Creates a new Cron Trigger Service.
// Scheduling will commence on calling .Start()
func New(p Params) *Service {
	l := logger.Named(p.Logger, "Service")

	var options []gocron.SchedulerOption
	options = append(options, gocron.WithMonitor(NewCronMonitor()))
	// Set scheduler location to UTC for consistency across nodes.
	options = append(options, gocron.WithLocation(time.UTC))
	// Adapt chainlink logger to gocron logger interface.
	options = append(options, gocron.WithLogger(NewCronLogger(l)))
	// Allow injecting a clock for testing. Otherwise use system clock.
	if p.Clock != nil {
		options = append(options, gocron.WithClock(p.Clock))
	} else {
		p.Clock = clockwork.NewRealClock()
	}
	s, err := gocron.NewScheduler(options...)
	if err != nil {
		return nil
	}

	cronStore := NewCronStore()

	return &Service{
		CapabilityInfo: cronTriggerInfo,
		Validator:      capabilities.NewValidator[Config, Input, capabilities.TriggerResponse](capabilities.ValidatorArgs{Info: cronTriggerInfo}),
		clock:          p.Clock,
		triggers:       cronStore,
		lggr:           l,
		scheduler:      s,
	}
}

// Register a new trigger
// Can register triggers before the service is actively scheduling
func (s *Service) RegisterTrigger(ctx context.Context, req capabilities.TriggerRegistrationRequest) (<-chan capabilities.TriggerResponse, error) {
	if req.Config == nil {
		return nil, errors.New("config is required to register a cron trigger")
	}
	config, err := s.ValidateConfig(req.Config)
	if err != nil {
		return nil, err
	}

	_, ok := s.triggers.Read(req.TriggerID)
	if ok {
		return nil, fmt.Errorf("triggerId %s already registered", req.TriggerID)
	}

	var job gocron.Job
	callbackCh := make(chan capabilities.TriggerResponse, defaultSendChannelBufferSize)

	allowSeconds := true
	jobDef := gocron.CronJob(config.Schedule, allowSeconds)

	task := gocron.NewTask(
		// Task callback, executed at next run time
		func() {
			trigger, ok := s.triggers.Read(req.TriggerID)
			if !ok {
				// Invariant: The trigger should always exist, as unregistering the trigger removes the job
				s.lggr.Errorw("task callback invariant: trigger no longer exists", "triggerID", req.TriggerID)
				return
			}
			scheduledExecutionTimeUTC := trigger.nextRun.UTC()
			currentTimeUTC := s.clock.Now().UTC()

			executionID, response := createTriggerResponse(req.Metadata.WorkflowID, scheduledExecutionTimeUTC, currentTimeUTC)

			if response.Err != nil {
				s.lggr.Errorw("task callback failed to create response", "executionID", executionID, "triggerID", req.TriggerID, "err", response.Err)
			} else {
				s.lggr.Debugw("task callback sending trigger response", "executionID", executionID, "triggerID", req.TriggerID, "scheduledExecTimeUTC", scheduledExecutionTimeUTC.Format(time.RFC3339Nano), "actualExecTimeUTC", currentTimeUTC.Format(time.RFC3339Nano))
			}

			nextExecutionTime, nextRunErr := job.NextRun()
			if nextRunErr != nil {
				// .NextRun() will error if the job no longer exists
				// or if there is no next run to schedule, which shouldn't happen with cron jobs
				s.lggr.Errorw("task callback failed to schedule next run", "executionID", executionID, "triggerID", req.TriggerID)
			}
			s.triggers.Write(req.TriggerID, cronTrigger{
				ch:      callbackCh,
				job:     job,
				nextRun: nextExecutionTime,
			})

			select {
			case callbackCh <- response:
			default:
				s.lggr.Errorw("channel full, dropping event", "executionID", executionID, "triggerID", req.TriggerID, "eventID", response.Event.ID)
			}
		})

	if s.scheduler == nil {
		return nil, errors.New("cannot register a new trigger, service has been closed")
	}

	// If service has already started, job will be scheduled immediately
	job, err = s.scheduler.NewJob(jobDef, task, gocron.WithName(req.TriggerID))
	if err != nil {
		s.lggr.Errorw("failed to create new job", "err", err)
		return nil, err
	}

	firstRunTime, err := job.NextRun()
	if err != nil {
		// errors if job no longer exists on scheduler
		s.lggr.Errorw("failed to get next run time", "err", err)
		// ensure that it is out of scheduler
		err := s.scheduler.RemoveJob(job.ID())
		return nil, err
	}

	s.triggers.Write(req.TriggerID, cronTrigger{
		ch:      callbackCh,
		job:     job,
		nextRun: firstRunTime,
	})

	s.lggr.Debugw("RegisterTrigger", "triggerId", req.TriggerID, "jobId", job.ID())
	PromTotalTriggersCount.Inc()
	return callbackCh, nil
}

func createTriggerResponse(workflowID string, scheduledExecutionTime time.Time, currentTime time.Time) (string, capabilities.TriggerResponse) {
	// Ensure UTC time is used for consistency across nodes.
	scheduledExecutionTimeUTC := scheduledExecutionTime.UTC()
	currentTimeUTC := currentTime.UTC()

	// Use the scheduled execution time as a deterministic identifier.
	// Since cron schedules only go to second granularity this should never have ms.
	// Just in case, truncate on seconds by formatting to ensure consistency across nodes.
	scheduledExecutionTimeFormatted := scheduledExecutionTimeUTC.Format(time.RFC3339)
	hash := sha256.Sum256([]byte(scheduledExecutionTimeFormatted))
	triggerEventID := hex.EncodeToString(hash[:])
	executionID, err := workflows.EncodeExecutionID(workflowID, triggerEventID)
	if err != nil {
		// Notice: Execution ID will be empty
		return "", capabilities.TriggerResponse{
			Err: fmt.Errorf("task callback could not generate execution ID: %s", err),
		}
	}

	// Show difference between scheduled and actual execution by including nanoseconds
	payload := Payload{
		ScheduledExecutionTime: scheduledExecutionTimeUTC.Format(time.RFC3339Nano),
		ActualExecutionTime:    currentTimeUTC.Format(time.RFC3339Nano),
	}
	wrappedPayload, err := values.WrapMap(payload)
	if err != nil {
		return executionID, capabilities.TriggerResponse{
			Err: fmt.Errorf("error wrapping trigger event: %s", err),
		}
	}

	return executionID, capabilities.TriggerResponse{
		Event: capabilities.TriggerEvent{
			TriggerType: ID,
			Outputs:     wrappedPayload,
		},
	}
}

func (s *Service) UnregisterTrigger(ctx context.Context, req capabilities.TriggerRegistrationRequest) error {
	trigger, ok := s.triggers.Read(req.TriggerID)
	if !ok {
		return fmt.Errorf("triggerId %s not found", req.TriggerID)
	}

	jobID := trigger.job.ID()

	// Remove job from scheduler
	if s.scheduler == nil {
		return errors.New("cannot unregister a new trigger, service has been closed")
	}
	err := s.scheduler.RemoveJob(jobID)
	if err != nil {
		return fmt.Errorf("UnregisterTrigger failed to remove job from scheduler: %s", err)
	}

	// Close callback channel
	close(trigger.ch)
	// Remove from triggers context
	s.triggers.Delete(req.TriggerID)

	s.lggr.Debugw("UnregisterTrigger", "triggerId", req.TriggerID, "jobId", jobID)
	PromTotalTriggersCount.Dec()
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
			ch:      trigger.ch,
			job:     trigger.job,
			nextRun: nextExecutionTime,
		})
		if err != nil {
			s.lggr.Errorw("Unable to get next run time", "err", err, "triggerID", triggerID)
		}
	}

	s.lggr.Info(s.Name() + " started")

	PromRunningServices.Inc()

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

	PromRunningServices.Dec()

	return nil
}

func (s *Service) Ready() error {
	return nil
}

func (s *Service) HealthReport() map[string]error {
	return map[string]error{s.Name(): nil}
}

func (s *Service) Name() string {
	return "Service"
}
