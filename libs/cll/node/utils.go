package node

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/smartcontractkit/capabilities/libs/cli/constants"
)

func getNodeDBName(nodeID int) string {
	return fmt.Sprintf("%s_%d", constants.LocalDbPrefix, nodeID)
}

func getNodeDir(nodeID int) string {
	return filepath.Join(constants.LocalDir, fmt.Sprintf("node-%d", nodeID))
}

func getPorts(nodeID int) (int, int) {
	const (
		HTTPPortBase = 6688
		P2PPortBase  = 8000
	)
	HTTPPort := HTTPPortBase + nodeID
	P2PPort := P2PPortBase + nodeID
	return HTTPPort, P2PPort
}

func contains(output []byte, substr string) bool {
	return strings.Contains(string(output), substr)
}

// execCommand executes a command and captures its output and errors.
func execCommand(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return output, fmt.Errorf("%s: %s", err, stderr.String())
	}
	return output, nil
}
