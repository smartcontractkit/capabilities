# CRON Trigger

A trigger that uses a cron schedule to run periodically at fixed times, dates, or intervals.

## Configuration

|              Name              |                       Description                       | Type | Default |
|:------------------------------:|:-------------------------------------------------------:|:----:|:-------:|
| fastestScheduleIntervalSeconds | The maximum fastest speed that the trigger can schedule | int  |   30    |

## Register Trigger Input
|   Name   |                                                                               Description                                                                                |  Type  | Default |
|:--------:|:------------------------------------------------------------------------------------------------------------------------------------------------------------------------:|:------:|:-------:|
| schedule | A [CRON job schedule](https://cloud.google.com/scheduler/docs/configuring/cron-job-schedules), with second support by providing 6 slots, with seconds in the first slot. | string |    -    |

### Sample

```
payload, _ := anypb.New(&protos.Config{Schedule: "0 * * * * *"})

capabilities.TriggerRegistrationRequest{
	TriggerID: "test-id-1",
	Metadata:  capabilities.RequestMetadata{WorkflowID: "workflow-id-1"},
	Method:    "Trigger",
	Payload:   payload,
}
```

## Output

|          Name          |                               Description                                |  Type  | Default |
|:----------------------:|:------------------------------------------------------------------------:|:------:|:-------:|
| ScheduledExecutionTime |   Time that cron trigger's task execution had been scheduled to occur    | google.protobuf.Timestamp |    -    |

### Sample

```
capabilities.TriggerEvent{
	TriggerType: "cron-trigger@1.0.0",
	// The scheduled execution time, RFC3339 in UTC
	ID: "2022-07-14T04:01:25Z",
	// A protos.Payload
	Payload: anypb.Any{ScheduledExecutionTime: 2022-07-14T04:01:25Z},
}
```