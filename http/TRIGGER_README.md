# HTTP Trigger

The HTTP trigger capability allows external systems to execute workflows in the Chainlink Runtime Environment (CRE) through HTTP requests. This capability serves as a bridge between off-chain systems and CRE workflows, enabling seamless integration with external applications.

## Overview

HTTP trigger enables external applications to invoke workflows in CRE through a secure HTTP/JSON-RPC interface. It handles authentication, authorization, rate limiting, and request routing to ensure that only authorized users can trigger workflows and that the system remains stable under load.

### Key Features
- **JSON-RPC interface** for triggering workflows
- **JWT-based authentication** and authorization using EVM-style ECDSA signatures on secp256k1 curve
- **Multi-level rate limiting**: global, per gateway, per workflow owner
- **CL Gateway integration** for WAF (Web Application Firewall)
- **Workflow identification** by ID or metadata (owner/name/tag)
- **Request deduplication** and idempotency handling
- **Metadata publishing** to gateway nodes: Workflow nodes load WASM workflows and send the workflow metadata to gateway nodes
- **DON Mode**: Gateway nodes broadcast requests to and aggregate responses from workflow nodes for BFT (Byzantine Fault Tolerance)

### Architecture Components

- **Connector Handler**: Core component that processes gateway messages and triggers workflow execution
- **Gateway Metadata Publisher**: Manages workflow metadata distribution to gateway nodes
- **Workflow Store**: Maintains a registry of workflows and their authorized keys
- **Request Cache**: Provides deduplication for requests

## API Specification

### Triggering a Workflow

**Request:**
```json
{
  "jsonrpc": "2.0",
  "id": "8f73d3a4-6d7c-4d1d-b9f2-28c8f630fa27",
  "method": "workflows.execute",
  "params": {
    "workflow": {
      "workflowID": "e3c0f8139e9e4cf0b2c31c70f3f4ae12"
    },
    "input": {"key": "value", "count": 5}
  }
}
```

Alternatively, workflows can be identified by metadata:
```json
"workflow": {
  "workflowOwner": "0x1234...abcd",
  "workflowName": "sendPayment",
  "workflowTag": "production-mainnet"
}
```

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": "8f73d3a4-6d7c-4d1d-b9f2-28c8f630fa27",
  "result": {
    "workflowExecutionID": "hash_of_workflow_id_and_request_id",
    "status": "ACCEPTED"
  }
}
```

---

## Architecture Deep Dive

### Connector Handler

The `connectorHandler` is the core component that implements the `GatewayConnectorHandler` interface and manages all gateway communication. There are three types of messages from gateway nodes:

#### Supported Gateway Messages

1. **`workflows.execute`** - Triggers workflow execution
2. **`workflows.pullMetadata`** - Gateway requests workflow metadata from workflow nodes
3. **`workflows.pushMetadata`** - Node pushes workflow metadata to gateway. Uni-directional from workflow nodes to gateway nodes.

#### Message Processing Flow

1. **Rate Limiting Check**: All incoming messages are subject to rate limiting (global, per gateway, per workflow owner)
2. **Method Routing**: Messages are routed based on their JSON-RPC method
3. **Request Validation**: Input parameters are validated and converted to appropriate formats
4. **Response Generation**: Responses are constructed and sent back to the gateway

### Workflow Registration Process

When a workflow is registered via `RegisterWorkflow()`:

1. **Key Validation**: Authorized keys are validated (format, type, count limits)
2. **Metadata Broadcasting**: Workflow metadata is pushed to all gateway nodes via `BroadcastWorkflowMetadata()`
3. **Local Storage**: Workflow is stored in the workflow store with dual indexing:
   - By `workflowID` for direct lookup
   - By `workflowReference` (owner/name/tag) for metadata-based lookup

### JSON-RPC Response Handling

#### Response Structure
All responses follow the JSON-RPC 2.0 specification:

```json
{
  "jsonrpc": "2.0",
  "id": "<request_id>",
  "result": {
    "workflowExecutionID": "hash_of_workflow_id_and_request_id",
    "status": "ACCEPTED"
  }
}
```

#### Request ID Format
- **For workflow execution**: The request ID is provided by the gateway
- **For metadata operations**: Uses pattern `<method>/<identifier>/<uuid>`
  - Pull metadata: `workflows.pullMetadata/<requestID>/<uuid>`
  - Push metadata: `workflows.pushMetadata/<workflowID>/<uuid>`

#### Workflow Execution ID Generation
Execution IDs are generated using:
```go
workflowExecutionID = EncodeExecutionID(workflowID, requestID)
```
This ensures unique execution IDs that can be traced back to their originating request and guarantees uniqueness per workflow.

### Deduplication

The request cache system deduplicates requests to prevent double-spending and retry storms.

#### Deduplication Logic
1. **Request Hashing**: Each incoming request is hashed using its complete JSON payload
2. **Cache Lookup**: Check if a request with the same ID exists
3. **Hash Comparison**: 
   - **Same hash**: Returns cached response (duplicate request)
   - **Different hash**: Returns conflict error (request ID reuse with different payload)
   - **No entry**: Processes as new request

#### Cache Management
- **TTL-based expiration**: Entries expire after a configurable duration. Background process removes expired entries
- **Storage**: Uses job-based KeyValueStore (defined in core node) for persistence

### Gateway Metadata Publisher

The metadata publisher manages workflow metadata distribution to gateway nodes.

#### Metadata Structure
Each workflow's metadata includes:
```go
type WorkflowMetadata struct {
    WorkflowSelector WorkflowSelector  // ID, owner, name, tag
    AuthorizedKeys   []AuthorizedKey   // Public keys authorized to trigger
}
```

#### Publishing Mechanisms

**1. Push Metadata (Registration-triggered)**
- Triggered during workflow registration
- Broadcasts to all connected gateways
- Includes retry logic with exponential backoff
- Non-blocking (failures don't prevent registration)

**2. Pull Metadata (Gateway-initiated)**
- Gateway requests metadata using `workflows.pullMetadata`
- Returns metadata in configurable batches and supports pagination for large workflow sets

The workflow metadata is used in the gateway node for two purposes:
- **Authentication and Authorization**: Eventually-consistent JWT auth
- **Workflow Lookup**: Looking up workflows using different workflow selectors
---

## Configuration

### Service Configuration

The HTTP trigger uses a comprehensive configuration structure to control various operational aspects:

```go
type ServiceConfig struct {
    SendChannelBufferSize         uint32            `json:"sendChannelBufferSize"`
    MaxAuthorizedKeysPerWorkflow  uint32            `json:"maxAuthorizedKeysPerWorkflow"`
    MetadataBatchSize            uint32            `json:"metadataBatchSize"`
    RequestCacheTTL              time.Duration     `json:"requestCacheTTL"`
    GatewayConnectionConfig      GatewayConfig     `json:"gatewayConnection"`
}
```

#### Configuration Fields

- **`sendChannelBufferSize`**: Buffer size for workflow trigger channels (prevents blocking when workflow engine is overloaded)
- **`maxAuthorizedKeysPerWorkflow`**: Maximum number of authorized keys per workflow
- **`metadataBatchSize`**: Number of workflows to include in each metadata batch sent to gateways
- **`requestCacheTTL`**: Time-to-live for cached requests (deduplication window)
- **`gatewayConnectionConfig`**: Gateway connection and retry configuration

### Gateway Connection Configuration

```go
type GatewayConnectionConfig struct {
    MaxPushMetadataDurationMs uint32      `json:"maxPushMetadataDurationMs"`
    MaxPullMetadataDurationMs uint32      `json:"maxPullMetadataDurationMs"`
    RetryConfig              RetryConfig `json:"retryConfig"`
}

