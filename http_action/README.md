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

---

## 4. Configuration Specification

### 4.1 Service Configuration Schema

```go
type ServiceConfig struct {
    IncomingRateLimiter     RateLimiterConfig       `json:"incomingRateLimiter"`
    OutgoingRateLimiter     RateLimiterConfig       `json:"outgoingRateLimiter"`
    LimitsConfig            LimitsConfig            `json:"limits"`
    ProxyMode               string                  `json:"proxyMode"`
    GatewayConnectionConfig GatewayConnectionConfig `json:"gatewayConnection"`
    HTTPClientConfig        HTTPClientConfig        `json:"httpClient"`
}
```

### 4.2 Rate Limiter Configuration

```go
type RateLimiterConfig struct {
    GlobalRPS      float64 `json:"globalRPS"`      // Global requests per second
    GlobalBurst    int     `json:"globalBurst"`    // Global burst capacity
    PerSenderRPS   float64 `json:"perSenderRPS"`   // Per-sender requests per second
    PerSenderBurst int     `json:"perSenderBurst"` // Per-sender burst capacity
}
```

### 4.3 Limits Configuration

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
}
```

### 4.6 Configuration Examples

#### 4.6.1 Gateway Mode Configuration

```json
{
  "incomingRateLimiter": {
    "globalRPS": 100.0,
    "globalBurst": 100,
    "perSenderRPS": 100.0,
    "perSenderBurst": 100
  },
  "outgoingRateLimiter": {
    "globalRPS": 100.0,
    "globalBurst": 100,
    "perSenderRPS": 5.0,
    "perSenderBurst": 50
  },
  "limits": {
    "maxTimeoutMs": 20000,
    "maxResponseBytes": 10485760,
    "maxHeaderCount": 50,
    "maxHeaderKeyLength": 256,
    "maxHeaderValueLength": 1024,
    "maxRequestBytes": 10485760,
    "maxCacheAgeMs": 600000
  },
  "proxyMode": "gateway",
  "gatewayConnection": {
    "initialIntervalMs": 100,
    "maxElapsedTimeMs": 30000,
    "multiplier": 2.0
  }
}
```

#### 4.6.2 Direct Mode Configuration

```json
{
  "incomingRateLimiter": {
    "globalRPS": 100.0,
    "globalBurst": 100,
    "perSenderRPS": 100.0,
    "perSenderBurst": 100
  },
  "outgoingRateLimiter": {
    "globalRPS": 100.0,
    "globalBurst": 100,
    "perSenderRPS": 5.0,
    "perSenderBurst": 50
  },
  "limits": {
    "maxTimeoutMs": 20000,
    "maxResponseBytes": 10485760,
    "maxHeaderCount": 50,
    "maxHeaderKeyLength": 256,
    "maxHeaderValueLength": 1024,
    "maxRequestBytes": 10485760,
    "maxCacheAgeMs": 600000
  },
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
