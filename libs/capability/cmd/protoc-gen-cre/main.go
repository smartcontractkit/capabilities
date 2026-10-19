// Command protoc-gen-cre is a protoc plugin that generates a capability.Capability server for each proto
// service, in the same package as its messages, and the production protos of every file, into production, see
// production.Generate. Both come from one plugin so a capability can't be generated without its production
// protos. Every file of the capability must be generated in one run.
package main

import (
	_ "embed"
	"log"
	"os"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/types/pluginpb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/protoc/pkg"

	"github.com/smartcontractkit/capabilities/libs/capability/cmd/protoc-gen-cre/production"
)

const (
	toolName    = "github.com/smartcontractkit/capabilities/libs/capability/cmd/protoc-gen-cre"
	localPrefix = "github.com/smartcontractkit/capabilities"
)

//go:embed server.go.tmpl
var serverTemplate string

//go:embed instances.go.tmpl
var instancesTemplate string

var templates = map[string]string{
	"{{.}}_server_gen.go":               serverTemplate,
	"protostest/{{.}}_instances_gen.go": instancesTemplate,
}

func main() {
	protogen.Options{}.Run(func(plugin *protogen.Plugin) error {
		plugin.SupportedFeatures = uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL)

		for _, file := range plugin.Files {
			if !file.Generate || len(file.Services) == 0 {
				continue
			}

			for fileName, tmpl := range templates {
				template := &pkg.TemplateGenerator{
					Name:               fileName,
					Template:           tmpl,
					FileNameTemplate:   fileName,
					StringLblValue:     pkg.StringLblValue(true),
					PbLabelTLangLabels: pkg.PbLabelToGoLabels,
				}

				if err := template.GenerateFile(file, plugin, file, toolName, localPrefix); err != nil {
					log.Printf("failed to generate %s for %s: %v", fileName, file.Desc.Path(), err)
					os.Exit(1)
				}
			}
		}

		return production.Generate(plugin)
	})
}
