# Ring Capability

The Ring capability implements a distributed consistent hashing ring for routing requests across multiple nodes in a DON (Decentralized Oracle Network).

## Overview

This capability uses OCR3 (Off-Chain Reporting 3.0) to reach consensus on:
- Ring membership and health status
- Request routing decisions
- Scaling the number of active rings

## Features

- **Consistent Hashing**: Uses consistent hashing to distribute requests evenly across rings
- **Health Monitoring**: Tracks the health of each ring and adjusts routing accordingly
- **Dynamic Scaling**: Supports scaling up/down the number of rings based on demand
- **Request Routing**: Routes requests to specific rings with expiration times

## Architecture

The Ring capability consists of:
- **Ring Plugin**: OCR3 reporting plugin that implements consensus logic
- **Environment Scaler**: Tracks desired ring count and health status
- **Request Store**: Stores pending routing requests
- **Consistent Hash Ring**: Distributes requests across available rings

## Protocol

### Observation Phase
Each node observes:
- Current ring health status
- Pending routing requests
- Timestamp

### Outcome Phase
The consensus determines:
- Next routing state (stable or transitioning)
- Request-to-ring mappings
- Expiration times for routes

### Report Phase
Reports contain:
- Current routing state
- Request routes with expiration times

