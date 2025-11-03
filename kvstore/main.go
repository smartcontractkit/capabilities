package main

import (
	"github.com/smartcontractkit/capabilities/kvstore/server"
	"github.com/smartcontractkit/capabilities/libs/loopserver"

	"github.com/smartcontractkit/chainlink-common/pkg/loop"
)

func main() {
	// TODO: Remove before merging to main.
	// Uncomment to see the static analysis violation error from nogo
	// s := []string{"a", "b", "c"}
	// _ = append(s) // This is a no-op, as no elements are being added.

	loopserver.ServeNew("KVStoreCapabilities", func(s *loop.Server) loop.StandardCapabilities {
		return server.New(s.Logger)
	})
}
