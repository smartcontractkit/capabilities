package service

import (
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/libs/capability"
	"github.com/smartcontractkit/capabilities/libs/capability/capabilitytest"
	"github.com/smartcontractkit/capabilities/libs/capability/instances"
	"github.com/smartcontractkit/capabilities/scheduler/cron/protos"
	"github.com/smartcontractkit/capabilities/scheduler/cron/protos/protostest"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
)

const (
	// Schedules
	everyYear          = "0 0 0 1 1 *"
	everyMonth         = "0 0 0 1 * *"
	everyWeek          = "0 0 0 * * 0"
	everyDay           = "0 0 0 * * *"
	everyDayEasternTZ  = "TZ=America/New_York 0 0 * * * *"
	everyHour          = "0 0 * * * *"
	everyHourFrom9To10 = "0 9-10 * * *"
	everyMinute        = "0 * * * * *"
	everySecond        = "* * * * * *"
	everyEvenSecond    = "*/2 * * * * *"

	// Workflow IDs
	workflowID1 = "workflow-id-1"

	// Trigger IDs
	triggerID1 = "test-id-1"
	triggerID2 = "test-id-2"

	// instanceCount is how many instances most tests run, so every event is also checked for agreement.
	instanceCount = 3
)

var metadata = capabilities.RequestMetadata{WorkflowID: workflowID1}

// startCrons starts n instances of the cron trigger as capability.Run would, all scheduling on clock.
func startCrons(t *testing.T, clock clockwork.Clock, cfg Config, n int) protostest.CronInstances {
	t.Helper()

	return capabilitytest.Start(t, New, n, capability.WithProvider(func(i *capability.Injector) error {
		return errors.Join(i.ProvideValue(clock), i.ProvideValue(&cfg))
	}))
}

// trigger is one registered trigger and the events its instances agreed on.
type trigger struct {
	id     string
	input  *protos.Config
	events <-chan instances.TypedEvent[*protos.Payload]
}

func register(t *testing.T, crons protostest.CronInstances, id, schedule string) *trigger {
	t.Helper()

	input := &protos.Config{Schedule: schedule}
	events, err := crons.RegisterTrigger(t.Context(), id, metadata, input)
	require.NoError(t, err)
	return &trigger{id: id, input: input, events: events}
}

func (tr *trigger) unregister(t *testing.T, crons protostest.CronInstances) {
	t.Helper()
	require.NoError(t, crons.UnregisterTrigger(t.Context(), tr.id, metadata, tr.input))
}

// next is the next event every instance agreed on.
func (tr *trigger) next(t *testing.T) capabilities.TriggerAndId[*protos.Payload] {
	t.Helper()

	select {
	case e, ok := <-tr.events:
		require.True(t, ok, "the trigger's events ended")
		require.Nil(t, e.Mismatch)
		return e.TriggerAndId
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for an event")
		return capabilities.TriggerAndId[*protos.Payload]{}
	}
}

// scheduled is the time the next event was scheduled for.
func (tr *trigger) scheduled(t *testing.T) time.Time {
	t.Helper()
	return tr.next(t).Trigger.ScheduledExecutionTime.AsTime()
}

// requireNone fails if an event arrives soon.
func (tr *trigger) requireNone(t *testing.T) {
	t.Helper()

	select {
	case e := <-tr.events:
		t.Fatalf("unexpected event %v", e)
	case <-time.After(100 * time.Millisecond):
	}
}

// requireClosed fails unless the trigger's events end.
func (tr *trigger) requireClosed(t *testing.T) {
	t.Helper()

	select {
	case _, ok := <-tr.events:
		require.False(t, ok, "the trigger's events should have ended")
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the trigger's events to end")
	}
}

