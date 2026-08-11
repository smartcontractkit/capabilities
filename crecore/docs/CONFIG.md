# main Configuration

## Example

```toml
# ----- Global Configuration -----
capabilities-registry-address = '0xYourRegistryAddress'
capabilities-registry-sync-interval = '12s'
[telemetry]
endpoint = ''
insecure-connection = false
ca-cert-file = ''
attributes = ['env=staging']
auth-pub-key-hex = ''
auth-headers-ttl = '0s'
prometheus-bridge-enabled = false
[tracing]
enabled = false
sampling-ratio = 1
tls-cert-file = ''
[chip-ingress]
endpoint = ''
insecure-connection = false
[pyroscope]
server-address = ''
environment = ''
[prometheus]
port = -1
[evm]
http-url = ['https://rpc.example.com']
chain-id = '1'
chain-type = ''
finality-tag-enabled = true
finality-depth = 50
poll-interval = '10s'
[proxy]
listen-address = ':50051'

# ----- Command: main embed -----
instances = 1

# ----- Command: main run -----
[ocr]
listen-addresses = ['127.0.0.1:1234']
delta-reconcile = '1m0s'
delta-dial = '5s'
incoming-buffer-size = 100
outgoing-buffer-size = 100
keystore-password = 'xxxxx'
[database]
url = 'postgresql://user:password@localhost:5432/chainlink?sslmode=disable'


```

## Global
```toml
capabilities-registry-address = '0xYourRegistryAddress' # Example
capabilities-registry-sync-interval = '12s' # Default
```


# Global Configuration

### capabilities-registry-address
```toml
capabilities-registry-address = '0xYourRegistryAddress' # Example
```
capabilities-registry-address on-chain CapabilitiesRegistry (v2) contract address

### capabilities-registry-sync-interval
```toml
capabilities-registry-sync-interval = '12s' # Default
```
capabilities-registry-sync-interval how often the on-chain registry is re-read

## telemetry
```toml
[telemetry]
endpoint = '' # Default
insecure-connection = false # Default
ca-cert-file = '' # Default
attributes = ['env=staging'] # Example
auth-pub-key-hex = '' # Default
auth-headers-ttl = '0s' # Default
prometheus-bridge-enabled = false # Default
```


### endpoint
```toml
endpoint = '' # Default
```
endpoint OTLP gRPC endpoint telemetry is exported to; telemetry is disabled when unset

### insecure-connection
```toml
insecure-connection = false # Default
```
insecure-connection export telemetry over an insecure connection

### ca-cert-file
```toml
ca-cert-file = '' # Default
```
ca-cert-file CA certificate file used to verify the telemetry endpoint

### attributes
```toml
attributes = ['env=staging'] # Example
```
attributes extra telemetry resource attributes, as key=value pairs

### auth-headers
```toml
auth-headers = [] # Docs only
```
auth-headers telemetry auth headers, as key=value pairs

### auth-pub-key-hex
```toml
auth-pub-key-hex = '' # Default
```
auth-pub-key-hex public key the telemetry auth headers are derived from

### auth-headers-ttl
```toml
auth-headers-ttl = '0s' # Default
```
auth-headers-ttl how long generated telemetry auth headers are valid for

### prometheus-bridge-enabled
```toml
prometheus-bridge-enabled = false # Default
```
prometheus-bridge-enabled feed metrics registered on the prometheus registry into the telemetry pipeline

## tracing
```toml
[tracing]
enabled = false # Default
sampling-ratio = 1 # Default
tls-cert-file = '' # Default
```


### enabled
```toml
enabled = false # Default
```
enabled export traces to the telemetry endpoint

### sampling-ratio
```toml
sampling-ratio = 1 # Default
```
sampling-ratio fraction of traces sampled, from 0 to 1

### tls-cert-file
```toml
tls-cert-file = '' # Default
```
tls-cert-file TLS certificate file used by the trace exporter

## chip-ingress
```toml
[chip-ingress]
endpoint = '' # Default
insecure-connection = false # Default
```


### endpoint
```toml
endpoint = '' # Default
```
endpoint chip ingress gRPC endpoint; the emitter is disabled when unset

