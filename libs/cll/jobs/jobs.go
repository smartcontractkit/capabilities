package jobs

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/urfave/cli/v2"

	"github.com/smartcontractkit/capabilities/libs/cli/chain"
	"github.com/smartcontractkit/capabilities/libs/cli/constants"
	"github.com/smartcontractkit/capabilities/libs/cli/evmcontracts"
	"github.com/smartcontractkit/capabilities/libs/cli/node"
	"github.com/smartcontractkit/capabilities/libs/cli/utils"
)

var Commands = []*cli.Command{
	{
		Name:  "jobs",
		Usage: "Commands to manage the jobs of the local nodes",
		Subcommands: []*cli.Command{
			{
				Name:  "add",
				Usage: "Add jobs to the local nodes",
				Flags: []cli.Flag{
					&cli.IntSliceFlag{
						Aliases:  []string{"n"},
						Name:     "nodeIDs",
						Usage:    "Node IDs to add capabilities to",
						Required: true,
					},
					&cli.StringFlag{
						Aliases:  []string{"j"},
						Name:     "job",
						Usage:    "Job to add to the nodes",
						Required: true,
						Value:    "bootstrap",
					},
				},
				Action: func(c *cli.Context) error {
					nodeIDs := c.IntSlice("nodeIDs")
					job := c.String("job")

					chainInfo := chain.GetInfo()
					ocrContractInfo, err := evmcontracts.GetInfo("ocr3")
					if err != nil {
						return fmt.Errorf("failed to get OCR contract info: %w", err)
					}

					for _, nodeID := range nodeIDs {
						err := node.Login(nodeID)

						if err != nil {
							return fmt.Errorf("failed to login to node %d: %w", nodeID, err)
						}

						nodeInfo := utils.GetNodeInfo(nodeID)

						// jobSpecLockPath := filepath.Join(
						// 	nodeInfo.Paths.Jobs,
						// 	fmt.Sprintf("%s_job_id.lock", job),
						// )

						// jobID, err := os.ReadFile(jobSpecLockPath)
						// if err == nil {
						// 	// Remove an existing job first
						// 	_, err2 := utils.ExecCommand(
						// 		constants.ChainlinkBinaryLocation,
						// 		"--remote-node-url", nodeInfo.URLs.HTTP,
						// 		"--admin-credentials-file", nodeInfo.Paths.Credentials,
						// 		"jobs", "remove",
						// 		string(jobID),
						// 	)

						// 	if err2 != nil {
						// 		return fmt.Errorf("failed to remove %s jobs from node %d: %w", job, nodeID, err2)
						// 	}

						// 	fmt.Printf("Removed %s job from node ID %d\n", job, nodeID)
						// } else {
						// 	if !os.IsNotExist(err) {
						// 		return fmt.Errorf("failed to read job ID lock file: %w", err)
						// 	}
						// 	// Else progress to adding the job
						// }

						jobSpecPath := filepath.Join(
							nodeInfo.Paths.Jobs,
							fmt.Sprintf("%s_job_spec.toml", job),
						)

						jobSpec := fmt.Sprintf(
							`type = "bootstrap"
schemaVersion = 1
name = "Botostrap"
contractID = "%s"
contractConfigTrackerPollInterval = "1s"
contractConfigConfirmations = 1
relay = "evm"

[relayConfig]
chainID = %d
`,
							ocrContractInfo.Address, chainInfo.ChainID)

						err = os.WriteFile(jobSpecPath, []byte(jobSpec), 0600)
						if err != nil {
							return err
						}

						// Login to the node
						output, err := utils.ExecCommand(
							constants.ChainlinkBinaryLocation,
							"--remote-node-url", nodeInfo.URLs.HTTP,
							"--admin-credentials-file", nodeInfo.Paths.Credentials,
							"jobs", "create",
							jobSpecPath,
						)

						if err != nil {
							return fmt.Errorf("failed to add %s jobs to node %d: %w", job, nodeID, err)
						}
						fmt.Println("output", string(output))

						// newJobID, err := ParseJobID(string(output))
						// if err != nil {
						// 	fmt.Println("output: ", string(output))
						// 	return fmt.Errorf("failed to parse job ID: %w", err)
						// }

						// if err = os.WriteFile(filepath.Join(nodeInfo.Paths.Jobs, jobSpecLockPath), []byte(newJobID), 0600); err != nil {
						// 	return fmt.Errorf("failed to write job ID to lock file: %w", err)
						// }

						fmt.Printf("Added %s jobs to node %d\n", job, nodeID)
					}

					return nil
				},
			},
		},
	},
}
