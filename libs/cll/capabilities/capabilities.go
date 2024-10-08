package capabilities

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/urfave/cli/v2"

	"github.com/smartcontractkit/capabilities/libs/cli/constants"
	"github.com/smartcontractkit/capabilities/libs/cli/node"
	"github.com/smartcontractkit/capabilities/libs/cli/utils"
)

var Commands = []*cli.Command{
	{
		Name:  "capabilities",
		Usage: "Commands to manage the capabilities of the local nodes",
		Subcommands: []*cli.Command{
			{
				Name:  "add",
				Usage: "Add capabilities to the local node",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:     "bootstrap-node-id",
						Usage:    "Bootstrap Node ID",
						Required: true,
					},
					&cli.StringSliceFlag{
						Aliases:  []string{"c"},
						Name:     "capabilities",
						Usage:    "List of capabilities to add to the local node",
						Required: true,
					},
					&cli.IntSliceFlag{
						Aliases:  []string{"n"},
						Name:     "nodeIDs",
						Usage:    "Node IDs to add capabilities to",
						Required: true,
					},
				},
				Action: func(c *cli.Context) error {
					capabilities := c.StringSlice("capabilities")
					nodeIDs := c.IntSlice("nodeIDs")
					bootstrapNodeID := c.Int("bootstrap-node-id")

					bootstrapNodeInfo := utils.GetNodeInfo(bootstrapNodeID)
					bootstrapPublicKeys, err := node.GetPublicKeys(bootstrapNodeID)
					if err != nil {
						return fmt.Errorf("failed to get public keys for bootstrap node: %w", err)
					}

					for _, nodeID := range nodeIDs {
						err := node.Login(nodeID)

						if err != nil {
							return fmt.Errorf("failed to login to node %d: %w", nodeID, err)
						}

						nodeInfo := utils.GetNodeInfo(nodeID)

						for _, name := range capabilities {
							binariesDir, err := utils.GetBinariesDir()
							if err != nil {
								return fmt.Errorf("failed to get binaries directory: %w", err)
							}

							capabilitiesSpecPath := filepath.Join(
								nodeInfo.Paths.Capabilities,
								fmt.Sprintf("%s_capabilities_spec.toml", name),
							)
							capabilitiesBinaryPath := filepath.Join(binariesDir, name)

							capabilitiesSpec := fmt.Sprintf(
								`type = "standardcapabilities"
schemaVersion = 1
name = "%s-capabilities"
command="%s"
config = '''{"enabled": true, "traceLogging": true, "bootstrapPeers": [ "%s@localhost:%d" ]}'''
`, name, capabilitiesBinaryPath, bootstrapPublicKeys.P2PPeerID, bootstrapNodeInfo.Ports.P2P)

							err = os.WriteFile(capabilitiesSpecPath, []byte(capabilitiesSpec), 0600)
							if err != nil {
								return err
							}

							nodeInfo := utils.GetNodeInfo(nodeID)
							// Login to the node
							output, err := utils.ExecCommand(
								constants.ChainlinkBinaryLocation,
								"--remote-node-url", nodeInfo.URLs.HTTP,
								"--admin-credentials-file", nodeInfo.Paths.Credentials,
								"jobs", "create",
								capabilitiesSpecPath,
							)

							if err != nil {
								return fmt.Errorf("failed to add %s capabilities to node %d: %w", name, nodeID, err)
							}

							// TODO: Bring back once job deletion also removes the capabilities from the registry

							fmt.Println("output", string(output))
							// TODO: Correctly handle error responses from CLI
							// TODO: Store job ID so it is easier to remove

							fmt.Printf("Added %s capabilities to node %d\n", name, nodeID)
						}
					}

					return nil
				},
			},
		},
	},
}
