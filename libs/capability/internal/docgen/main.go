// Command docgen generates the DocComments for this package's config sections, which the flags'
// help text is read from.
package main

import (
	"log"

	"github.com/smartcontractkit/chainlink-common/x/config/commentparsing"
	"github.com/smartcontractkit/chainlink-common/x/config/markup/tomlmarkup"

	"github.com/smartcontractkit/capabilities/libs/capability"
)

func main() {
	if err := commentparsing.Run(commentparsing.RunArgs{
		Markup: tomlmarkup.New(),
		Roots: []any{
			&capability.AppConfig{},
			&capability.HTTPConfig{},
			&capability.CapabilitiesConfig{},
			&capability.SettingsConfig{},
		},
		Tool:        "github.com/smartcontractkit/capabilities/libs/capability/internal/docgen",
		LocalPrefix: "github.com/smartcontractkit",
	}); err != nil {
		log.Fatal(err)
	}
}
