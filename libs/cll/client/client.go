package client

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/urfave/cli/v2"
)

var Commands = []*cli.Command{
	{
		Name:  "client",
		Usage: "Commands to manage the local chainlink client (binary)",
		Subcommands: []*cli.Command{
			{
				Name:  "build",
				Usage: "Build local chainlink client (binary)",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Aliases:  []string{"d"},
						Name:     "dir",
						Usage:    "Location of the local Chainlink repository",
						EnvVars:  []string{"CLL_CHAINLINK_DIRECTORY"},
						Required: true,
					},
					&cli.StringFlag{
						Name:  "version",
						Value: "1.0.0",
						Usage: "Version to set in the Chainlink binary",
					},
					&cli.StringFlag{
						Name:  "output",
						Value: "./bin/chainlink", // Update default path as needed
						Usage: "Output location for the Chainlink binary",
					},
				},
				Action: func(c *cli.Context) error {
					version := c.String("version")
					output := c.String("output")
					dir := c.String("dir")

					absDir, err := filepath.Abs(output)
					if err != nil {
						return fmt.Errorf("failed to get absolute path: %v", err)
					}

					fmt.Printf("Building Chainlink client... ")

					ldflags := fmt.Sprintf("-X github.com/smartcontractkit/chainlink/v2/core/static.Version=%s", version)
					cmd := exec.Command(
						"go", "build",
						"-C", dir,
						"-ldflags", ldflags,
						"-o", absDir,
					)

					cmd.Stdout = os.Stdout
					cmd.Stderr = os.Stderr

					err = cmd.Run()
					if err != nil {
						return fmt.Errorf("failed to build Chainlink binary: %v", err)
					}

					fmt.Printf("Done. Client built in %s.\n", absDir)
					return nil
				},
			},
		},
	},
}
