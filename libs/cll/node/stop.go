package node

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/smartcontractkit/capabilities/libs/cli/constants"
	"github.com/smartcontractkit/capabilities/libs/cli/utils"
)

func stopNodes(nodeIDs []int) error {
	for _, nodeID := range nodeIDs {
		nodeDir := utils.GetNodeDir(nodeID)

		lockFilePath := filepath.Join(nodeDir, constants.LockFile)
		pidData, err := os.ReadFile(lockFilePath)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Printf("Node %d is not running (lock file does not exist)\n", nodeID)
				continue
			}
			return fmt.Errorf("failed to read lock file: %v", err)
		}

		pid, err := strconv.Atoi(string(pidData))
		if err != nil {
			return fmt.Errorf("invalid PID in lock file: %v", err)
		}

		process, err := os.FindProcess(pid)
		if err != nil {
			return fmt.Errorf("failed to find process with PID %d: %v", pid, err)
		}

		err = process.Signal(syscall.SIGTERM)
		if err != nil && err.Error() != "os: process already finished" {
			return fmt.Errorf("failed to stop process with PID %d: %v", pid, err)
		}

		err = os.Remove(lockFilePath)
		if err != nil {
			return fmt.Errorf("failed to remove lock file: %v", err)
		}

		fmt.Printf("Stopped Chainlink Node %d (PID %d)\n", nodeID, pid)
	}

	return nil
}
