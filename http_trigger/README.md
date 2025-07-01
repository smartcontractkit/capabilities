# HTTP Trigger

The HTTP trigger capability allows external systems to execute workflows in the Chainlink Runtime Environment (CRE) through HTTP requests. This capability serves as a bridge between off-chain systems and CRE workflows, enabling seamless integration with external applications.

## Overview

HTTP trigger enables external applications to invoke workflows in CRE through a secure HTTP/JSON-RPC interface. It handles authentication, authorization, rate limiting, and request routing to ensure that only authorized users can trigger workflows and that the system remains stable under load.

Key features:
- JSON-RPC interface for triggering workflows
- JWT-based authentication and authorization using ECDSA signatures on secp256k1 curve
- Multi-level rate limiting (global, per-sender, per-workflow)
- Integration with Gateway nodes
- Support for workflow identification by ID or metadata (owner/name/label)

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
  "workflowLabel": "production-mainnet"
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

## Capability Configuration

The following values are default values for the capability configuration:

```json
{
  "sendChannelBufferSize": 1000,
  "incomingRateLimiter": {
    "perSenderRPS": 100.0,
    "perSenderBurst": 100,
    "globalRPS": 100.0,
    "globalBurst": 100
  },
  "outgoingRateLimiter": {
    "perSenderRPS": 100.0,
    "perSenderBurst": 100,
    "globalRPS": 100.0,
    "globalBurst": 100
  }
}
```
