package chain

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/smartcontractkit/capabilities/libs/cli/constants"
	"github.com/urfave/cli/v2"
)

const localDir = ".local/chain"

func startAnvil() error {
	// Ensure the local directory exists
	if err := os.MkdirAll(localDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create local directory: %v", err)
	}

	lockFilePath := filepath.Join(localDir, constants.LockFile)
	if _, err := os.Stat(lockFilePath); err == nil {
		data, err := os.ReadFile(lockFilePath)
		if err != nil {
			return fmt.Errorf("failed to read lock file: %v", err)
		}
		pid, err := strconv.Atoi(string(data))
		if err != nil {
			return fmt.Errorf("failed to read PID from lock file: %v", err)
		}

		fmt.Printf("Chain is already started on PID %s (lock file exists)\n", pid)
		return nil
	}

	// SPIKE: Investigate if we can replace `anvil`
	// TODO: Start and kill anvil with a flag to dump state and info
	// This way, we can automate copying of anvil variables instead of having to do it manually

	// Start anvil in the background
	cmd := exec.Command("anvil", "--silent")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start anvil: %v", err)
	}

	err := os.WriteFile(lockFilePath, []byte(strconv.Itoa(cmd.Process.Pid)), 0600)
	if err != nil {
		return fmt.Errorf("failed to write lock file: %v", err)
	}

	fmt.Printf("Chain started (PID %d)\n", cmd.Process.Pid)
	return nil
}

func stopAnvil() error {
	lockFilePath := filepath.Join(localDir, constants.LockFile)

	// Check if chain info file exists
	if _, err := os.Stat(lockFilePath); err == nil {
		data, err := os.ReadFile(lockFilePath)
		if err != nil {
			return fmt.Errorf("failed to read lock file: %v", err)
		}
		pid, err := strconv.Atoi(string(data))
		if err != nil {
			return fmt.Errorf("failed to read PID from lock file: %v", err)
		}

		process, err := os.FindProcess(pid)
		if err != nil {
			return fmt.Errorf("failed to find process: %v", err)
		}

		// Kill the process
		if err := process.Kill(); err != nil {
			return fmt.Errorf("failed to kill anvil: %v", err)
		}

		// Clean up the chain info file
		if err := os.Remove(lockFilePath); err != nil {
			return fmt.Errorf("failed to remove chain info file: %v", err)
		}

		fmt.Printf("Chain stopped (PID %d)\n", pid)
		return nil
	}

	fmt.Println("Anvil is not running.")
	return nil
}

var Commands = []*cli.Command{
	{
		Name:  "chain",
		Usage: "Commands to manage the local chain",
		Subcommands: []*cli.Command{
			{
				Name:  "start",
				Usage: "Start the anvil client",
				Action: func(c *cli.Context) error {
					return startAnvil()
				},
			},
			{
				Name:  "stop",
				Usage: "Stop the anvil client",
				Action: func(c *cli.Context) error {
					return stopAnvil()
				},
			},
			{
				Name:  "restart",
				Usage: "Restart the anvil client",
				Action: func(c *cli.Context) error {
					if err := stopAnvil(); err != nil {
						return err
					}
					return startAnvil()
				},
			},
		},
	},
}
