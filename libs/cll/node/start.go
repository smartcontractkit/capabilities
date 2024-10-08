package node

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/smartcontractkit/capabilities/libs/cli/constants"
	"github.com/smartcontractkit/capabilities/libs/cli/utils"
)

type startNodesArgs struct {
	nodeIDs []int
	logs    bool
}

func startNodes(args startNodesArgs) error {
	for i, nodeID := range args.nodeIDs {
		nodeDir := utils.GetNodeDir(nodeID)
		err := os.MkdirAll(nodeDir, os.ModePerm)
		if err != nil {
			return fmt.Errorf("failed to create node directory: %v", err)
		}

		lockFilePath := filepath.Join(nodeDir, constants.LockFile)
		if _, err := os.Stat(lockFilePath); err == nil {
			fmt.Printf("Node %d is already started (lock file exists)\n", nodeID)
			continue
		}

		nodeInfo := utils.GetNodeInfo(nodeID)

		nodeLogsFilepath := filepath.Join(nodeDir, constants.ChainlinkNodeLogsFilename)
		uiCredentialsFilepath := filepath.Join(nodeDir, constants.ChainlinkNodeUICredentialsFilename)
		secretsFilepath := filepath.Join(nodeDir, constants.ChainlinkNodeSecretsFilename)

		cmd := exec.Command( //nolint:gosec
			constants.ChainlinkBinaryLocation,
			"--config", filepath.Join(nodeDir, constants.ChainlinkNodeConfigFilename),
			"--secrets", secretsFilepath,
			"node", "start",
			"--password", filepath.Join(nodeDir, constants.ChainlinkNodeKeystorePasswordFile),
			"--api", uiCredentialsFilepath,
		)

		// Redirect stderr to log file
		logFile, err := os.OpenFile(nodeLogsFilepath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			return fmt.Errorf("failed to open log file: %v", err)
		}
		cmd.Stdout = os.Stdout // Optionally redirect stdout to terminal

		if args.logs {
			cmd.Stderr = io.MultiWriter(os.Stderr, logFile)
			// Start the Chainlink node process
			err = cmd.Run()
			if err != nil {
				logFile.Close()
				return fmt.Errorf("failed to start Chainlink node: %v", err)
			}
		} else {
			cmd.Stderr = logFile
			// Start the Chainlink node process
			err = cmd.Start()
			if err != nil {
				logFile.Close()
				return fmt.Errorf("failed to start Chainlink node: %v", err)
			}
		}

		// Store the PID in a .lock file
		err = os.WriteFile(lockFilePath, []byte(strconv.Itoa(cmd.Process.Pid)), 0600)
		if err != nil {
			logFile.Close()
			return fmt.Errorf("failed to write lock file: %v", err)
		}

		// Output messages
		fmt.Println("--------------------------------------------------")
		fmt.Printf("Started Chainlink Node %d\n", nodeID)
		fmt.Println("--------------------------------------------------")
		fmt.Printf("Operator UI:\t%s (credentials: %s)\n", nodeInfo.URLs.HTTP, uiCredentialsFilepath)
		fmt.Printf("Prometheus:\t%s\n", nodeInfo.URLs.Prometheus)
		fmt.Printf("Logs:\t\t%s\n", nodeLogsFilepath)

		if i+1 == len(args.nodeIDs) {
			fmt.Println("--------------------------------------------------")
		}
	}

	return nil
}
