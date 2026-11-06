// Command docgen generates the DocComments for the configs in libs/ocr, which the flags' help text is read from.
package main

import (
	"log"

	"github.com/smartcontractkit/chainlink-common/x/config/commentparsing"
	"github.com/smartcontractkit/chainlink-common/x/config/markup/tomlmarkup"

	"github.com/smartcontractkit/capabilities/libs/ocr"
)

func main() {
	if err := commentparsing.Run(commentparsing.RunArgs{
		Markup:      tomlmarkup.New(),
		Roots:       []any{&ocr.Config{}},
		Tool:        "github.com/smartcontractkit/capabilities/libs/ocr/internal/docgen",
		LocalPrefix: "github.com/smartcontractkit",
	}); err != nil {
		log.Fatal(err)
	}
}
