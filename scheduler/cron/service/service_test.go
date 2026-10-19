package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smartcontractkit/capabilities/scheduler/cron/protos"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services/orgresolver"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows"
)

// These tests need the Service itself rather than what a run builds.

func TestCronTrigger_CloseStartErrors(t *testing.T) {
	fakeClock := clockwork.NewRealClock()
	ts, err := newService(logger.Nop(), fakeClock, limits.Factory{}, Config{}, nil)
	require.NoError(t, err)
	ctx := t.Context()

	err = ts.Start(ctx)
	require.NoError(t, err)
	err = ts.Close()
	require.NoError(t, err)
	err = ts.Start(ctx)
	require.Error(t, err)
}

func TestCronTrigger_ReadyAndHealthFollowLifecycle(t *testing.T) {
	ts, err := newService(logger.Nop(), clockwork.NewRealClock(), limits.Factory{}, Config{}, nil)
	require.NoError(t, err)

	require.Error(t, ts.Ready())
	require.Error(t, ts.HealthReport()[ts.Name()])

	require.NoError(t, ts.Start(t.Context()))
	require.NoError(t, ts.Ready())
	require.NoError(t, ts.HealthReport()[ts.Name()])

	require.NoError(t, ts.Close())
	require.Error(t, ts.Ready())
	require.Error(t, ts.HealthReport()[ts.Name()])
}

func TestCronTrigger_CloseBeforeStart(t *testing.T) {
	ts, err := newService(logger.Nop(), clockwork.NewRealClock(), limits.Factory{}, Config{}, nil)
	require.NoError(t, err)

	require.NoError(t, ts.Close())
	require.NoError(t, ts.Close())
}

func TestCronTrigger_CloseTwice(t *testing.T) {
	ts, err := newService(logger.Nop(), clockwork.NewRealClock(), limits.Factory{}, Config{}, nil)
	require.NoError(t, err)
	require.NoError(t, ts.Start(t.Context()))

	require.NoError(t, ts.Close())
	require.NoError(t, ts.Close())
}

// TestCronTrigger_NoLogsAfterClose covers gocron, which keeps logging for a moment after Shutdown returns:
// none of it may reach the service's logger once Close has returned.
func TestCronTrigger_NoLogsAfterClose(t *testing.T) {
	for range 20 {
		lggr, observedLogs := logger.TestObserved(t, zap.DebugLevel)
		ts, err := newService(lggr, clockwork.NewFakeClock(), limits.Factory{}, Config{}, nil)
		require.NoError(t, err)
		require.NoError(t, ts.Start(t.Context()))

		require.NoError(t, ts.Close())
		closed := observedLogs.Len()

		time.Sleep(10 * time.Millisecond)
		require.Equal(t, closed, observedLogs.Len(), "logged after Close returned: %v", observedLogs.All()[closed:])
	}
}

type panicOnNowClock struct {
	clockwork.Clock
}

func (c *panicOnNowClock) Now() time.Time {
	panic("clock panic in Now()")
}

func TestGocronNewTaskPanic(t *testing.T) {
	fakeClock := clockwork.NewFakeClock()

	lggr, observedLogs := logger.TestObserved(t, zap.ErrorLevel)
	ts := newStartedService(t, lggr, fakeClock, Config{FastestScheduleIntervalSeconds: 1})

	_, capErr := ts.RegisterTrigger(t.Context(), triggerID1, capabilities.RequestMetadata{WorkflowID: workflowID1}, &protos.Config{Schedule: everySecond})
	require.Nil(t, capErr)

	// Swap ts.clock after setup so gocron's scheduler/executor keep using the
	// original fakeClock, while the task callback (trigger.go s.clock.Now())
	// hits the panicking clock.
	ts.clock = &panicOnNowClock{Clock: fakeClock}

	fakeClock.Advance(time.Second * 1)

	ticker := time.NewTicker(time.Second * 1)
	timeout := time.NewTimer(time.Second * 10)
	for {
		select {
		case <-ticker.C:
			logs := observedLogs.All()
			for _, log := range logs {
				if log.Message == "panic in gocron.NewTask function" && len(log.Context) > 0 && log.Context[0].Key == "err" && log.Context[0].String == "clock panic in Now()" {
					return
				}
			}
		case <-timeout.C:
			t.Fatal("timeout, no panic in gocron.NewTask function")
		}
	}
}

// MockOrgResolver is a mock implementation of the OrgResolver interface
type MockOrgResolver struct {
	mock.Mock
}

func (m *MockOrgResolver) Get(ctx context.Context, owner string) (string, error) {
	args := m.Called(ctx, owner)
	return args.String(0), args.Error(1)
}

func (m *MockOrgResolver) Start(ctx context.Context) error {
	return nil
}

func (m *MockOrgResolver) Close() error {
	return nil
}

func (m *MockOrgResolver) HealthReport() map[string]error {
	return map[string]error{}
}

func (m *MockOrgResolver) Ready() error {
	return nil
}

func (m *MockOrgResolver) Name() string {
	return "MockOrgResolver"
}

// Ensure MockOrgResolver implements the interface
var _ orgresolver.OrgResolver = (*MockOrgResolver)(nil)

