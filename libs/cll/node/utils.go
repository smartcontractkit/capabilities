package node

import (
	"fmt"
	"path/filepath"

	"github.com/smartcontractkit/capabilities/libs/cli/constants"
)

func getNodeDBName(nodeID int) string {
	return fmt.Sprintf("node_db_%d", nodeID)
}

func getNodeDir(nodeID int) string {
	return filepath.Join(constants.LocalDir, fmt.Sprintf("node-%d", nodeID))
}
