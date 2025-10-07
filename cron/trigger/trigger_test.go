package trigger

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/triggers/cron"
	crontypedapi "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/cron"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/cron/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services/orgresolver"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"
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
)

type TriggerCapability interface {
	RegisterTrigger(ctx context.Context, request capabilities.TriggerRegistrationRequest) (<-chan capabilities.TriggerResponse, error)
	UnregisterTrigger(ctx context.Context, request capabilities.TriggerRegistrationRequest) error
}

func registerTriggerToCronTriggerService(
	ctx context.Context,
	t *testing.T,
	cts TriggerCapability,
	schedule string,
	triggerID string,
	useTypedAPI bool,
) (
	<-chan capabilities.TriggerResponse,
	capabilities.TriggerRegistrationRequest,
	error,
) {
	requestTriggerID := workflowID1 + "|" + triggerID // TODO: remove wid once added by workflow engine
	requestMetadata := capabilities.RequestMetadata{
		WorkflowID: workflowID1,
	}

	if useTypedAPI {
		payload, err := anypb.New(&crontypedapi.Config{Schedule: schedule})
		require.NoError(t, err)

		request := capabilities.TriggerRegistrationRequest{
			TriggerID: requestTriggerID,
			Metadata:  requestMetadata,
			Payload:   payload,
			Method:    "",
		}
		triggerEventsCh, err := cts.RegisterTrigger(ctx, request)

		return triggerEventsCh, request, err
	}

	config, err := values.NewMap(map[string]interface{}{
		"schedule": schedule,
	})
	require.NoError(t, err)

	request := capabilities.TriggerRegistrationRequest{
		TriggerID: requestTriggerID,
		Metadata:  requestMetadata,
		Config:    config,
	}
	triggerEventsCh, err := cts.RegisterTrigger(ctx, request)

	return triggerEventsCh, request, err
}

func upwrapCronTriggerEvent(t *testing.T, event capabilities.TriggerEvent,
	useTypedAPI bool) Response {
	response := Response{}
	response.TriggerType = event.TriggerType
	assert.Equal(t, server.CronID, response.TriggerType)
	response.ID = event.ID

	if useTypedAPI {
		payload := &crontypedapi.LegacyPayload{} //nolint:staticcheck
		err := event.Payload.UnmarshalTo(payload)
		require.NoError(t, err)
		response.Payload = cron.Payload{ScheduledExecutionTime: payload.ScheduledExecutionTime}
		return response
	}

	err := event.Outputs.UnwrapTo(&response.Payload)
	require.NoError(t, err)
	require.NotNil(t, response.Payload.ScheduledExecutionTime)
	return response
}

func makeTriggerID(number int) string {
	// avoid conversion overflow for negative numbers
	if number < 0 {
		fmt.Printf("Trigger ID cannot be negative: %d", number)
		return ""
	}
	return "test-id-" + strconv.FormatUint(uint64(number), 10)
}

func requireNoChanMsg[T any](t *testing.T, ch <-chan T) {
	timedOut := false
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
		timedOut = true
	}
	require.True(t, timedOut)
}

func TestCronTrigger_SuccessWithStandardCronIntervals_UntypedAPI(t *testing.T) {
	successWithStandardCronIntervals(t, false)
}

func TestCronTrigger_SuccessWithStandardCronIntervals_TypedAPI(t *testing.T) {
	successWithStandardCronIntervals(t, true)
}

