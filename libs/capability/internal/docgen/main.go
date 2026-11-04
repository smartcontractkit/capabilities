// Command docgen generates the DocComments for every config in this module, which the flags' help text is
// read from.
package main

import (
	"log"

	"github.com/smartcontractkit/chainlink-common/x/config/commentparsing"
	"github.com/smartcontractkit/chainlink-common/x/config/markup/tomlmarkup"

	"github.com/smartcontractkit/capabilities/libs/capability"
	"github.com/smartcontractkit/capabilities/libs/capability/cmd/newcapability/options"
	"github.com/smartcontractkit/capabilities/libs/capability/internal/example/capability/service"
)

func main() {
	if err := commentparsing.Run(commentparsing.RunArgs{
		Markup: tomlmarkup.New(),
		Roots: []any{
			&capability.AppConfig{},
			&capability.PrometheusConfig{},
			&capability.HTTPConfig{},
			&capability.CapabilitiesConfig{},
			&capability.SettingsConfig{},
			&options.Options{},
			&service.Config{},
		},
		Tool:        "github.com/smartcontractkit/capabilities/libs/capability/internal/docgen",
		LocalPrefix: "github.com/smartcontractkit",
	}); err != nil {
		log.Fatal(err)
	}
}
