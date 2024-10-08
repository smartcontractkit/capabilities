package utils

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/smartcontractkit/capabilities/libs/cli/constants"
)

func GetNodeDBName(nodeID int) string {
	return fmt.Sprintf("%s_%d", constants.LocalDbPrefix, nodeID)
}

func GetNodeDir(nodeID int) string {
	return filepath.Join(constants.LocalDir, fmt.Sprintf("node-%d", nodeID))
}

func GetBinariesDir() (string, error) {
	return filepath.Abs(constants.BinaryDir)
}

type Ports struct {
	HTTP       int
	P2P        int
	Prometheus int
}

func GetPorts(nodeID int) Ports {
	return Ports{
		HTTP:       6688 + nodeID,
		P2P:        8000 + nodeID,
		Prometheus: 5680 + nodeID,
	}
}

func Contains(output []byte, substr string) bool {
	return strings.Contains(string(output), substr)
}

// execCommand executes a command and captures its output and errors.
func ExecCommand(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return output, fmt.Errorf("%s: %s", err, stderr.String())
	}
	return output, nil
}

type Paths struct {
	Capabilities string
	Credentials  string
	Dir          string
	Jobs         string
	PublicKeys   string
}

type URLs struct {
	HTTP       string
	P2P        string
	Prometheus string
}

type NodeInfo struct {
	Paths Paths
	Ports Ports
	URLs  URLs
}

func GetNodeInfo(nodeID int) NodeInfo {
	ports := Ports{
		HTTP:       6688 + nodeID,
		P2P:        8000 + nodeID,
		Prometheus: 5680 + nodeID,
	}
	nodeDir := GetNodeDir(nodeID)
	return NodeInfo{
		Paths: Paths{
			Capabilities: filepath.Join(nodeDir, constants.ChainlinkNodeCapabilitiesDir),
			Credentials:  filepath.Join(nodeDir, constants.ChainlinkNodeUICredentialsFilename),
			Dir:          nodeDir,
			Jobs:         filepath.Join(nodeDir, constants.ChainlinkNodeJobsDir),
			PublicKeys:   filepath.Join(nodeDir, constants.ChainlinkNodePublicKeysFilename),
		},
		Ports: ports,
		URLs: URLs{
			HTTP:       fmt.Sprintf("http://localhost:%d", ports.HTTP),
			P2P:        fmt.Sprintf("http://localhost:%d", ports.P2P),
			Prometheus: fmt.Sprintf("http://localhost:%d", ports.Prometheus),
		},
	}
}