func successWithStandardCronIntervals(t *testing.T, useTypedAPI bool) {
	cases := []struct {
		name     string
		schedule string
		interval [5]time.Duration
	}{
		{
			name:     "success - every second",
			schedule: everySecond,
			interval: [5]time.Duration{
				time.Second,
				time.Second,
				time.Second,
				time.Second,
				time.Second,
			},
		},
		{
			name:     "success - every minute",
			schedule: everyMinute,
			interval: [5]time.Duration{
				time.Minute,
				time.Minute,
				time.Minute,
				time.Minute,
				time.Minute,
			},
		},
		{
			name:     "success - every hour",
			schedule: everyHour,
			interval: [5]time.Duration{
				time.Hour,
				time.Hour,
				time.Hour,
				time.Hour,
				time.Hour,
			},
		},
		{
			name:     "success - every day",
			schedule: everyDay,
			interval: [5]time.Duration{
				24 * time.Hour,
				24 * time.Hour,
				24 * time.Hour,
				24 * time.Hour,
				24 * time.Hour,
			},
		},
		{
			name:     "success - every week on Sunday",
			schedule: everyWeek,
			interval: [5]time.Duration{
				(time.Hour * 24) * 7,
				(time.Hour * 24) * 7,
				(time.Hour * 24) * 7,
				(time.Hour * 24) * 7,
				(time.Hour * 24) * 7,
			},
		},
		{
			name:     "success - every month",
			schedule: everyMonth,
			interval: [5]time.Duration{
				(time.Hour * 24) * 31,
				(time.Hour * 24) * 28,
				(time.Hour * 24) * 31,
				(time.Hour * 24) * 30,
				(time.Hour * 24) * 31,
			},
		},
		{
			name:     "success - every year",
			schedule: everyYear,
			interval: [5]time.Duration{
				(time.Hour * 24) * 365,
				(time.Hour * 24) * 365,
				(time.Hour * 24) * 365,
				(time.Hour * 24) * 365,
				(time.Hour * 24) * 365,
			},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			startTime, _ := time.Parse("2006-01-01", "22-01-01")
			fakeClock := clockwork.NewFakeClockAt(startTime)
			if tt.schedule == everyWeek {
				// If every week set to Saturday
				for fakeClock.Now().UTC().Weekday() != time.Sunday {
					fakeClock.Advance(24 * time.Hour)
				}
			}

			config, err := json.Marshal(Config{FastestScheduleIntervalSeconds: 1})
			require.NoError(t, err)

			ts, err := NewTriggerService(logger.Nop(), fakeClock, nil)
			require.NoError(t, err)
			err = ts.Initialise(t.Context(), core.StandardCapabilitiesDependencies{
				Config: string(config),
			})
			require.NoError(t, err)

			triggerAPI := server.NewCronServer(ts)

			// Register trigger
			callback, registerUnregisterRequest, err := registerTriggerToCronTriggerService(
				t.Context(),
				t,
				triggerAPI,
				tt.schedule,
				makeTriggerID(1),
				useTypedAPI,
			)
			require.NoError(t, err)
			assert.Equal(t, len(ts.scheduler.Jobs()), 1)

			// Advance to 1ms before scheduled time, there should be no channel message
			fakeClock.Advance(tt.interval[0] - time.Millisecond)
			requireNoChanMsg(t, callback)
			// Pass scheduled time by 1ms
			fakeClock.Advance(2 * time.Millisecond)

			// 1st process
			msg := <-callback
			response := upwrapCronTriggerEvent(t, msg.Event, useTypedAPI)
			scheduledExecutionTime1, _ := time.Parse(time.RFC3339, response.Payload.ScheduledExecutionTime)

			fakeClock.Advance(tt.interval[1])

			// 2nd process
			msg = <-callback
			response = upwrapCronTriggerEvent(t, msg.Event, useTypedAPI)
			scheduledExecutionTime2, _ := time.Parse(time.RFC3339, response.Payload.ScheduledExecutionTime)

			fakeClock.Advance(tt.interval[2])

			// 3rd process
			msg = <-callback
			response = upwrapCronTriggerEvent(t, msg.Event, useTypedAPI)
			scheduledExecutionTime3, _ := time.Parse(time.RFC3339, response.Payload.ScheduledExecutionTime)

			// Unregister the trigger and check that events no longer go on the callback
			require.NoError(t, triggerAPI.UnregisterTrigger(t.Context(), registerUnregisterRequest))
			assert.Equal(t, len(ts.scheduler.Jobs()), 0)
			assert.Equal(t, ts.scheduler.JobsWaitingInQueue(), 0)

			// Skip to when the next execution would be
			fakeClock.Advance(tt.interval[3])

			// One interval after unregistering, channel should be closed
			_, open := <-callback
			require.False(t, open)

			// Close the service
			require.NoError(t, ts.Close())

			// Check scheduled execution times are every interval
			require.True(t, scheduledExecutionTime3.Equal(scheduledExecutionTime2.Add(tt.interval[2])))
			require.True(t, scheduledExecutionTime3.Equal(scheduledExecutionTime1.Add(tt.interval[1]+tt.interval[2])))
			require.True(t, scheduledExecutionTime2.Equal(scheduledExecutionTime1.Add(tt.interval[1])))
		})
	}
}

