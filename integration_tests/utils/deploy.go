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
	outputBinary := capabilitiesDir + "/" + capabilityName
	absoluteBinaryPath, err := filepath.Abs(outputBinary)
	require.NoError(t, err)

	cmd := exec.Command("go", "build", "-o", absoluteBinaryPath)
	cmd.Dir = projectPath
	err = cmd.Run()
	require.NoError(t, err)

	return absoluteBinaryPath, nil
}

// CleanupCapabilitiesDir removes any capabilities built by the test
func CleanupCapabilitiesDir(lggr logger.Logger) {
	err := os.RemoveAll(capabilitiesDir)
	if err != nil {
		lggr.Errorf("Failed to remove directory: %v", err)
	}
}
