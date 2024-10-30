package main

import (
	"github.com/smartcontractkit/capabilities/libs/cll/capabilities/execution"

	"github.com/smartcontractkit/capabilities/kvstore/server"
)

const (
	serviceName = "KVStoreCapabilities"
)

func main() {
	execution.RunCapability(serviceName, server.New)
}
