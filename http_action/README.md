# HTTP Action Capability

> A capability module that enables CRE workflows (V2/NoDAG engine) to perform outbound HTTP requests to external systems.

---

## 1. System Overview

### 1.1 Purpose

The HTTP Action Capability enables Chainlink Runtime Environment (CRE) workflows to perform secure, rate-limited outbound HTTP requests to external systems.

### 1.2 Core Functionality

- **HTTP Methods**: GET, POST, PUT, DELETE, PATCH
- **Proxy Modes**: Direct mode and Gateway mode (production default)
- **Security**: SSRF protection via IP filtering, port restrictions, and scheme validation
- **Rate Limiting**: Global and per-workflow controls
- **Validation**: Request validation and sanitization
- **Error Handling**: HTTP status code responses

---

## 2. Architecture

### 2.1 Components

#### 2.1.1 HTTP Action Service
- **Purpose**: Service orchestration and lifecycle management
- **Functions**: Initialization, request routing, lifecycle management

#### 2.1.2 Validation Layer
- **Purpose**: Input validation and sanitization
- **Validates**: HTTP methods, URLs, headers, body size, timeouts

#### 2.1.3 Direct Mode Client
- **Purpose**: Direct HTTP request execution
- **Features**: Secure HTTP operations with `safeurl`, rate limiting

#### 2.1.4 Gateway Mode Client
- **Purpose**: Gateway-proxied HTTP request execution
- **Features**: Rate limiting, request deduplication via consistent hashing, exponential backoff retry
- **Deadlines**: `timeoutMs` bounds delivery to a gateway. The response wait is `timeoutMs +
  responseGraceMs`, started after the send, so the gateway (which applies `timeoutMs` to the endpoint
  call itself) always reports back before we give up. Exhausting the grace means the gateway went silent.

---

## 3. API Specification

### 3.1 Request Schema

```go
type Request struct {
    Url           string                 `json:"url"`           // Required: Target URL
    Method        string                 `json:"method"`        // Required: HTTP method
    Headers       map[string]string      `json:"headers"`       // Optional: HTTP headers
    Body          []byte                 `json:"body"`          // Optional: Request body
    TimeoutMs     int32                  `json:"timeoutMs"`     // Optional: Timeout in milliseconds
    CacheSettings *CacheSettings         `json:"cacheSettings"` // Optional: Cache configuration (gateway mode only)
}

type CacheSettings struct {
    Store         bool  `json:"store"`     // Enable cache reading
    MaxAgeMs      int64 `json:"maxAgeMs"`  // Cached entry max age in milliseconds
}
```

### 3.2 Response Schema

```go
type Response struct {
    StatusCode int               `json:"statusCode"` // HTTP status code
    Headers    map[string]string `json:"headers"`    // Response headers
    Body       []byte            `json:"body"`       // Response body
}
```

### 3.3 Validation Rules

#### 3.3.1 Required Fields
- `url`: Must be non-empty after trimming whitespace
- `method`: Must be one of: GET, POST, PUT, DELETE, PATCH (case-insensitive)

#### 3.3.2 Optional Fields with Defaults
- `timeoutMs`: Defaults to service configuration `maxTimeoutMs` if not provided or 0
- `headers`: Defaults to empty map
- `body`: Defaults to empty byte array
- `cacheSettings`: Defaults to empty cache settings if not provided

#### 3.3.3 Constraint Validation
- `timeoutMs`: Must be between 0 and configured `maxTimeoutMs`
- `headers`: Count must not exceed `maxHeaderCount`
- Header keys: Length must not exceed `maxHeaderKeyLength`
- Header values: Length must not exceed `maxHeaderValueLength`
- `body`: Size must not exceed `maxRequestBytes`

#### 3.3.4 Cache Settings Validation
- `maxAgeMs`: Must be non-negative and not exceed configured `maxCacheAgeMs`.
- `store`: Can be true or false;

### 3.4 Error Classification

Failures are attributed to the party responsible for them. Only platform faults reach
`http_action_execution_error_count`, which is what the "Execution Errors in More than F Nodes" alert
reads. A slow or failing customer endpoint must never page us.

| Condition | Error type | Capability error | Counters (`http_action_` prefix) |
|---|---|---|---|
| Input validation failed | `UserError` | user, `InvalidArgument` / `LimitExceeded` | `validation_failure_count` |
| Gateway reports endpoint send/read failure | `UserError` | user, `InvalidArgument` | `external_endpoint_error_count` |
| Gateway reports a blocked request | `UserError` | user, `InvalidArgument` | `validation_failure_count` |
| Response exceeds the size limit | `UserError` | user, `LimitExceeded` | `external_endpoint_error_count` |
| No gateway reachable | plain error | system, `Internal` | `capability_gateway_send_error_count`, `execution_error_count` |
| Gateway returned an unclassified error | plain error | system, `Internal` | `execution_error_count` |
| Gateway silent past the response deadline | `TimeoutError` | system, `DeadlineExceeded` | `execution_timeout_count`, `execution_error_count` |
| Caller canceled before a response arrived | `CanceledError` | system, `Canceled` | `request_canceled_count` |

