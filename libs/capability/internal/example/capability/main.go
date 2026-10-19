package main

import (
	"context"

	"github.com/smartcontractkit/capabilities/libs/capability"
	"github.com/smartcontractkit/capabilities/libs/capability/internal/example/capability/service"
)

func main() {
	capability.Run(context.Background(), service.New)
}
