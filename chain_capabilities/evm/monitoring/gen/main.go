package main

import "github.com/smartcontractkit/chainlink-common/pkg/values/installer/pkg"

func main() {
	gen := &pkg.ProtocGen{}
	gen.AddSourceDirectories(".")          //current monitoring directory
	gen.AddSourceDirectories("../../../.") //go up to find libs/monitoring/execution_context proto

	// needed by the proto generator
	gen.LinkPackage(pkg.Packages{Go: "github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk/v2/pb", Proto: "tools/generator/v1alpha/cre_metadata.proto"})
	gen.LinkPackage(pkg.Packages{Go: "github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk/v2/pb", Proto: "sdk/v1alpha/sdk.proto"})

	// direct used protos
	gen.LinkPackage(pkg.Packages{Go: "github.com/smartcontractkit/capabilities/libs/monitoring", Proto: "libs/monitoring/execution_context.proto"})
	gen.LinkPackage(pkg.Packages{Go: "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm", Proto: "capabilities/blockchain/evm/v1alpha/client.proto"})
	//gen.LinkPackage(pkg.Packages{Go: "github.com/smartcontractkit/chainlink-common/pkg/values/pb", Proto: "values/v1/values.proto"})
	if err := gen.GenerateFile("log_trigger.proto", "."); err != nil {
		panic(err.Error())
	}
}
