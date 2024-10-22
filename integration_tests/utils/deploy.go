package utils

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink/v2/core/logger"
)

const capabilitiesDir = "integration_tests_temp"

// DeployCapability builds the capability returns the path to the binary
func DeployCapability(t *testing.T, capabilityName string) (string, error) {
	projectPath := "../../" + capabilityName
	outputBinary := "../" + capabilitiesDir + "/" + capabilityName

	cmd := exec.Command("go", "build", "-o", outputBinary)
	cmd.Dir = projectPath
	err := cmd.Run()

	require.NoError(t, err)
	return "../../" + capabilitiesDir + "/" + capabilityName, err
}

// CleanupCapabilities removes any capabilities built by the test
func CleanupCapabilities(lggr logger.Logger) {
	err := os.RemoveAll("../../" + capabilitiesDir)
	if err != nil {
		lggr.Errorf("Failed to remove directory: %v", err)
	}
}
