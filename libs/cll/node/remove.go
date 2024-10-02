package node

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/smartcontractkit/capabilities/libs/cli/constants"
)

func removeNodes(nodes int) error {
	for i := 0; i < nodes; i++ {
		nodeID := i + 1
		nodeDir := getNodeDir(nodeID)

		if _, err := os.Stat(nodeDir); !os.IsNotExist(err) {
			lockFilePath := filepath.Join(nodeDir, constants.LockFile)
			if _, err := os.Stat(lockFilePath); err == nil {
				return fmt.Errorf("node %d is running (lock file exists)", nodeID)
			}

			// Directory exists and node is not running
			err = os.RemoveAll(nodeDir)
			if err != nil {
				return fmt.Errorf("failed to remove directory %s: %v", nodeDir, err)
			}

			// Check if the database exists
			checkDBCmd := []string{
				"-U", "postgres",
				"-tAc", fmt.Sprintf("SELECT 1 FROM pg_database WHERE datname='%s'", getNodeDBName(nodeID)),
			}
			dbCheckOutput, err := execCommand("psql", checkDBCmd...)
			if err != nil {
				return fmt.Errorf("failed to check database: %v", err)
			}

			dbExists := strings.TrimSpace(string(dbCheckOutput)) == "1"

			// Drop the database if it exists
			if dbExists {
				dropDBCmd := []string{
					"-U", "postgres",
					"-c", fmt.Sprintf("DROP DATABASE %s;", getNodeDBName(nodeID)),
				}
				_, err = execCommand("psql", dropDBCmd...)
				if err != nil {
					return fmt.Errorf("failed to drop database: %v", err)
				}
			}

			fmt.Printf("Chainlink Node %d removed! (%s directory, %s database)\n", nodeID, getNodeDir(nodeID), getNodeDBName(nodeID))
		} else {
			// Directory does not exist
			fmt.Printf("Chainlink Node %d not found! (%s directory)\n", nodeID, nodeDir)
		}
	}

	return nil
}
