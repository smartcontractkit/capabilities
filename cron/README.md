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
{
	TriggerID: "test-id-1",
	Metadata:  capabilities.RequestMetadata{
		WorkflowID: "workflow-id-1",
	},
	Config:    values.NewMap(
        map[string]interface{}{
		    "schedule": "0 * * * * *",
	    }
    ),
}
```

## Output

|          Name          |                               Description                                |  Type  | Default |
|:----------------------:|:------------------------------------------------------------------------:|:------:|:-------:|
|  ActualExecutionTime   | Time that cron trigger's task execution occurred (RFC3339Nano formatted) | string |    -    |
| ScheduledExecutionTime |   Time that cron trigger's task execution had been scheduled to occur    | string |    -    |

### Sample

```
type Response struct {
// The ID of the trigger capability
TriggerType string
// The ID of the trigger event
ID string
// Trigger-specific payload
Outputs *values.Map
Payload: {  
    ActualExecutionTime: '2022-07-14T12:01:25.225089+08:00',
    ScheduledExecutionTime: '2022-07-14T12:01:25.000000+08:00',
}
}
```