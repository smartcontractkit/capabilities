package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/urfave/cli/v2"
)

// File to store the process ID of the anvil client

const localDir = ".local"
const chainInfoFile = "chain_info.json"

type ChainInfo struct {
	PID int `json:"pid"`
}

func startAnvil() error {
	// Ensure the local directory exists
	if err := os.MkdirAll(localDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create local directory: %v", err)
	}

	chainInfoPath := filepath.Join(localDir, chainInfoFile)

	// Check if anvil is already running
	if _, err := os.Stat(chainInfoPath); err == nil {
		data, _ := os.ReadFile(chainInfoPath)
		var info ChainInfo
		if err := json.Unmarshal(data, &info); err == nil {
			process, err := os.FindProcess(info.PID)
			if err == nil && process.Signal(syscall.Signal(0)) == nil {
				fmt.Printf("Anvil is already running with PID %d\n", info.PID)
				return nil
			}

			fmt.Println("Found stale PID file. Starting a new instance.")
		}
	}

	// Start anvil in the background
	cmd := exec.Command("anvil")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start anvil: %v", err)
	}

	// Save the process ID to a file
	info := ChainInfo{PID: cmd.Process.Pid}
	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("failed to marshal chain info: %v", err)
	}

	if err := os.WriteFile(chainInfoPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write chain info file: %v", err)
	}

	fmt.Printf("Anvil started with PID %d\n", info.PID)
	return nil
}

func stopAnvil() error {
	chainInfoPath := filepath.Join(localDir, chainInfoFile)

	// Check if chain info file exists
	if _, err := os.Stat(chainInfoPath); err == nil {
		data, _ := os.ReadFile(chainInfoPath)
		var info ChainInfo
		if err := json.Unmarshal(data, &info); err == nil {
			process, err := os.FindProcess(info.PID)
			if err != nil {
				return fmt.Errorf("failed to find process: %v", err)
			}

			// Kill the process
			if err := process.Kill(); err != nil {
				return fmt.Errorf("failed to kill anvil: %v", err)
			}

			// Clean up the chain info file
			if err := os.Remove(chainInfoPath); err != nil {
				return fmt.Errorf("failed to remove chain info file: %v", err)
			}

			fmt.Printf("Anvil stopped (PID %d)\n", info.PID)
			return nil
		}
	}

	fmt.Println("Anvil is not running.")
	return nil
}

func main() {
	app := &cli.App{
		Name:  "cll",
		Usage: "Run capabilities in a local environment",
		Commands: []*cli.Command{
			{
				Name:  "chain",
				Usage: "Prints a greeting",
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
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