func TestCronTrigger_Load(t *testing.T) {
	const numTriggers = 1_000
	const numExecutions = 3
	require.Greater(t, numTriggers, 0)
	require.Greater(t, numExecutions, 0)

	fakeClock := clockwork.NewRealClock()

	config, err := json.Marshal(Config{FastestScheduleIntervalSeconds: 1})
	require.NoError(t, err)

	ts, err := NewTriggerService(logger.Nop(), fakeClock, nil)
	require.NoError(t, err)

	triggerAPI := server.NewCronServer(ts)

	ctx := t.Context()

	var callbacks [numTriggers]<-chan capabilities.TriggerResponse
	var unregisterRequests [numTriggers]capabilities.TriggerRegistrationRequest

	// Register triggers
	for triggerIdx := 0; triggerIdx < numTriggers; triggerIdx++ {
		callback, unregisterRequest, err := registerTriggerToCronTriggerService(
			ctx,
			t,
			triggerAPI,
			everySecond,
			makeTriggerID(triggerIdx+1),
			false,
		)
		require.NoError(t, err)
		callbacks[triggerIdx] = callback
		unregisterRequests[triggerIdx] = unregisterRequest
	}
	assert.Equal(t, len(ts.scheduler.Jobs()), numTriggers)

	// Start scheduling
	err = ts.Initialise(t.Context(), core.StandardCapabilitiesDependencies{
		Config: string(config),
	})
	require.NoError(t, err)

	// Process "numExecutions" times
	var timestamps [numTriggers][numExecutions]time.Time
	var scheduledExecTimes [numTriggers][numExecutions]time.Time
	var mu sync.Mutex // Mutex to ensure memory is synced across threads

	wg := sync.WaitGroup{}

	for triggerIdx := 0; triggerIdx < numTriggers; triggerIdx++ {
		wg.Add(1)
		go func(tIdx int) {
			for execIdx := 0; execIdx < numExecutions; execIdx++ {
				msg := <-callbacks[tIdx]
				response := upwrapCronTriggerEvent(t, msg.Event, false)
				scheduledExecutionTime, _ := time.Parse(time.RFC3339Nano, response.Payload.ScheduledExecutionTime)

				actualExecutionTimeUTC := fakeClock.Now().UTC()

				mu.Lock()
				scheduledExecTimes[tIdx][execIdx] = scheduledExecutionTime
				timestamps[tIdx][execIdx] = actualExecutionTimeUTC
				mu.Unlock()
			}
			wg.Done()
		}(triggerIdx)
	}

	wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	// Unregister the trigger and check that events no longer go on the callback
	for i := 0; i < numTriggers; i++ {
		require.NoError(t, triggerAPI.UnregisterTrigger(ctx, unregisterRequests[i]))
	}

	assert.Equal(t, len(ts.scheduler.Jobs()), 0)
	assert.Equal(t, ts.scheduler.JobsWaitingInQueue(), 0)

	// Wait a second to ensure no more events
	time.Sleep(time.Second * 5)
	for i := 0; i < numTriggers; i++ {
		_, open := <-callbacks[i]
		require.False(t, open)
	}

	// Close the service
	require.NoError(t, ts.Close())

	var scheduledActualDelta [numTriggers * numExecutions]int64

	for execIdx := 0; execIdx < numExecutions; execIdx++ {
		for triggerIdx := 0; triggerIdx < numTriggers; triggerIdx++ {
			// Check all scheduled execution times at each process are the same across all triggers
			if triggerIdx > 0 {
				require.True(t, scheduledExecTimes[0][execIdx].Equal(scheduledExecTimes[triggerIdx][execIdx]))
			}
			// Check that executions happened every second
			if execIdx > 0 {
				require.True(t, scheduledExecTimes[triggerIdx][execIdx].Equal(scheduledExecTimes[triggerIdx][execIdx-1].Add(time.Second)))
			}
			// Check that actual execution time is after scheduled time

			after := timestamps[triggerIdx][execIdx].After(scheduledExecTimes[triggerIdx][execIdx]) || timestamps[triggerIdx][execIdx].Equal(scheduledExecTimes[triggerIdx][execIdx])

			require.True(t, after)

			// Check that scheduled time and actual time did not differ more than 1 second
			require.False(t, timestamps[triggerIdx][execIdx].After(scheduledExecTimes[triggerIdx][execIdx].Add(time.Second)))
			// Store time difference between scheduled and actual
			scheduledActualDelta[triggerIdx*numExecutions+execIdx] = timestamps[triggerIdx][execIdx].Sub(scheduledExecTimes[triggerIdx][execIdx]).Milliseconds()
		}
	}

	var averageDelta int64
	for _, num := range scheduledActualDelta {
		averageDelta += num
	}
	averageDelta = averageDelta / int64(len(scheduledActualDelta))
	fmt.Println("Average Delta: ", averageDelta, "ms")
}

func TestCronTrigger_RegisterTriggerBeforeStart_TypedAPI(t *testing.T) {
	testCronTriggerRegisterTriggerBeforeStart(t, true)
}

func TestCronTrigger_RegisterTriggerBeforeStart_UntypedAPI(t *testing.T) {
	testCronTriggerRegisterTriggerBeforeStart(t, false)
}

func testCronTriggerRegisterTriggerBeforeStart(t *testing.T, useTypedAPI bool) {
	fakeClock := clockwork.NewRealClock()
	config, err := json.Marshal(Config{FastestScheduleIntervalSeconds: 1})
	require.NoError(t, err)
	ts, err := NewTriggerService(logger.Nop(), fakeClock, nil)
	require.NoError(t, err)

	triggerAPI := server.NewCronServer(ts)

	ctx := t.Context()

	// Register trigger
	callback, registerUnregisterRequest, err := registerTriggerToCronTriggerService(
		ctx,
		t,
		triggerAPI,
		everySecond,
		makeTriggerID(1),
		useTypedAPI,
	)
	require.NoError(t, err)
	assert.Equal(t, len(ts.scheduler.Jobs()), 1)

	// Start scheduling
	err = ts.Initialise(t.Context(), core.StandardCapabilitiesDependencies{
		Config: string(config),
	})
	require.NoError(t, err)

	// 1st process
	msg := <-callback
	actualExecutionTime1 := fakeClock.Now().UTC()
	response := upwrapCronTriggerEvent(t, msg.Event, useTypedAPI)
	scheduledExecutionTime1, _ := time.Parse(time.RFC3339, response.Payload.ScheduledExecutionTime)

	// 2nd process
	msg = <-callback
	actualExecutionTime2 := fakeClock.Now().UTC()
	response = upwrapCronTriggerEvent(t, msg.Event, useTypedAPI)
	scheduledExecutionTime2, _ := time.Parse(time.RFC3339, response.Payload.ScheduledExecutionTime)

	// Unregister the trigger and check that events no longer go on the callback
	require.NoError(t, triggerAPI.UnregisterTrigger(ctx, registerUnregisterRequest))
	assert.Equal(t, len(ts.scheduler.Jobs()), 0)
	assert.Equal(t, ts.scheduler.JobsWaitingInQueue(), 0)

	// Close the service
	require.NoError(t, ts.Close())

	// Check that executions happened every second
	require.True(t, scheduledExecutionTime2.Equal(scheduledExecutionTime1.Add(time.Second)))
	// Check that actual execution time is after scheduled time
	require.True(t, actualExecutionTime1.After(scheduledExecutionTime1))
	require.True(t, actualExecutionTime2.After(scheduledExecutionTime2))
	// Check that scheduled time and actual time did not differ more than 1 second
	require.False(t, actualExecutionTime1.After(scheduledExecutionTime1.Add(time.Second)))
	require.False(t, actualExecutionTime2.After(scheduledExecutionTime2.Add(time.Second)))
}

