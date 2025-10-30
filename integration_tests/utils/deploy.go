package utils

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

const CapabilitiesDir = "integration_tests_temp"

// findBazelBinary attempts to locate a binary built by Bazel in the runfiles.
// Returns the path if found, empty string otherwise.
func findBazelBinary(t *testing.T, capabilityName string) string {
	// Check for Bazel test environment
	testSrcDir := os.Getenv("TEST_SRCDIR")
	testWorkspace := os.Getenv("TEST_WORKSPACE")
	runfilesDir := os.Getenv("RUNFILES_DIR")

	fmt.Println("testSrcDir: ", testSrcDir)
	fmt.Println("testWorkspace: ", testWorkspace)
	fmt.Println("RUNFILES_DIR: ", runfilesDir)

	if testSrcDir == "" || testWorkspace == "" {
		// Not running under Bazel
		fmt.Println("Not running under Bazel")
		return ""
	}

	fmt.Println("Running under Bazel")

	// Extract binary name from capabilityName
	// For "chain_capabilities/evm", the binary target is "evm"
	// For "readcontract", the binary target is "readcontract"
	binaryName := filepath.Base(capabilityName)

	// Bazel binaries are in runfiles at: {workspace}/{package}/{target}
	// For a binary target like //readcontract:readcontract, the path would be:
	// {TEST_SRCDIR}/{TEST_WORKSPACE}/readcontract/readcontract
	// For //chain_capabilities/evm:evm, it would be:
	// {TEST_SRCDIR}/{TEST_WORKSPACE}/chain_capabilities/evm/evm
	// Try multiple possible paths
	possiblePaths := []string{}

	// If RUNFILES_DIR is set, use it as base
	if runfilesDir != "" {
		possiblePaths = append(possiblePaths,
			// Bazel binary directory pattern (checked first as it's most specific)
			filepath.Join(runfilesDir, testWorkspace, capabilityName, binaryName+"_", binaryName),
			filepath.Join(runfilesDir, testWorkspace, capabilityName, binaryName),
			filepath.Join(runfilesDir, testWorkspace, capabilityName),
			filepath.Join(runfilesDir, capabilityName, binaryName),
			filepath.Join(runfilesDir, binaryName),
		)
	}

	// Standard TEST_SRCDIR paths
	possiblePaths = append(possiblePaths,
		// Most common: workspace/package/target
		filepath.Join(testSrcDir, testWorkspace, capabilityName, binaryName),
		// Bazel binary directory pattern: workspace/package/binaryName_/binaryName
		filepath.Join(testSrcDir, testWorkspace, capabilityName, binaryName+"_", binaryName),
		// Just package/target in workspace
		filepath.Join(testSrcDir, testWorkspace, capabilityName),
		// Alternative structures
		filepath.Join(testSrcDir, capabilityName, binaryName),
		filepath.Join(testSrcDir, capabilityName),
		// Sometimes binaries are directly accessible
		filepath.Join(testSrcDir, testWorkspace, binaryName),
		filepath.Join(testSrcDir, binaryName),
	)

	// Also check RUNFILES_DIR with the binaryName_ pattern if available
	if runfilesDir != "" && testWorkspace != "" {
		possiblePaths = append(possiblePaths,
			filepath.Join(runfilesDir, testWorkspace, capabilityName, binaryName+"_", binaryName),
		)
	}

	fmt.Println("Possible paths: ", possiblePaths)

	for _, binaryPath := range possiblePaths {
		if info, err := os.Stat(binaryPath); err == nil {
			// Check if it's actually a file (not a directory)
			if !info.IsDir() {
				fmt.Println("Found Bazel binary: ", binaryPath)
				return binaryPath
			}
		} else {
			// Debug: print the error for first few failed attempts
			if len(possiblePaths) <= 10 {
				fmt.Printf("Path %s: %v\n", binaryPath, err)
			}
		}
	}

	// If still not found, try to list the runfiles directory to help debug
	if runfilesDir != "" {
		if entries, err := os.ReadDir(runfilesDir); err == nil {
			fmt.Printf("Contents of RUNFILES_DIR (%s): ", runfilesDir)
			for i, entry := range entries {
				if i < 20 { // Limit output but show more
					entryType := "dir"
					if entry.Type().IsRegular() {
						entryType = "file"
					}
					fmt.Printf("%s[%s] ", entry.Name(), entryType)
				}
			}
			fmt.Println()

			// If workspace is known, check that subdirectory too
			if testWorkspace != "" {
				workspaceRunfilesPath := filepath.Join(runfilesDir, testWorkspace)
				if entries, err := os.ReadDir(workspaceRunfilesPath); err == nil {
					fmt.Printf("Contents of %s: ", workspaceRunfilesPath)
					for i, entry := range entries {
						if i < 20 {
							entryType := "dir"
							if entry.Type().IsRegular() {
								entryType = "file"
							}
							fmt.Printf("%s[%s] ", entry.Name(), entryType)
						}
					}
					fmt.Println()

					// Also check the specific capability directory
					if capabilityName != "" {
						capabilityRunfilesPath := filepath.Join(workspaceRunfilesPath, capabilityName)
						if entries, err := os.ReadDir(capabilityRunfilesPath); err == nil {
							fmt.Printf("Contents of %s: ", capabilityRunfilesPath)
							for i, entry := range entries {
								if i < 20 {
									entryType := "dir"
									if entry.Type().IsRegular() {
										entryType = "file"
									}
									fmt.Printf("%s[%s] ", entry.Name(), entryType)

									// Bazel binaries often have a directory with underscore suffix
									// Check inside directories that match the pattern: binaryName_
									if entry.IsDir() && len(entry.Name()) > len(binaryName) && entry.Name()[:len(binaryName)] == binaryName && entry.Name()[len(binaryName)] == '_' {
										subDirPath := filepath.Join(capabilityRunfilesPath, entry.Name())
										if subEntries, err := os.ReadDir(subDirPath); err == nil {
											fmt.Printf("\n  Contents of %s: ", subDirPath)
											for j, subEntry := range subEntries {
												if j < 20 {
													subType := "dir"
													if subEntry.Type().IsRegular() {
														subType = "file"
													}
													fmt.Printf("%s[%s] ", subEntry.Name(), subType)
												}
											}
											fmt.Println()
										}
									}
								}
							}
							fmt.Println()
						} else {
							fmt.Printf("Could not read directory %s: %v\n", capabilityRunfilesPath, err)
						}
					}
				} else {
					fmt.Printf("Could not read directory %s: %v\n", workspaceRunfilesPath, err)
				}
			}
		} else {
			fmt.Printf("Could not read RUNFILES_DIR %s: %v\n", runfilesDir, err)
		}
	}

	// Also try listing TEST_SRCDIR
	if testWorkspace != "" {
		workspacePath := filepath.Join(testSrcDir, testWorkspace)
		if entries, err := os.ReadDir(workspacePath); err == nil {
			fmt.Printf("Contents of %s: ", workspacePath)
			for i, entry := range entries {
				if i < 20 { // Limit output but show more
					fmt.Printf("%s ", entry.Name())
				}
			}
			fmt.Println()

			// Also check if the capabilityName directory exists
			if capabilityName != "" {
				capabilityPath := filepath.Join(workspacePath, capabilityName)
				if entries, err := os.ReadDir(capabilityPath); err == nil {
					fmt.Printf("Contents of %s: ", capabilityPath)
					for i, entry := range entries {
						if i < 20 {
							entryType := "dir"
							if entry.Type().IsRegular() {
								entryType = "file"
							}
							fmt.Printf("%s[%s] ", entry.Name(), entryType)
						}
					}
					fmt.Println()
				} else {
					fmt.Printf("Could not read directory %s: %v\n", capabilityPath, err)
				}
			}
		} else {
			fmt.Printf("Could not read directory %s: %v\n", workspacePath, err)
		}
	}

	// List TEST_SRCDIR root too
	if entries, err := os.ReadDir(testSrcDir); err == nil {
		fmt.Printf("Contents of TEST_SRCDIR root (%s): ", testSrcDir)
		for i, entry := range entries {
			if i < 20 {
				fmt.Printf("%s ", entry.Name())
			}
		}
		fmt.Println()
	}

	fmt.Println("No Bazel binary found")

	return ""
}

// DeployCapability returns the path to the capability binary.
// If running under Bazel, it uses the pre-built binary from runfiles.
// Otherwise, it builds the binary using go build.
func DeployCapability(t *testing.T, capabilityName string) (string, error) {
	// First, try to find a Bazel-built binary
	if bazelPath := findBazelBinary(t, capabilityName); bazelPath != "" {
		return bazelPath, nil
	}

	// Fall back to building with go build (non-Bazel mode)
	projectPath := "../../" + capabilityName
	outputBinary := CapabilitiesDir + "/" + capabilityName
	absoluteBinaryPath, err := filepath.Abs(outputBinary)
	require.NoError(t, err)

	fmt.Println("Building capability with go build: ", absoluteBinaryPath)

	cmd := exec.Command("go", "build", "-gcflags", "all=-N -l", "-o", absoluteBinaryPath)
	cmd.Dir = projectPath
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))

	return absoluteBinaryPath, nil
}

// CleanupCapabilitiesDir removes any capabilities built by the test
func CleanupCapabilitiesDir(lggr logger.Logger) {
	err := os.RemoveAll(CapabilitiesDir)
	if err != nil {
		lggr.Errorf("Failed to remove directory: %v", err)
	}
}
