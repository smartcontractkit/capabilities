// Command protoc-gen-cre generates the server that serves a capability's proto
// service: the typed interface a capability implements, and the untyped
// capabilities.ExecutableAndTriggerCapability a host registers and serves.
//
// It is chainlink-common's plugin without the parts a host does: nothing here
// initialises a capability, adds it to a registry or takes it back out, because
// the standalone bootstrapper (libs/standalone/capability) is what hosts a
// capability and already does all three. What is left is the translation between
// the untyped capability API and the typed methods generated from the proto,
// which is the only part that has to be generated per service.
//
// The generated file goes in the same package as the messages it is generated
// alongside - a capability's protos directory - so a capability is one package
// rather than a package and a server subpackage of it.
package main

import (
	_ "embed"
	"log"
	"os"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/types/pluginpb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/protoc/pkg"
)

const (
	toolName    = "github.com/smartcontractkit/capabilities/libs/standalone/protohelpers/protoc"
	localPrefix = "github.com/smartcontractkit/capabilities"
)

//go:embed server.go.tmpl
var serverTemplate string

func main() {
	protogen.Options{}.Run(func(plugin *protogen.Plugin) error {
		plugin.SupportedFeatures = uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL)

		for _, file := range plugin.Files {
			// A file with no service carries messages only, and has no server
			// to generate - the Go plugin has already generated all of it.
			if !file.Generate || len(file.Services) == 0 {
				continue
			}

			template := &pkg.TemplateGenerator{
				Name:               "capability_server",
				Template:           serverTemplate,
				FileNameTemplate:   "{{.}}_server_gen.go",
				StringLblValue:     pkg.StringLblValue(true),
				PbLabelTLangLabels: pkg.PbLabelToGoLabels,
			}

			if err := template.GenerateFile(file, plugin, file, toolName, localPrefix); err != nil {
				log.Printf("failed to generate for %s: %v", file.Desc.Path(), err)
				os.Exit(1)
			}
		}

		return nil
	})
}