func TestCronTriggerTimeWindows_UntypedAPI(t *testing.T) {
	testCronTriggerTimeWindows(t, false)
}

func TestCronTriggerTimeWindows_TypedAPI(t *testing.T) {
	testCronTriggerTimeWindows(t, true)
}

func testCronTriggerTimeWindows(t *testing.T, useTypedAPI bool) {
	fakeClock := clockwork.NewFakeClock()
	// Set time to have 0ms by advancing to next truncated second
	fakeClock.Advance(fakeClock.Now().Truncate(time.Second).Add(time.Second).Sub(fakeClock.Now()))
	// Set time to 8:50am UTC
	hour, minimum, sec := fakeClock.Now().UTC().Clock()
	fakeClock.Advance(time.Duration(60-sec) * time.Second)
	fakeClock.Advance(time.Duration(49-minimum) * time.Minute)
	fakeClock.Advance(time.Duration(8-hour) * time.Hour)

	config, err := json.Marshal(Config{FastestScheduleIntervalSeconds: 1})
	require.NoError(t, err)
	ts, err := NewTriggerService(logger.Nop(), fakeClock, nil)
	require.NoError(t, err)
	triggerAPI := server.NewCronServer(ts)

	ctx := t.Context()

	// Register trigger
	callback, registerUnregisterRequest, err := registerTriggerToCronTriggerService(
		ctx,
		t,
		triggerAPI,
		everyHourFrom9To10,
		makeTriggerID(1),
		useTypedAPI,
	)
	require.NoError(t, err)
	assert.Equal(t, len(ts.scheduler.Jobs()), 1)

	// Start scheduling
	err = ts.Initialise(t.Context(), core.StandardCapabilitiesDependencies{
		Config: string(config),
	})
	require.NoError(t, err)

	// Advance to 1ms past 9am
	fakeClock.Advance(10*time.Minute + time.Millisecond)

	// 1st process @ 9am UTC
	msg := <-callback
	response := upwrapCronTriggerEvent(t, msg.Event, useTypedAPI)
	scheduledExecutionTime1, _ := time.Parse(time.RFC3339, response.Payload.ScheduledExecutionTime)

	// Advance to 10am
	fakeClock.Advance(time.Hour)

	// 2nd process @ 10am UTC
	msg = <-callback
	response = upwrapCronTriggerEvent(t, msg.Event, useTypedAPI)
	scheduledExecutionTime2, _ := time.Parse(time.RFC3339, response.Payload.ScheduledExecutionTime)

	// Advance to 9am UTC next day
	fakeClock.Advance(time.Hour * 23)

	// should not process again until next day
	msg = <-callback
	response = upwrapCronTriggerEvent(t, msg.Event, useTypedAPI)
	scheduledExecutionTime3, _ := time.Parse(time.RFC3339, response.Payload.ScheduledExecutionTime)

	// Unregister the trigger and check that events no longer go on the callback
	require.NoError(t, triggerAPI.UnregisterTrigger(ctx, registerUnregisterRequest))
	assert.Equal(t, len(ts.scheduler.Jobs()), 0)
	assert.Equal(t, ts.scheduler.JobsWaitingInQueue(), 0)

	// Close the service
	require.NoError(t, ts.Close())

	// Check scheduled execution times 9am, 10am, then 9am the next day
	require.True(t, scheduledExecutionTime3.Equal(scheduledExecutionTime2.Add(23*time.Hour)))
	require.True(t, scheduledExecutionTime3.Equal(scheduledExecutionTime1.Add(24*time.Hour)))
	require.True(t, scheduledExecutionTime2.Equal(scheduledExecutionTime1.Add(time.Hour)))
}

func TestCronTrigger_MultipleDifferentSchedules_UntypedAPI(t *testing.T) {
	testCronTriggerMultipleDifferentSchedules(t, false)
}

func TestCronTrigger_MultipleDifferentSchedules_TypedAPI(t *testing.T) {
	testCronTriggerMultipleDifferentSchedules(t, true)
}

