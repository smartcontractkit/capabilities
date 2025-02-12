package utils

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink/v2/core/logger"
)

const capabilitiesDir = "integration_tests_temp"

// DeployCapability builds the capability returns the path to the binary
func DeployCapability(t *testing.T, capabilityName string) (string, error) {
	projectPath := "../../" + capabilityName
	absoluteProjectPath, err := filepath.Abs(projectPath)
	require.NoError(t, err)

	outputBinary := capabilitiesDir + "/" + capabilityName
	absoluteBinaryPath, err := filepath.Abs(outputBinary)
	require.NoError(t, err)

	cmd := exec.Command("go", "build", "-o", absoluteBinaryPath)
	cmd.Dir = absoluteProjectPath
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))

	return absoluteBinaryPath, nil
}

// CleanupCapabilitiesDir removes any capabilities built by the test
func CleanupCapabilitiesDir(lggr logger.Logger) {
	err := os.RemoveAll(capabilitiesDir)
	if err != nil {
		lggr.Errorf("Failed to remove directory: %v", err)
	}
}
