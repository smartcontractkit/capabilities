# Workflow event target

This is a target that, when executed, emits events via a telemetry client

```mermaid
flowchart LR
    subgraph Chainlink_Node [Chainlink Node]
        subgraph LOOPP_Plugin [LOOPP Plugin]
            Workflow_Event_Capability[Workflow Event Capability]
        end
    end
    
    OTEL_Collector[OTEL Collector]
    
    Workflow_Event_Capability --> OTEL_Collector
```

## Development

### Tidy

`nx run tidy`

### Generate

`nx run generate`

### Test

`nx run test`