func testCronTriggerMultipleDifferentSchedules(t *testing.T, useTypedAPI bool) {
	fakeClock := clockwork.NewFakeClock()
	// Start on an odd numbered second
	if fakeClock.Now().Second()%2 == 1 {
		fakeClock.Advance(time.Second)
	}
	config, err := json.Marshal(Config{FastestScheduleIntervalSeconds: 1})
	require.NoError(t, err)
	ts, err := NewTriggerService(logger.Nop(), fakeClock, nil)
	require.NoError(t, err)
	triggerAPI := server.NewCronServer(ts)
	ctx := t.Context()

	callback1, registerUnregisterRequest1, err := registerTriggerToCronTriggerService(
		ctx,
		t,
		triggerAPI,
		everySecond,
		triggerID1,
		useTypedAPI,
	)
	require.NoError(t, err)

	callback2, registerUnregisterRequest2, err := registerTriggerToCronTriggerService(
		ctx,
		t,
		triggerAPI,
		everyEvenSecond,
		triggerID2,
		useTypedAPI,
	)
	require.NoError(t, err)

	assert.Equal(t, len(ts.scheduler.Jobs()), 2)

	// Start scheduling
	err = ts.Initialise(t.Context(), core.StandardCapabilitiesDependencies{
		Config: string(config),
	})
	require.NoError(t, err)

	fakeClock.Advance(time.Second)

	// 1st second
	msg1 := <-callback1
	response1 := upwrapCronTriggerEvent(t, msg1.Event, useTypedAPI)
	scheduledExecutionTime1_1, _ := time.Parse(time.RFC3339, response1.Payload.ScheduledExecutionTime)

	fakeClock.Advance(time.Second)

	// 2nd second
	msg1 = <-callback1
	response1 = upwrapCronTriggerEvent(t, msg1.Event, useTypedAPI)
	scheduledExecutionTime1_2, _ := time.Parse(time.RFC3339, response1.Payload.ScheduledExecutionTime)
	eventID1Run2 := response1.ID

	msg2 := <-callback2
	response2 := upwrapCronTriggerEvent(t, msg2.Event, useTypedAPI)
	scheduledExecutionTime2_1, _ := time.Parse(time.RFC3339, response2.Payload.ScheduledExecutionTime)
	eventID2Run2 := response2.ID

	fakeClock.Advance(time.Second)

	// 3rd second
	msg1 = <-callback1
	response1 = upwrapCronTriggerEvent(t, msg1.Event, useTypedAPI)
	scheduledExecutionTime1_3, _ := time.Parse(time.RFC3339, response1.Payload.ScheduledExecutionTime)

	fakeClock.Advance(time.Second)

	// 4th second
	msg1 = <-callback1
	response1 = upwrapCronTriggerEvent(t, msg1.Event, useTypedAPI)
	scheduledExecutionTime1_4, _ := time.Parse(time.RFC3339, response1.Payload.ScheduledExecutionTime)
	eventID1Run4 := response1.ID

	msg2 = <-callback2
	response2 = upwrapCronTriggerEvent(t, msg2.Event, useTypedAPI)
	scheduledExecutionTime2_2, _ := time.Parse(time.RFC3339, response2.Payload.ScheduledExecutionTime)
	eventID2Run4 := response2.ID

	// Unregister the trigger and check that events no longer go on the callback
	require.NoError(t, triggerAPI.UnregisterTrigger(ctx, registerUnregisterRequest1))
	require.NoError(t, triggerAPI.UnregisterTrigger(ctx, registerUnregisterRequest2))

	fakeClock.Advance(time.Second)

	_, open := <-callback1
	require.False(t, open)

	_, open = <-callback2
	require.False(t, open)

	// Close the service
	require.NoError(t, ts.Close())

	// Check scheduled execution times
	// Trigger 1 happened every second
	require.True(t, scheduledExecutionTime1_4.Equal(scheduledExecutionTime1_3.Add(time.Second)))
	require.True(t, scheduledExecutionTime1_4.Equal(scheduledExecutionTime1_2.Add(time.Second*2)))
	require.True(t, scheduledExecutionTime1_4.Equal(scheduledExecutionTime1_1.Add(time.Second*3)))
	require.True(t, scheduledExecutionTime1_3.Equal(scheduledExecutionTime1_2.Add(time.Second)))
	require.True(t, scheduledExecutionTime1_3.Equal(scheduledExecutionTime1_1.Add(time.Second*2)))
	require.True(t, scheduledExecutionTime1_2.Equal(scheduledExecutionTime1_1.Add(time.Second)))
	// Trigger 2 happened every second second
	require.True(t, scheduledExecutionTime2_2.Equal(scheduledExecutionTime2_1.Add(time.Second*2)))
	// The 2nd and 4th second have the same event ID
	require.Equal(t, eventID1Run2, eventID2Run2)
	require.Equal(t, eventID1Run4, eventID2Run4)
}

func TestCronTriggerTimeZone_UntypedAPI(t *testing.T) {
	testCronTriggerTimeZone(t, false)
}

func TestCronTriggerTimeZone_TypedAPI(t *testing.T) {
	testCronTriggerTimeZone(t, true)
}

