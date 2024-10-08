package main

import (
	"log"
	"os"

	"github.com/urfave/cli/v2"

	"github.com/smartcontractkit/capabilities/libs/cli/capabilities"
	"github.com/smartcontractkit/capabilities/libs/cli/chain"
	"github.com/smartcontractkit/capabilities/libs/cli/client"
	"github.com/smartcontractkit/capabilities/libs/cli/evmcontracts"
	"github.com/smartcontractkit/capabilities/libs/cli/jobs"
	"github.com/smartcontractkit/capabilities/libs/cli/node"
)

func main() {
	var commands = make([]*cli.Command, 0)
	commands = append(commands, capabilities.Commands...)
	commands = append(commands, chain.Commands...)
	commands = append(commands, client.Commands...)
	commands = append(commands, evmcontracts.Commands...)
	commands = append(commands, jobs.Commands...)
	commands = append(commands, node.Commands...)

	app := &cli.App{
		Name:     "cll",
		Usage:    "Run capabilities in a local environment",
		Commands: commands,
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
