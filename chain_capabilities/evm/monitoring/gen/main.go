package main

import (
	"github.com/smartcontractkit/chainlink-protos/cre/go/installer/pkg"
)

func main() {
	gen := &pkg.ProtocGen{Plugins: []pkg.Plugin{pkg.GoPlugin}}
	gen.AddSourceDirectories(".")
	gen.AddSourceDirectories("../../../.")

	// needed by the proto generator
	gen.LinkPackage(pkg.Packages{Go: "github.com/smartcontractkit/chainlink-protos/cre/go/tools/generator", Proto: "tools/generator/v1alpha/cre_metadata.proto"})
	gen.LinkPackage(pkg.Packages{Go: "github.com/smartcontractkit/chainlink-protos/cre/go/sdk", Proto: "sdk/v1alpha/sdk.proto"})

	// direct used protos
	gen.LinkPackage(pkg.Packages{Go: "github.com/smartcontractkit/capabilities/libs/monitoring", Proto: "libs/monitoring/execution_context.proto"})
	gen.LinkPackage(pkg.Packages{Go: "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm", Proto: "capabilities/blockchain/evm/v1alpha/client.proto"})

	if err := gen.GenerateFile("log_trigger.proto", "."); err != nil {
		panic(err.Error())
	}
}