func TestCronTrigger_SuccessWithStandardCronIntervals(t *testing.T) {
	cases := []struct {
		name     string
		schedule string
		interval [4]time.Duration
	}{
		{name: "every second", schedule: everySecond, interval: [4]time.Duration{time.Second, time.Second, time.Second, time.Second}},
		{name: "every minute", schedule: everyMinute, interval: [4]time.Duration{time.Minute, time.Minute, time.Minute, time.Minute}},
		{name: "every hour", schedule: everyHour, interval: [4]time.Duration{time.Hour, time.Hour, time.Hour, time.Hour}},
		{name: "every day", schedule: everyDay, interval: [4]time.Duration{24 * time.Hour, 24 * time.Hour, 24 * time.Hour, 24 * time.Hour}},
		{name: "every week on Sunday", schedule: everyWeek, interval: [4]time.Duration{7 * 24 * time.Hour, 7 * 24 * time.Hour, 7 * 24 * time.Hour, 7 * 24 * time.Hour}},
		{name: "every month", schedule: everyMonth, interval: [4]time.Duration{31 * 24 * time.Hour, 28 * 24 * time.Hour, 31 * 24 * time.Hour, 30 * 24 * time.Hour}},
		{name: "every year", schedule: everyYear, interval: [4]time.Duration{365 * 24 * time.Hour, 365 * 24 * time.Hour, 365 * 24 * time.Hour, 365 * 24 * time.Hour}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Year 1 starts on a Monday and has no leap years for the intervals above to step over; the
			// weekly case starts on the Sunday after.
			clock := clockwork.NewFakeClockAt(time.Time{})
			if tt.schedule == everyWeek {
				clock.Advance(6 * 24 * time.Hour)
			}
			crons := startCrons(t, clock, Config{FastestScheduleIntervalSeconds: 1}, instanceCount)
			tr := register(t, crons, triggerID1, tt.schedule)

			clock.Advance(tt.interval[0] - time.Millisecond)
			tr.requireNone(t)
			clock.Advance(2 * time.Millisecond)
			first := tr.scheduled(t)

			clock.Advance(tt.interval[1])
			second := tr.scheduled(t)

			clock.Advance(tt.interval[2])
			third := tr.scheduled(t)

			tr.unregister(t, crons)
			clock.Advance(tt.interval[3])
			tr.requireClosed(t)

			assert.Equal(t, first.Add(tt.interval[1]), second)
			assert.Equal(t, second.Add(tt.interval[2]), third)
		})
	}
}

// TestCronTrigger_Deterministic is what a DON relies on: every instance fires on the same tick with the
// same event, tick after tick.
func TestCronTrigger_Deterministic(t *testing.T) {
	const ticks = 100

	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := clockwork.NewFakeClockAt(start)
	crons := startCrons(t, clock, Config{FastestScheduleIntervalSeconds: 1}, instanceCount)
	tr := register(t, crons, triggerID1, everySecond)

	for i := 1; i <= ticks; i++ {
		clock.Advance(time.Second)
		e := tr.next(t)

		want := start.Add(time.Duration(i) * time.Second)
		require.Equal(t, want, e.Trigger.ScheduledExecutionTime.AsTime(), "tick %d", i)
		require.Equal(t, want.Format(time.RFC3339), e.Id, "tick %d", i)
	}

	tr.unregister(t, crons)
	tr.requireClosed(t)
}

