package node

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/smartcontractkit/capabilities/libs/cli/constants"
)

type nodeProcess struct {
	cmd     *exec.Cmd
	logFile *os.File
}

type startNodesArgs struct {
	nodes int
	logs  bool
}

func startNodes(args startNodesArgs) error {
	var nodeProcesses []nodeProcess

	for i := 0; i < args.nodes; i++ {
		nodeID := i + 1
		nodeDir := getNodeDir(nodeID)
		err := os.MkdirAll(nodeDir, os.ModePerm)
		if err != nil {
			return fmt.Errorf("failed to create node directory: %v", err)
		}

		lockFilePath := filepath.Join(nodeDir, constants.LockFile)
		if _, err := os.Stat(lockFilePath); err == nil {
			fmt.Printf("Node %d is already started (lock file exists)\n", nodeID)
			continue
		}

		httpPort, prometheusPort := getPorts(nodeID)
		nodeLogsFilepath := filepath.Join(nodeDir, constants.ChainlinkNodeLogsFilename)
		uiCredentialsFilepath := filepath.Join(nodeDir, constants.ChainlinkNodeUICredentialsFilename)
		secretsFilepath := filepath.Join(nodeDir, constants.ChainlinkNodeSecretsFilename)

		cmd := exec.Command(
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

		if args.logs {
			cmd.Stderr = io.MultiWriter(logFile, os.Stderr)
		} else {
			cmd.Stderr = logFile
		}
		cmd.Stdout = os.Stdout // Optionally redirect stdout to terminal

		// Output messages
		fmt.Println("--------------------------------------------------")
		fmt.Printf("Started Chainlink Node %d\n", nodeID)
		fmt.Println("--------------------------------------------------")
		fmt.Printf("Operator UI:\thttp://localhost:%d (credentials: %s)\n", httpPort, uiCredentialsFilepath)
		fmt.Printf("Prometheus:\thttp://localhost:%d\n", prometheusPort)
		fmt.Printf("Logs:\t\t%s\n", nodeLogsFilepath)
		fmt.Println("--------------------------------------------------")

		// Start the Chainlink node process
		err = cmd.Start()
		if err != nil {
			logFile.Close()
			return fmt.Errorf("failed to start Chainlink node: %v", err)
		}

		// Store the PID in a .lock file
		err = os.WriteFile(lockFilePath, []byte(strconv.Itoa(cmd.Process.Pid)), 0600)
		if err != nil {
			logFile.Close()
			return fmt.Errorf("failed to write lock file: %v", err)
		}

		// Keep track of the command and the log file
		nodeProcesses = append(nodeProcesses, nodeProcess{
			cmd:     cmd,
			logFile: logFile,
		})
	}

	return nil
}