type RetryConfig struct {
    InitialIntervalMs  uint32  `json:"initialIntervalMs"`
    MaxIntervalTimeMs  uint32  `json:"maxIntervalTimeMs"`
    Multiplier         float64 `json:"multiplier"`
}
```

### Default Configuration Example

```json
{
  "sendChannelBufferSize": 1000,
  "maxAuthorizedKeysPerWorkflow": 10,
  "metadataBatchSize": 100,
  "requestCacheTTL": "1h",
  "gatewayConnection": {
    "maxPushMetadataDurationMs": 30000,
    "maxPullMetadataDurationMs": 60000,
    "retryConfig": {
      "initialIntervalMs": 100,
      "maxIntervalTimeMs": 10000,
      "multiplier": 2.0
    }
  }
}
```

---

## Error Handling

### JSON-RPC Error Codes

The HTTP trigger uses standard JSON-RPC error codes with specific meanings:

- **`-32700`** (Parse Error): Invalid JSON in request
- **`-32600`** (Invalid Request): Request is invalid
- **`-32601`** (Method Not Found): Unsupported JSON-RPC method
- **`-32602`** (Invalid Params): Invalid method parameters
- **`-32603`** (Internal Error): Internal server error
- **`-32000`** (Server Error): Custom server errors (e.g., workflow not found)
- **`-32001`** (Server Overloaded): Rate limits exceeded or system overloaded

### Rate Limiting Behavior

When rate limits are exceeded:
- **Incoming limits**: Messages are dropped and logged
- **Outgoing limits**: Responses are not sent and logged as errors
- **No error responses**: Rate-limited requests receive no response to prevent amplification

### Request Validation Errors

Common validation errors and their handling:
- **Missing workflow identification**: Returns "Workflow not registered" error
- **Invalid authorized keys**: Returns validation error during registration
- **Malformed JSON**: Returns parse error
- **Request conflicts**: Returns conflict error for duplicate request IDs with different payloads
---

## Development

### Testing

```bash
# Run all tests
go test ./... -v

# Run tests with coverage
go test ./... -coverpkg=./... -coverprofile=coverage.txt

# Run race condition tests  
go test -race ./...
```

