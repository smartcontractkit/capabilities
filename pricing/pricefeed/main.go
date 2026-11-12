package main

//go:generate go run . docgen

import (
	"github.com/smartcontractkit/capabilities/libs/capability"
	"github.com/smartcontractkit/capabilities/pricing/pricefeed/service"
)

func main() {
	capability.Run("pricefeed", service.New)
}