// TestCronTrigger_Load runs on the real clock, and on one instance since it is the scheduler under load
// being measured rather than agreement.
func TestCronTrigger_Load(t *testing.T) {
	t.Parallel()
	const numTriggers = 1_000
	const numExecutions = 3

	clock := clockwork.NewRealClock()
	crons := startCrons(t, clock, Config{FastestScheduleIntervalSeconds: 1}, 1)

	triggers := make([]*trigger, numTriggers)
	for i := range triggers {
		triggers[i] = register(t, crons, "test-id-"+strconv.Itoa(i+1), everySecond)
	}

	var (
		mu        sync.Mutex
		scheduled [numTriggers][numExecutions]time.Time
		actual    [numTriggers][numExecutions]time.Time
		wg        sync.WaitGroup
	)
	for i, tr := range triggers {
		wg.Go(func() {
			for exec := range numExecutions {
				s := tr.scheduled(t)
				now := clock.Now().UTC()

				mu.Lock()
				scheduled[i][exec] = s
				actual[i][exec] = now
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	for _, tr := range triggers {
		tr.unregister(t, crons)
	}
	for _, tr := range triggers {
		tr.requireClosed(t)
	}

	mu.Lock()
	defer mu.Unlock()
	var totalDelta time.Duration
	for exec := range numExecutions {
		earliest, latest := scheduled[0][exec], scheduled[0][exec]
		for i := range numTriggers {
			s, a := scheduled[i][exec], actual[i][exec]
			if s.Before(earliest) {
				earliest = s
			}
			if s.After(latest) {
				latest = s
			}
			if exec > 0 {
				require.Equal(t, scheduled[i][exec-1].Add(time.Second), s, "executions should be a second apart")
			}
			require.False(t, a.Before(s), "an execution should not run before it is scheduled")
			require.False(t, a.After(s.Add(time.Second)), "an execution should run within a second of when it is scheduled")
			totalDelta += a.Sub(s)
		}
		// Triggers on the same schedule stay aligned, though registration on the real clock can span a
		// second boundary under load.
		require.False(t, latest.After(earliest.Add(time.Second)))
	}
	t.Logf("Average delta: %s", totalDelta/(numTriggers*numExecutions))
}

func TestCronTrigger_RegisterTriggerBeforeStart(t *testing.T) {
	clock := clockwork.NewRealClock()
	crons := startCrons(t, clock, Config{FastestScheduleIntervalSeconds: 1}, instanceCount)
	tr := register(t, crons, triggerID1, everySecond)

	first := tr.scheduled(t)
	firstAt := clock.Now().UTC()
	second := tr.scheduled(t)
	secondAt := clock.Now().UTC()

	tr.unregister(t, crons)

	assert.Equal(t, first.Add(time.Second), second)
	for _, at := range []struct{ scheduled, actual time.Time }{{first, firstAt}, {second, secondAt}} {
		assert.True(t, at.actual.After(at.scheduled), "an execution should not run before it is scheduled")
		assert.False(t, at.actual.After(at.scheduled.Add(time.Second)), "an execution should run within a second of when it is scheduled")
	}
}

func TestCronTriggerTimeWindows(t *testing.T) {
	// 08:50 UTC
	clock := clockwork.NewFakeClockAt(time.Date(2024, 1, 1, 8, 50, 0, 0, time.UTC))
	crons := startCrons(t, clock, Config{FastestScheduleIntervalSeconds: 1}, instanceCount)
	tr := register(t, crons, triggerID1, everyHourFrom9To10)

	clock.Advance(10*time.Minute + time.Millisecond)
	nine := tr.scheduled(t)

	clock.Advance(time.Hour)
	ten := tr.scheduled(t)

	// Not again until 09:00 the next day.
	clock.Advance(23 * time.Hour)
	nextNine := tr.scheduled(t)

	tr.unregister(t, crons)

	assert.Equal(t, nine.Add(time.Hour), ten)
	assert.Equal(t, nine.Add(24*time.Hour), nextNine)
}

func TestCronTrigger_MultipleDifferentSchedules(t *testing.T) {
	// An odd second, so the even schedule first fires a second after the every-second one.
	clock := clockwork.NewFakeClockAt(time.Date(2024, 1, 1, 0, 0, 1, 0, time.UTC))
	crons := startCrons(t, clock, Config{FastestScheduleIntervalSeconds: 1}, instanceCount)
	every := register(t, crons, triggerID1, everySecond)
	even := register(t, crons, triggerID2, everyEvenSecond)

	var everyEvents, evenEvents []capabilities.TriggerAndId[*protos.Payload]
	for second := 1; second <= 4; second++ {
		clock.Advance(time.Second)
		everyEvents = append(everyEvents, every.next(t))
		if second%2 == 1 {
			evenEvents = append(evenEvents, even.next(t))
		}
	}

	every.unregister(t, crons)
	even.unregister(t, crons)
	clock.Advance(time.Second)
	every.requireClosed(t)
	even.requireClosed(t)

	for i := 1; i < len(everyEvents); i++ {
		assert.Equal(t, everyEvents[i-1].Trigger.ScheduledExecutionTime.AsTime().Add(time.Second), everyEvents[i].Trigger.ScheduledExecutionTime.AsTime())
	}
	assert.Equal(t, evenEvents[0].Trigger.ScheduledExecutionTime.AsTime().Add(2*time.Second), evenEvents[1].Trigger.ScheduledExecutionTime.AsTime())
	// The even schedule's events land on the every-second schedule's 1st and 3rd, and share their IDs.
	assert.Equal(t, everyEvents[0].Id, evenEvents[0].Id)
	assert.Equal(t, everyEvents[2].Id, evenEvents[1].Id)
}

func TestCronTriggerTimeZone(t *testing.T) {
	eastern, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	// 23:50 Eastern
	clock := clockwork.NewFakeClockAt(time.Date(2024, 1, 1, 23, 50, 0, 0, eastern))
	crons := startCrons(t, clock, Config{FastestScheduleIntervalSeconds: 1}, instanceCount)
	tr := register(t, crons, triggerID1, everyDayEasternTZ)

	clock.Advance(10*time.Minute - time.Millisecond)
	tr.requireNone(t)

	clock.Advance(time.Millisecond)
	midnight := tr.scheduled(t)

	tr.unregister(t, crons)

	assert.True(t, midnight.Equal(time.Date(2024, 1, 2, 0, 0, 0, 0, eastern)), "fired at %s", midnight)
}

func TestCronTrigger_RegisterTrigger(t *testing.T) {
	cases := []struct {
		name        string
		schedule    string
		expectedErr string
		code        caperrors.ErrorCode
	}{
		{name: "valid cron schedule - 6 entries", schedule: everyMinute},
		{name: "valid cron schedule - 5 entries", schedule: "* * * * *"},
		{
			name:        "invalid cron schedule - empty",
			expectedErr: "[3]InvalidArgument: failed to initialize job: gocron: CronJob: crontab parse failure\nexpected 5 to 6 fields, found 0: []",
		},
		{
			name:        "invalid cron schedule - not a cron schedule",
			schedule:    "d d d d d",
			expectedErr: "[3]InvalidArgument: failed to initialize job: gocron: CronJob: crontab parse failure\nfailed to parse int from d: strconv.Atoi: parsing \"d\": invalid syntax",
		},
		{
			name:        "invalid cron schedule - invalid timezone",
			schedule:    "TZ=moon * * * * *",
			expectedErr: "[3]InvalidArgument: failed to initialize job: gocron: CronJob: crontab parse failure\nprovided bad location moon: unknown time zone moon",
		},
		{
			name:        "invalid cron schedule - exceeds maximum fastest",
			schedule:    everySecond,
			expectedErr: "[101]LimitExceeded: maximum fastest cron schedule is 30s",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			crons := startCrons(t, clockwork.NewRealClock(), Config{}, instanceCount)

			_, err := crons.RegisterTrigger(t.Context(), triggerID1, metadata, &protos.Config{Schedule: tt.schedule})
			if tt.expectedErr == "" {
				require.NoError(t, err)
				return
			}

			require.EqualError(t, err, tt.expectedErr)
			var capErr caperrors.Error
			require.ErrorAs(t, err, &capErr)
			assert.Equal(t, caperrors.OriginUser, capErr.Origin())
			assert.Equal(t, caperrors.VisibilityPublic, capErr.Visibility())
		})
	}
}

func TestCronTrigger_RegisterTriggerDuplicateError(t *testing.T) {
	crons := startCrons(t, clockwork.NewRealClock(), Config{FastestScheduleIntervalSeconds: 1}, instanceCount)
	register(t, crons, triggerID1, everySecond)

	_, err := crons.RegisterTrigger(t.Context(), triggerID1, metadata, &protos.Config{Schedule: everySecond})
	require.EqualError(t, err, "[13]Internal: triggerId test-id-1 already registered")
}

func TestCronTrigger_UnregisterTriggerError(t *testing.T) {
	input := &protos.Config{Schedule: everySecond}

	t.Run("OK if trigger not found", func(t *testing.T) {
		crons := startCrons(t, clockwork.NewRealClock(), Config{FastestScheduleIntervalSeconds: 1}, instanceCount)
		require.NoError(t, crons.UnregisterTrigger(t.Context(), "missing", metadata, input))
	})

	t.Run("OK register then unregister multiple times", func(t *testing.T) {
		crons := startCrons(t, clockwork.NewRealClock(), Config{FastestScheduleIntervalSeconds: 1}, instanceCount)
		tr := register(t, crons, triggerID1, everySecond)
		for range 3 {
			tr.unregister(t, crons)
		}
	})

	t.Run("NOK fails to unregister if closed", func(t *testing.T) {
		crons := startCrons(t, clockwork.NewRealClock(), Config{FastestScheduleIntervalSeconds: 1}, instanceCount)
		register(t, crons, triggerID1, everySecond)
		for _, c := range crons {
			require.NoError(t, c.Close())
		}

		require.ErrorContains(t, crons.UnregisterTrigger(t.Context(), triggerID1, metadata, input),
			"cannot unregister a new trigger, service has been closed")
	})
}