### insecure-connection
```toml
insecure-connection = false # Default
```
insecure-connection connect to chip ingress over an insecure connection

## pyroscope
```toml
[pyroscope]
server-address = '' # Default
environment = '' # Default
```


### server-address
```toml
server-address = '' # Default
```
server-address pyroscope server address; profiling is disabled when unset

### auth-token
```toml
auth-token = 'xxxxx' # Docs only
```
auth-token pyroscope auth token

### environment
```toml
environment = '' # Default
```
environment tag attached to profiles

## prometheus
```toml
[prometheus]
port = -1 # Default
```


### port
```toml
port = -1 # Default
```
port serving /metrics, /debug/pprof, /healthz and /readyz; -1 disables the server, 0 asks the OS for an ephemeral port. Instance i of an embed run listens on this port plus i

## evm
```toml
[evm]
http-url = ['https://rpc.example.com'] # Example
ws-url = [] # Default
chain-id = '1' # Example
chain-type = '' # Default
finality-tag-enabled = true # Default
finality-depth = 50 # Default
poll-interval = '10s' # Default
```


### http-url
```toml
http-url = ['https://rpc.example.com'] # Example
```
http-url EVM RPC HTTP URL(s); repeat or comma-separate for a multinode pool

### ws-url
```toml
ws-url = [] # Default
```
ws-url EVM RPC WebSocket URL(s), positionally paired with --evm.http-url; optional (must not be set unless http-url is set)

### chain-id
```toml
chain-id = '1' # Example
```
chain-id EVM chain ID

### chain-type
```toml
chain-type = '' # Default
```
chain-type EVM chain type (empty for a generic EVM chain)

### finality-tag-enabled
```toml
finality-tag-enabled = true # Default
```
finality-tag-enabled use the finalized block tag instead of a finality depth

### finality-depth
```toml
finality-depth = 50 # Default
```
finality-depth finality depth, used when --evm.finality-tag-enabled=false

### poll-interval
```toml
poll-interval = '10s' # Default
```
poll-interval per-node health poll interval

## proxy
```toml
[proxy]
listen-address = ':50051' # Default
instances = 1 # Default
```


### listen-address
```toml
listen-address = ':50051' # Default
```
listen-address address (host:port) this server listens on; instance i of an embed run listens on the port plus i

# Command: main embed

### instances
```toml
instances = 1 # Default
```
instances number of instances to run in this process

# Command: main run

## ocr
```toml
[ocr]
listen-addresses = ['127.0.0.1:1234'] # Example
announce-addresses = [] # Default
delta-reconcile = '1m0s' # Default
delta-dial = '5s' # Default
incoming-buffer-size = 100 # Default
outgoing-buffer-size = 100 # Default
keystore-password = 'xxxxx' # Default
```


### listen-addresses
```toml
listen-addresses = ['127.0.0.1:1234'] # Example
```
listen-addresses rage p2p V2 listen addresses (host:port); creates a local peer

### announce-addresses
```toml
announce-addresses = [] # Default
```
announce-addresses rage p2p V2 announce addresses (host:port); defaults to the listen addresses (must not be set unless listen-addresses is set)

### delta-reconcile
```toml
delta-reconcile = '1m0s' # Default
```
delta-reconcile rage p2p V2 delta reconcile interval

### delta-dial
```toml
delta-dial = '5s' # Default
```
delta-dial rage p2p V2 minimum interval between dial attempts

### incoming-buffer-size
```toml
incoming-buffer-size = 100 # Default
```
incoming-buffer-size per-remote incoming message buffer size

### outgoing-buffer-size
```toml
outgoing-buffer-size = 100 # Default
```
outgoing-buffer-size per-remote outgoing message buffer size

### keystore-password
```toml
keystore-password = 'xxxxx' # Default
```
keystore-password password for the node keystore holding the shared P2P identity; required unless the identity is derived, as it is under embed

## database
```toml
[database]
url = 'postgresql://user:password@localhost:5432/chainlink?sslmode=disable' # Example
```


### url
```toml
url = 'postgresql://user:password@localhost:5432/chainlink?sslmode=disable' # Example
```
url database url