func testCronTriggerTimeZone(t *testing.T, useTypedAPI bool) {
	fakeClock := clockwork.NewFakeClock()
	location, _ := time.LoadLocation("America/New_York")
	// Set time to have 0ms by advancing to next truncated second
	fakeClock.Advance(time.Duration(1000000000 - fakeClock.Now().Nanosecond()))
	// Set time to 23:50pm Eastern
	now := fakeClock.Now().In(location)
	hour, minimum, sec := now.Clock()
	fakeClock.Advance(time.Duration(60-sec) * time.Second)
	fakeClock.Advance(time.Duration(49-minimum) * time.Minute)
	fakeClock.Advance(time.Duration(23-hour) * time.Hour)

	config, err := json.Marshal(Config{FastestScheduleIntervalSeconds: 1})
	require.NoError(t, err)
	ts, err := NewTriggerService(logger.Nop(), fakeClock, nil)
	require.NoError(t, err)
	triggerAPI := server.NewCronServer(ts)
	ctx := t.Context()

	// Register trigger
	callback, registerUnregisterRequest, err := registerTriggerToCronTriggerService(
		ctx,
		t,
		triggerAPI,
		everyDayEasternTZ,
		makeTriggerID(1),
		useTypedAPI,
	)
	require.NoError(t, err)
	assert.Equal(t, len(ts.scheduler.Jobs()), 1)

	// Start scheduling
	err = ts.Initialise(t.Context(), core.StandardCapabilitiesDependencies{
		Config: string(config),
	})
	require.NoError(t, err)

	// Advance to 1ms before trigger
	fakeClock.Advance(9*time.Minute + 59*time.Second + 999*time.Millisecond)

	// There should be no channel message
	requireNoChanMsg(t, callback)

	// Advance to next 12am Eastern
	fakeClock.Advance(time.Millisecond)

	// 1st process @ 12am Eastern
	msg := <-callback
	response := upwrapCronTriggerEvent(t, msg.Event, useTypedAPI)
	scheduledExecutionTime, _ := time.Parse(time.RFC3339, response.Payload.ScheduledExecutionTime)

	// Unregister the trigger and check that events no longer go on the callback
	require.NoError(t, triggerAPI.UnregisterTrigger(ctx, registerUnregisterRequest))
	assert.Equal(t, len(ts.scheduler.Jobs()), 0)
	assert.Equal(t, ts.scheduler.JobsWaitingInQueue(), 0)

	// Close the service
	require.NoError(t, ts.Close())

	// Check scheduled execution is at 12am Eastern
	timezone, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	expectedEasternExecution := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, timezone)
	require.True(t, scheduledExecutionTime.Equal(expectedEasternExecution))
}

func TestCronTrigger_RegisterTrigger_UntypedAPI(t *testing.T) {
	testCronTriggerRegisterTrigger(t, false)
}

func TestCronTrigger_RegisterTrigger_TypedAPI(t *testing.T) {
	testCronTriggerRegisterTrigger(t, true)
}

func testCronTriggerRegisterTrigger(t *testing.T, useTypedAPI bool) {
	cases := []struct {
		name              string
		schedule          string
		shouldErr         bool
		expectedErrString string
	}{
		// No Error
		{
			name:              "valid cron schedule - 6 entries",
			schedule:          everyMinute,
			shouldErr:         false,
			expectedErrString: "",
		},
		{
			name:              "valid cron schedule - 5 entries",
			schedule:          "* * * * *",
			shouldErr:         false,
			expectedErrString: "",
		},

		// Error
		{
			name:              "invalid cron schedule - empty",
			schedule:          "",
			shouldErr:         true,
			expectedErrString: "gocron: CronJob: crontab parse failure\nexpected 5 to 6 fields, found 0: []",
		},
		{
			name:              "invalid cron schedule - not a cron schedule",
			schedule:          "d d d d d",
			shouldErr:         true,
			expectedErrString: "gocron: CronJob: crontab parse failure\nfailed to parse int from d: strconv.Atoi: parsing \"d\": invalid syntax",
		},
		{
			name:              "invalid cron schedule - invalid timezone",
			schedule:          "TZ=moon * * * * *",
			shouldErr:         true,
			expectedErrString: "gocron: CronJob: crontab parse failure\nprovided bad location moon: unknown time zone moon",
		},
		{
			name:              "invalid cron schedule - exceeds maximum fastest",
			schedule:          everySecond,
			shouldErr:         true,
			expectedErrString: "maximum fastest cron schedule is 30s",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			fakeClock := clockwork.NewRealClock()
			ts, err := NewTriggerService(logger.Nop(), fakeClock, nil)
			require.NoError(t, err)
			err = ts.Initialise(t.Context(), core.StandardCapabilitiesDependencies{})
			require.NoError(t, err)
			triggerAPI := server.NewCronServer(ts)
			ctx := t.Context()
			_, _, err = registerTriggerToCronTriggerService(
				ctx,
				t,
				triggerAPI,
				tt.schedule,
				triggerID1,
				useTypedAPI,
			)

			if tt.shouldErr {
				require.Error(t, err)
				if tt.expectedErrString != "" {
					require.Equal(t, tt.expectedErrString, err.Error())
				}
			} else {
				require.NoError(t, err)
			}

			require.NoError(t, ts.Close())
		})
	}
}