Direct mode returns `InputValidationError` rather than `UserError` for the first row; both map to the
same capability error.

Cancellation is not the user's fault, but `caperrors.Origin` has only `System` and `User`, so it is
reported as a system error carrying `Canceled`. Alerts over capability failures should exclude that
code rather than treat it as a platform fault.

---

## 4. Configuration Specification

### 4.1 Service Configuration Schema

```go
type ServiceConfig struct {
    ProxyMode               string                  `json:"proxyMode"`
    GatewayConnectionConfig GatewayConnectionConfig `json:"gatewayConnection"`
    HTTPClientConfig        HTTPClientConfig        `json:"httpClient"`
}
```

### 4.2 Limits Configuration

```go
type LimitsConfig struct {
    MaxTimeoutMs         uint32 `json:"maxTimeoutMs"`         // Maximum timeout in milliseconds
    MaxResponseBytes     uint32 `json:"maxResponseBytes"`     // Maximum response body size
    MaxHeaderCount       uint32 `json:"maxHeaderCount"`       // Maximum number of headers
    MaxHeaderKeyLength   uint32 `json:"maxHeaderKeyLength"`   // Maximum header key length
    MaxHeaderValueLength uint32 `json:"maxHeaderValueLength"` // Maximum header value length
    MaxRequestBytes      uint32 `json:"maxRequestBytes"`      // Maximum request body size
    MaxCacheAgeMs        uint32 `json:"maxCacheAgeMs"`        // Maximum cache age in milliseconds
}
```

### 4.4 HTTP Client Configuration (Direct Mode)

```go
type HTTPClientConfig struct {
    BlockedIPs     []string `json:"blockedIPs"`     // Blocked IP addresses
    BlockedIPsCIDR []string `json:"blockedIPsCIDR"` // Blocked CIDR blocks
    AllowedPorts   []int    `json:"allowedPorts"`   // Allowed ports
    AllowedSchemes []string `json:"allowedSchemes"` // Allowed URL schemes
    AllowedIPs     []string `json:"allowedIPs"`     // Explicitly allowed IPs
    AllowedIPsCIDR []string `json:"allowedIPsCIDR"` // Explicitly allowed CIDR blocks
}
```

### 4.5 Gateway Connection Configuration

```go
type GatewayConnectionConfig struct {
    InitialIntervalMs uint32  `json:"initialIntervalMs"` // Initial retry interval
    MaxElapsedTimeMs  uint32  `json:"maxElapsedTimeMs"`  // Maximum retry duration
    Multiplier        float64 `json:"multiplier"`        // Backoff multiplier
    ResponseGraceMs   uint32  `json:"responseGraceMs"`   // Extra wait for a gateway response beyond timeoutMs
}
```

`responseGraceMs` defaults to 5000, mirroring the gateway's own budget for delivering a response back
to the node. It is optional: omitting it keeps the default, so existing deployments need no change.

### 4.6 Configuration Examples

#### 4.6.1 Gateway Mode Configuration

```json
{
  "proxyMode": "gateway",
  "gatewayConnection": {
    "initialIntervalMs": 100,
    "maxElapsedTimeMs": 30000,
    "multiplier": 2.0,
    "responseGraceMs": 5000
  }
}
```

#### 4.6.2 Direct Mode Configuration

```json
{
  "proxyMode": "direct",
  "httpClient": {
    "blockedIPs": [],
    "blockedIPsCIDR": [],
    "allowedPorts": [80, 443],
    "allowedSchemes": ["http", "https"],
    "allowedIPs": [],
    "allowedIPsCIDR": []
  }
}
```

> **See also:** [Default values in `action/validate.go`](./action/validate.go)

---

## 5. Development

### 5.1 Running Tests

```bash
# Run all tests
go test ./... -v

# Run tests with coverage
go test ./... -coverpkg=./... -coverprofile=coverage.txt

# Run race condition tests
go test -race ./...
```

### 5.2 Building

```bash
# Build for Linux amd64
CGO_ENABLED='0' GOOS='linux' GOARCH='amd64' go build -o ./bin/amd64/http_action .

# Build for Linux arm64  
CGO_ENABLED='0' GOOS='linux' GOARCH='arm64' go build -o ./bin/arm64/http_action .

# Build for current platform
go build -o ./http_action .
```
