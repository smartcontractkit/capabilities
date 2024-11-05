package main

import (
	"github.com/smartcontractkit/capabilities/kvstore/server"
	"github.com/smartcontractkit/capabilities/libs/loopserver"
)

func main() {
	loopserver.Serve("KVStoreCapabilities", server.New)
}
