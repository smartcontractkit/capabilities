# Confidential HTTP Action Capability

> A capability module (similar to http_action capability) that enables CRE workflows ((V2 / NoDAG engine)) to perform outbound HTTP requests to offchain systems. But unlike http_action capability, this capability fetches secrets from the Vault DON and uses them, without itself ever knowing these secrets.

---

## ✨ Overview

TO BE FILLED

## 📦 Features

- Supports following HTTP methods: `GET`, `POST`


🧪 Configuration

### 🛠️ Configuration Example

```json
{
}
```

### Run all tests:

TODO: Update this part when integration tests with real/mock DON, local CRE, etc. are ready.

```
cd http
go test ./... -v
```