func TestCronTrigger_RegisterTriggerDuplicateError(t *testing.T) {
	triggerConfig, err := json.Marshal(Config{FastestScheduleIntervalSeconds: 1})
	require.NoError(t, err)
	fakeClock := clockwork.NewRealClock()
	ts, err := NewTriggerService(logger.Nop(), fakeClock, nil)
	require.NoError(t, err)
	err = ts.Initialise(t.Context(), core.StandardCapabilitiesDependencies{
		Config: string(triggerConfig),
	})
	require.NoError(t, err)
	triggerAPI := server.NewCronServer(ts)

	ctx := t.Context()

	config, err := values.NewMap(map[string]interface{}{
		"schedule": everySecond,
	})
	require.NoError(t, err)

	requestMetadata := capabilities.RequestMetadata{
		WorkflowID: workflowID1,
	}
	request := capabilities.TriggerRegistrationRequest{
		TriggerID: triggerID1,
		Metadata:  requestMetadata,
		Config:    config,
	}

	_, err = triggerAPI.RegisterTrigger(ctx, request)
	require.NoError(t, err)
	_, err = triggerAPI.RegisterTrigger(ctx, request)
	require.Error(t, err)
	require.Equal(t, "triggerId test-id-1 already registered", err.Error())
}

func TestCronTrigger_UnregisterTriggerError(t *testing.T) {
	triggerConfig, err := json.Marshal(Config{FastestScheduleIntervalSeconds: 1})
	require.NoError(t, err)
	fakeClock := clockwork.NewRealClock()
	ts, err := NewTriggerService(logger.Nop(), fakeClock, nil)
	require.NoError(t, err)
	err = ts.Initialise(t.Context(), core.StandardCapabilitiesDependencies{
		Config: string(triggerConfig),
	})
	require.NoError(t, err)
	triggerAPI := server.NewCronServer(ts)

	t.Run("OK if trigger not found", func(t *testing.T) {
		ctx := t.Context()

		config, err := values.NewMap(map[string]interface{}{
			"schedule": everySecond,
		})
		require.NoError(t, err)

		requestMetadata := capabilities.RequestMetadata{
			WorkflowID: workflowID1,
		}
		request := capabilities.TriggerRegistrationRequest{
			TriggerID: "missing",
			Metadata:  requestMetadata,
			Config:    config,
		}

		err = triggerAPI.UnregisterTrigger(ctx, request)
		require.NoError(t, err)
	})

	t.Run("OK register then unregister", func(t *testing.T) {
		ctx := t.Context()

		config, err := values.NewMap(map[string]interface{}{
			"schedule": everySecond,
		})
		require.NoError(t, err)

		requestMetadata := capabilities.RequestMetadata{
			WorkflowID: workflowID1,
		}
		request := capabilities.TriggerRegistrationRequest{
			TriggerID: "trigger-id",
			Metadata:  requestMetadata,
			Config:    config,
		}

		_, err = triggerAPI.RegisterTrigger(ctx, request)
		require.NoError(t, err)
		err = triggerAPI.UnregisterTrigger(ctx, request)
		require.NoError(t, err)
	})

	t.Run("OK register then unregister multiple times", func(t *testing.T) {
		ctx := t.Context()

		config, err := values.NewMap(map[string]interface{}{
			"schedule": everySecond,
		})
		require.NoError(t, err)

		requestMetadata := capabilities.RequestMetadata{
			WorkflowID: workflowID1,
		}
		request := capabilities.TriggerRegistrationRequest{
			TriggerID: "trigger-id",
			Metadata:  requestMetadata,
			Config:    config,
		}

		_, err = triggerAPI.RegisterTrigger(ctx, request)
		require.NoError(t, err)

		for range 3 {
			err = triggerAPI.UnregisterTrigger(ctx, request)
			require.NoError(t, err)
		}
	})

	t.Run("NOK fails to unregister if closed", func(t *testing.T) {
		ts, err := NewTriggerService(logger.Nop(), fakeClock, nil)
		require.NoError(t, err)
		err = ts.Initialise(t.Context(), core.StandardCapabilitiesDependencies{
			Config: string(triggerConfig),
		})
		require.NoError(t, err)

		triggerAPI := server.NewCronServer(ts)
		ctx := t.Context()

		config, err := values.NewMap(map[string]interface{}{
			"schedule": everySecond,
		})
		require.NoError(t, err)

		requestMetadata := capabilities.RequestMetadata{
			WorkflowID: workflowID1,
		}
		request := capabilities.TriggerRegistrationRequest{
			TriggerID: "trigger-id",
			Metadata:  requestMetadata,
			Config:    config,
		}

		_, err = triggerAPI.RegisterTrigger(ctx, request)
		require.NoError(t, err)

		err = triggerAPI.Close()
		require.NoError(t, err)

		err = triggerAPI.UnregisterTrigger(ctx, request)
		require.Error(t, err)
		require.ErrorContains(t, err, "cannot unregister a new trigger, service has been closed")
	})
}

func TestCronTrigger_CloseStartErrors(t *testing.T) {
	fakeClock := clockwork.NewRealClock()
	ts, err := NewTriggerService(logger.Nop(), fakeClock, nil)
	require.NoError(t, err)
	ctx := t.Context()

	err = ts.Start(ctx)
	require.NoError(t, err)
	err = ts.Close()
	require.NoError(t, err)
	err = ts.Start(ctx)
	require.Error(t, err)
}

