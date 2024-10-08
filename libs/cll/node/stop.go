package node

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

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

		err = process.Signal(os.Interrupt)

		if err != nil && errors.Is(err, os.ErrProcessDone) {
			fmt.Printf("failed to interrupt node: %v", err)

			if err2 := process.Kill(); err2 != nil {
				return fmt.Errorf("failed to kill node: %v", err2)
			}
		}

		err = os.Remove(lockFilePath)
		if err != nil {
			return fmt.Errorf("failed to remove lock file: %v", err)
		}

		fmt.Printf("Stopped Chainlink Node %d (PID %d)\n", nodeID, pid)
	}

	return nil
}
