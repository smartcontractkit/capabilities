# HTTP Action Capability

> A capability module that enables CRE workflows ((V2 / NoDAG engine)) to perform outbound HTTP requests to offchain systems.

---

## ✨ Overview

The **HTTP Action Capability** allows CRE workflows to interact with external HTTP APIs to retrieve data or perform actions (e.g. POST updates, fetch records, trigger webhooks). It is modeled as a **standard capability** and supports both **direct** and **gateway-proxied** request modes.

## 📦 Features

- Supports following HTTP methods: `GET`, `POST`, `PUT`, `DELETE`, `PATCH`
- Configurable limits for timeouts, headers, and body size
- Two outbound proxy modes:
  - `direct`: Calls made via the local HTTP client
  - `gateway`: Calls routed through gateway nodes with at-least-once delivery and deduplication
- Input validation with default fallback values


🧪 Configuration

### 🛠️ Configuration Example

```json
{
  "incomingRateLimiter": {
    "globalRPS": 100.0,
    "globalBurst": 100,
    "perSenderRPS": 100.0,
    "perSenderBurst": 100,
  },
  "outgoingRateLimiter": {
    "globalRPS": 100.0,
    "globalBurst": 100,
    "perSenderRPS": 100.0,
    "perSenderBurst": 100,
  },
  "limits": {
    "maxTimeoutMs": 10000,
    "maxResponseBytes": 1048576,
    "maxHeaderCount": 50,
    "maxHeaderKeyLength": 256,
    "maxHeaderValueLength": 1024,
    "maxRequestBytes": 1048576
  },
  "proxyMode": "gateway",
  "gatewayConnection": {
    "initialIntervalMs": 100,
    "maxElapsedTimeMs": 10000,
    "multiplier": 2.0
  },
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

### Run all tests:

```
cd http
go test ./... -v
```