type PanicingClock struct {
	*clockwork.FakeClock
	mu             sync.Mutex
	callCount      int
	panicAfterCall int // Panic after this many calls to a specific method
}

func (p *PanicingClock) Now() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callCount++
	if p.callCount == p.panicAfterCall {
		panic("clock panic in Now()")
	}
	return p.FakeClock.Now()
}

func TestGocronNewTaskPanic(t *testing.T) {
	panicClock := &PanicingClock{
		FakeClock:      clockwork.NewFakeClock(),
		panicAfterCall: 15, // Adjust based on actual call pattern, we want to panic on cron/trigger/trigger.go:201 call to Now()
	}

	config, err := json.Marshal(Config{FastestScheduleIntervalSeconds: 1})
	require.NoError(t, err)
	logger, observedLogs := logger.TestObserved(t, zap.ErrorLevel)
	ts, err := NewTriggerService(logger, panicClock, nil)
	require.NoError(t, err)

	triggerAPI := server.NewCronServer(ts)

	_, _, err = registerTriggerToCronTriggerService(
		t.Context(),
		t,
		triggerAPI,
		everySecond,
		makeTriggerID(1),
		true,
	)
	require.NoError(t, err)
	assert.Equal(t, len(ts.scheduler.Jobs()), 1)

	// Start scheduling
	err = ts.Initialise(t.Context(), core.StandardCapabilitiesDependencies{
		Config: string(config),
	})
	require.NoError(t, err)

	panicClock.Advance(time.Second * 1)

	ticker := time.NewTicker(time.Second * 1)
	timeout := time.NewTimer(time.Second * 10)
	for {
		select {
		case <-ticker.C:
			logs := observedLogs.All()
			for _, log := range logs {
				if log.Message == "panic in gocron.NewTask function" && len(log.Context) > 0 && log.Context[0].Key == "err" && log.Context[0].String == "clock panic in Now()" {
					return // Success, end the test
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

func TestCronTrigger_WithOrgResolver(t *testing.T) {
	// Create a mock org resolver
	mockOrgResolver := &MockOrgResolver{}
	mockOrgResolver.On("Get", mock.Anything, "test-owner").Return("org-123", nil)

	// Create clock for testing
	fakeClock := clockwork.NewFakeClock()

	// Create the cron trigger service with org resolver
	service, err := NewTriggerService(logger.Test(t), fakeClock, mockOrgResolver)
	require.NoError(t, err)

	// Start the service
	ctx := context.Background()
	err = service.Start(ctx)
	require.NoError(t, err)
	defer service.Close()

	// Setup test trigger
	triggerID := "test-trigger"
	metadata := capabilities.RequestMetadata{
		WorkflowID:               "test-workflow",
		WorkflowOwner:            "test-owner",
		WorkflowName:             "test-workflow-name",
		WorkflowDonID:            1,
		WorkflowDonConfigVersion: 1,
		WorkflowExecutionID:      "test-execution",
	}

	config := &crontypedapi.Config{
		Schedule: "*/5 * * * * *", // Every 5 seconds
	}

	// Register the trigger
	triggerCh, err := service.RegisterTrigger(ctx, triggerID, metadata, config)
	require.NoError(t, err)

	// Advance the clock to trigger the cron job
	fakeClock.Advance(6 * time.Second)

	// Wait for the trigger to fire
	select {
	case trigger := <-triggerCh:
		assert.NotNil(t, trigger)
		// The trigger ID is generated by the cron trigger based on schedule time, not the registration ID
		assert.NotEmpty(t, trigger.Id)
		// The important thing is that we received a trigger event
	case <-time.After(2 * time.Second):
		t.Fatal("Expected trigger to fire but it didn't")
	}

	// Verify that the org resolver was called
	mockOrgResolver.AssertExpectations(t)
}

func TestCronTrigger_WithoutOrgResolver(t *testing.T) {
	// Create clock for testing
	fakeClock := clockwork.NewFakeClock()

	// Create the cron trigger service without org resolver (nil)
	service, err := NewTriggerService(logger.Test(t), fakeClock, nil)
	require.NoError(t, err)

	// Start the service
	ctx := context.Background()
	err = service.Start(ctx)
	require.NoError(t, err)
	defer service.Close()

	// Setup test trigger
	triggerID := "test-trigger"
	metadata := capabilities.RequestMetadata{
		WorkflowID:               "test-workflow",
		WorkflowOwner:            "test-owner",
		WorkflowName:             "test-workflow-name",
		WorkflowDonID:            1,
		WorkflowDonConfigVersion: 1,
		WorkflowExecutionID:      "test-execution",
	}

	config := &crontypedapi.Config{
		Schedule: "*/5 * * * * *", // Every 5 seconds
	}

	// Register the trigger
	triggerCh, err := service.RegisterTrigger(ctx, triggerID, metadata, config)
	require.NoError(t, err)

	// Advance the clock to trigger the cron job
	fakeClock.Advance(6 * time.Second)

	// Wait for the trigger to fire
	select {
	case trigger := <-triggerCh:
		assert.NotNil(t, trigger)
		assert.NotEmpty(t, trigger.Id)
	case <-time.After(2 * time.Second):
		t.Fatal("Expected trigger to fire but it didn't")
	}
}
