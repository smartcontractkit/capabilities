# main Configuration

## Example

```toml
# ----- Global Configuration -----
proxy-listen-address = ':50051'
capabilities-registry-address = '0xYourRegistryAddress'
capabilities-registry-sync-interval = '12s'
[database]
url = 'postgresql://user:password@localhost:5432/chainlink?sslmode=disable'
[ocr]
listen-addresses = ['127.0.0.1:1234']
delta-reconcile = '1m0s'
delta-dial = '5s'
incoming-buffer-size = 100
outgoing-buffer-size = 100
keystore-password = 'xxxxx'
[evm]
http-url = ['https://rpc.example.com']
chain-id = '1'
chain-type = ''
finality-tag-enabled = true
finality-depth = 50
poll-interval = '10s'


```

## Global
```toml
proxy-listen-address = ':50051' # Default
capabilities-registry-address = '0xYourRegistryAddress' # Example
capabilities-registry-sync-interval = '12s' # Default
```


# Global Configuration

### proxy-listen-address
```toml
proxy-listen-address = ':50051' # Default
```
proxy-listen-address address the proxy gRPC server listens on

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

### fake
```toml
fake = false # Docs only
```
fake use fake dependencies instead of real ones

## database
```toml
[database]
url = 'postgresql://user:password@localhost:5432/chainlink?sslmode=disable' # Example
```


### url
```toml
url = 'postgresql://user:password@localhost:5432/chainlink?sslmode=disable' # Example
```
url database url; required unless running with --fake and without --real-db

### real-db
```toml
real-db = false # Docs only
```
real-db use a real database even though --fake is set; requires --fake, and a url to point at

## ocr
```toml
[ocr]
listen-addresses = ['127.0.0.1:1234'] # Example
announce-addresses = [] # Default
delta-reconcile = '1m0s' # Default
delta-dial = '5s' # Default
incoming-buffer-size = 100 # Default
outgoing-buffer-size = 100 # Default
keystore-password = 'xxxxx' # Example
proxy-address = '' # Default
```


### listen-addresses
```toml
listen-addresses = ['127.0.0.1:1234'] # Example
```
listen-addresses rage p2p V2 listen addresses (host:port); creates a local peer (required unless proxy-address is set; must not be set when proxy-address is set)

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
keystore-password = 'xxxxx' # Example
```
keystore-password password for the node keystore holding the shared P2P identity

### proxy-address
```toml
proxy-address = '' # Default
```
proxy-address delegate rage networking to a proxy at this gRPC address instead of creating a local peer (must not be set when listen-addresses is set)

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

