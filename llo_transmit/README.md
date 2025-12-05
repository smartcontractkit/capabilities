# LLO Transmit Action Capability

> A NoDAG action capability that enables CRE workflows to transmit LLO (Low Latency Oracle) reports to multiple configured destinations.

---

## Overview

The LLO Transmit capability is adapted from the [chainlink LLO transmitter](https://github.com/smartcontractkit/chainlink/blob/develop/core/services/llo/transmitter.go) to work as a standalone NoDAG action capability. It provides a fan-out transmission mechanism that can send LLO reports to multiple destinations simultaneously.

### Key Features

- **Multi-Destination Transmission**: Fan out reports to multiple transmitters (Mercury servers, CRE triggers, etc.)
- **Parallel Execution**: Transmit to all destinations concurrently for low latency
- **Lifecycle Management**: Proper startup/shutdown of all sub-transmitters
- **Health Monitoring**: Aggregate health status from all transmission paths
- **Flexible Configuration**: Support for multiple transmitter types and configurations

---

## Architecture

```
┌─────────────────────────────────────────────────┐
│  CRE Workflow (NoDAG)                           │
│                                                 │
│  ┌─────────────────────────────────┐           │
│  │ Step: Transmit LLO Report       │           │
│  │ ID: llo-transmit@1.0.0          │           │
│  └─────────────┬───────────────────┘           │
└────────────────┼─────────────────────────────────┘
                 │
                 │ CapabilityRequest
                 ▼
┌─────────────────────────────────────────────────┐
│  LLO Transmit Action Capability                 │
│                                                 │
│  ┌───────────────────────────────────────┐     │
│  │  Fan-Out Coordinator                  │     │
│  │  • Parallel transmission               │     │
│  │  • Result aggregation                  │     │
│  │  • Error handling                      │     │
│  └─────────┬──────────┬───────────┬──────┘     │
│            │          │           │            │
│     ┌──────▼─────┐ ┌──▼──────┐ ┌──▼──────┐    │
│     │  Mercury   │ │   CRE   │ │  Custom │    │
│     │Transmitter │ │Trigger  │ │Transmit │    │
│     └──────┬─────┘ └──┬──────┘ └──┬──────┘    │
└────────────┼──────────┼───────────┼────────────┘
             │          │           │
             ▼          ▼           ▼
      Mercury     CRE        Other
      Servers   Workflows  Destinations
```

---

## Usage

### 1. Deployment

Deploy the capability as a standard capabilities binary:

```toml
type = "standardcapabilities"
schemaVersion = 1
name = "llo-transmit-capability"
command = "<deploymentpath>/llo_transmit"
config = '''
{
  "donID": 1,
  "verboseLogging": true,
  "transmitters": [
    {
      "type": "cre",
      "opts": {
        "triggerCapabilityName": "streams-trigger",
        "triggerCapabilityVersion": "2.0.0"
      }
    }
  ]
}
'''
```

### 2. Workflow Integration

Use in a CRE workflow to transmit LLO reports:

```yaml
name: "llo-report-transmission"
owner: "0x0100000000000000000000000000000000000001"

triggers:
  - id: "ocr-report-generated@1.0.0"
    ref: "report_trigger"
    config:
      reportFormat: "llo"

actions:
  - id: "llo-transmit@1.0.0"
    ref: "transmit_action"
    inputs:
      configDigest: $(report_trigger.outputs.configDigest)
      seqNr: $(report_trigger.outputs.seqNr)
      report: $(report_trigger.outputs.report)
      reportInfo: $(report_trigger.outputs.reportInfo)
      signatures: $(report_trigger.outputs.signatures)
    config:
      donID: 1
      verboseLogging: false

targets:
  - id: "log-target@1.0.0"
    ref: "logger"
    inputs:
      message: $(transmit_action.outputs)
```

---

## Configuration

### Top-Level Configuration

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `donID` | uint32 | Yes | Decentralized Oracle Network ID |
| `servers` | map[string][]byte | No | Legacy Mercury server configurations |
| `transmitters` | []TransmitterConfig | Yes | Sub-transmitter configurations |
| `verboseLogging` | bool | No | Enable detailed debug logging |
| `fromAccount` | string | No | Account identifier for transmissions |

### Transmitter Configuration

Each transmitter in the `transmitters` array:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | string | Yes | Transmitter type: "cre", "mercury", "mock" |
| `opts` | JSON | Yes | Type-specific configuration options |

#### CRE Transmitter Options

```json
{
  "triggerCapabilityName": "streams-trigger",
  "triggerCapabilityVersion": "2.0.0",
  "triggerTickerMinResolutionMs": 1000,
  "triggerSendChannelBufferSize": 1000
}
```

#### Mercury Transmitter Options

```json
{
  "serverURL": "wss://mercury.example.com:4242",
  "serverPubKey": "0x1234...",
  "queueSize": 10000
}
```

---

## Input Schema

The action accepts the following inputs:

```typescript
interface Request {
  configDigest: bytes;     // OCR configuration digest
  seqNr: number;          // Report sequence number
  report: bytes;          // The LLO report to transmit
  reportInfo: {
    lifeCycleStage: string;  // e.g., "production", "staging", "retirement"
    reportFormat: string;    // e.g., "llo", "mercury"
  };
  signatures: Array<{
    signature: bytes;
    signer: number;
  }>;
}
```

---

## Output Schema

The action returns:

```typescript
interface Response {
  success: boolean;                     // Overall success status
  error?: string;                       // Error message if failed
  successfulTransmissions: number;      // Count of successful transmissions
  failedTransmissions: number;          // Count of failed transmissions
  transmitterResults: Array<{
    type: string;                       // Transmitter type
    success: boolean;                   // Individual success status
    error?: string;                     // Individual error if failed
  }>;
}
```

---

## Transmitter Types

### 1. CRE Transmitter

Transmits reports to CRE trigger capabilities for workflow consumption.

**Configuration:**
```json
{
  "type": "cre",
  "opts": {
    "triggerCapabilityName": "streams-trigger",
    "triggerCapabilityVersion": "2.0.0"
  }
}
```

**Use Case:** Send LLO reports to workflows for processing

### 2. Mercury Transmitter (Legacy)

Transmits reports to Mercury servers for Data Streams.

**Configuration:**
```json
{
  "type": "mercury",
  "opts": {
    "servers": {
      "wss://mercury1.example.com": "0x1234..."
    }
  }
}
```

**Use Case:** Legacy compatibility with Mercury infrastructure

### 3. Mock Transmitter

Simple mock for testing and development.

**Configuration:**
```json
{
  "type": "mock",
  "opts": {}
}
```

**Use Case:** Testing workflows without real transmission infrastructure

---

## Development

### Building

```bash
cd llo_transmit
go build -o dist/llo_transmit .
```

### Testing

```bash
go test -v ./...
```

### Linting

```bash
golangci-lint run
```

---

## Error Handling

The capability handles errors gracefully:

1. **Partial Failures**: If some transmitters succeed and others fail, the overall status is `success: false` but successful transmissions are reported
2. **Timeout Handling**: Each transmitter has configurable timeouts
3. **Retry Logic**: Individual transmitters can implement their own retry strategies
4. **Health Monitoring**: Health status includes all sub-transmitter health

---

## Comparison with Original LLO Transmitter

| Aspect | Original (chainlink) | NoDAG Capability |
|--------|---------------------|------------------|
| **Execution** | Embedded in node | Standalone binary |
| **Trigger** | Automatic from OCR | Explicit workflow action |
| **Dependencies** | Node infrastructure | Minimal, self-contained |
| **Configuration** | Job spec TOML | Capability config JSON |
| **Use Case** | Automatic transmission | Workflow-controlled transmission |

---

## Proto Definition

Located at: `chainlink-protos/cre/capabilities/llo/v1/transmit.proto`

Service ID: `llo-transmit@1.0.0`

Capability Type: Action

Mode: MODE_DON

---

## Related Capabilities

- **streams-trigger@1.0.0**: Receives transmitted reports as trigger events
- **ocr-report@1.0.0**: Generates OCR reports that can be transmitted
- **mercury-trigger@1.0.0**: Legacy trigger for Mercury reports

---

## Troubleshooting

### Problem: Transmissions failing silently

**Solution**: Enable `verboseLogging: true` in configuration to see detailed transmission logs

### Problem: Sub-transmitter not starting

**Solution**: Check sub-transmitter configuration and ensure required dependencies are available

### Problem: Reports not reaching workflows

**Solution**: Verify CRE transmitter configuration matches the trigger capability name and version

---

## Future Enhancements

- [ ] Add support for report batching
- [ ] Implement transmission metrics and monitoring
- [ ] Add configurable retry strategies
- [ ] Support for dynamic transmitter registration
- [ ] WebSocket transmitter for custom endpoints

---

## References

- [Original LLO Transmitter](https://github.com/smartcontractkit/chainlink/blob/develop/core/services/llo/transmitter.go)
- [CRE Transmitter](https://github.com/smartcontractkit/chainlink/blob/develop/core/services/llo/cre/transmitter.go)
- [LLO Types](https://github.com/smartcontractkit/chainlink-common/blob/main/pkg/types/llo/types.go)
- [Capabilities Documentation](../README.md)

---

## License

Same as parent chainlink repository

## Support

For issues or questions, please file an issue in the capabilities repository.