// TestCronTrigger_ExecutionIDWithTriggerIndex verifies execution IDs include the
// trigger index parsed from ReferenceID.
func TestCronTrigger_ExecutionIDWithTriggerIndex(t *testing.T) {
	t.Parallel()

	const testWorkflowID = "workflow-id-multi-trigger-test"
	const testReferenceID = "trigger_2" // parsed to triggerIndex = 2
	const testTriggerIndex = 2
	const testTriggerID = testWorkflowID + "|test-trigger"

	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	fakeClock := clockwork.NewFakeClockAt(startTime)
	lggr, observedLogs := logger.TestObserved(t, zap.DebugLevel)

	ts := newStartedService(t, lggr, fakeClock, Config{FastestScheduleIntervalSeconds: 1})

	metadata := capabilities.RequestMetadata{
		WorkflowID:  testWorkflowID,
		ReferenceID: testReferenceID,
	}
	ch, capErr := ts.RegisterTrigger(t.Context(), testTriggerID, metadata, &protos.Config{Schedule: everySecond})
	require.Nil(t, capErr)

	fakeClock.Advance(time.Second + time.Millisecond)
	msg := <-ch

	expectedExecID, err := workflows.GenerateExecutionIDWithTriggerIndex(testWorkflowID, msg.Id, testTriggerIndex)
	require.NoError(t, err)

	// The debug log at "sending trigger event" is written before the channel
	// send, so it is already present once we receive msg.
	var execIDFromLog string
	var found bool
	for _, entry := range observedLogs.All() {
		if entry.Message == "sending trigger event" {
			for _, field := range entry.Context {
				if field.Key == "executionID" {
					execIDFromLog = field.String
				}
			}
			found = true
			break
		}
	}
	require.True(t, found, "expected log entry 'sending trigger event'")
	require.Equal(t, expectedExecID, execIDFromLog, "execution ID should match expected hash function")

	require.Nil(t, ts.UnregisterTrigger(t.Context(), testTriggerID, metadata, &protos.Config{Schedule: everySecond}))
}

func TestEnforceFastestSchedule_NonUniformSecondsField(t *testing.T) {
	t.Parallel()
	// a schedule that has a bunch of 5s gaps followed by a bunch of 1s gaps
	// requires checking multiple runs to catch the short gaps
	schedule := "0,5,10,15,20,25,30,35,40,45,50,55-59 * * * * *"
	maximumFastest := 5 * time.Second

	jobDef := gocron.CronJob(schedule, true)
	capErr := enforceFastestSchedule(logger.Nop(), gocron.NewScheduler, jobDef, maximumFastest)
	require.NotNil(t, capErr, "should reject schedule with 1s gaps")
	require.Equal(t, caperrors.LimitExceeded, capErr.Code())
	require.Contains(t, capErr.Error(), "maximum fastest cron schedule is 5s")
}

type mockScheduler struct {
	gocron.Scheduler
	shutdowns int
}

func (s *mockScheduler) Shutdown() error {
	s.shutdowns++
	return s.Scheduler.Shutdown()
}

func mockFactory(mocks *[]*mockScheduler) schedulerFactory {
	return func(options ...gocron.SchedulerOption) (gocron.Scheduler, error) {
		sched, err := gocron.NewScheduler(options...)
		if err != nil {
			return nil, err
		}
		mock := &mockScheduler{Scheduler: sched}
		*mocks = append(*mocks, mock)
		return mock, nil
	}
}

// TestEnforceFastestSchedule_ShutsDownTempScheduler covers a regression where the
// temporary scheduler's Shutdown was deferred below the NewJob error check, so every
// workflow-supplied cron string that gocron failed to parse retained a scheduler
// goroutine for the life of the plugin. Each schedule below exercises a different
// return path through the check; all of them must dispose of the scheduler.
func TestEnforceFastestSchedule_ShutsDownTempScheduler(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		schedule  string
		shouldErr bool
	}{
		{name: "valid schedule", schedule: everyMinute, shouldErr: false},
		{name: "invalid timezone", schedule: "TZ=moon * * * * *", shouldErr: true},
		{name: "empty schedule", schedule: "", shouldErr: true},
		{name: "not a cron schedule", schedule: "d d d d d", shouldErr: true},
		{name: "exceeds maximum fastest", schedule: everySecond, shouldErr: true},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var mocks []*mockScheduler
			capErr := enforceFastestSchedule(logger.Nop(), mockFactory(&mocks), gocron.CronJob(tt.schedule, true), 5*time.Second)
			if tt.shouldErr {
				require.NotNil(t, capErr, "schedule %q should have been rejected", tt.schedule)
			} else {
				require.Nil(t, capErr)
			}

			require.Len(t, mocks, 1, "expected exactly one temporary scheduler to be created")
			require.Equal(t, 1, mocks[0].shutdowns, "temporary scheduler was not shut down on this path")
		})
	}
}

func TestEnforceFastestSchedule_SchedulerConstructionFails(t *testing.T) {
	t.Parallel()

	failing := func(...gocron.SchedulerOption) (gocron.Scheduler, error) {
		return nil, errors.New("cannot construct")
	}
	capErr := enforceFastestSchedule(logger.Nop(), failing, gocron.CronJob(everyMinute, true), 5*time.Second)
	require.NotNil(t, capErr)
	require.Equal(t, caperrors.Internal, capErr.Code())
}

// newStartedService is a started service on clock, closed when the test ends if the test has not
// closed it itself.
func newStartedService(t *testing.T, lggr logger.Logger, clock clockwork.Clock, cfg Config) *service {
	t.Helper()

	ts, err := newService(lggr, clock, limits.Factory{}, cfg, nil)
	require.NoError(t, err)
	require.NoError(t, ts.Start(t.Context()))
	t.Cleanup(func() { _ = ts.Close() })

	return ts
}
