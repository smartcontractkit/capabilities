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

```toml
[limits]
timeoutMs = 10000                # Request timeout in milliseconds
maxResponseBytes = 1048576       # Maximum allowed response size in bytes
headerCount = 50                 # Maximum number of headers
maxHeaderKeyLength = 256         # Maximum length of a header key
maxRequestBytes = 1048576        # Maximum allowed request size in bytes
maxResponseBytes = 1048576       # Maximum allowed response size in bytes

[gatewayConnection]
initialIntervalMs = 100          # Initial retry interval in milliseconds
maxElapsedTimeMs = 10000         # Maximum total retry time in milliseconds
multiplier = 2.0                 # Backoff multiplier for retries
```

### Run all tests:

```
cd http
go test ./... -v
```
