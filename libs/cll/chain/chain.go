package chain

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/urfave/cli/v2"

	"github.com/smartcontractkit/capabilities/libs/cli/constants"
)

var chainDir = filepath.Join(constants.LocalDir, "chain")
var chainConfigFilePath = filepath.Join(chainDir, "chain_config.json")

type URLs struct {
	HTTP string `json:"http"`
	WS   string `json:"ws"`
}

type Paths struct {
	ChainStateFile string `json:"chain_state_file"`
	ConfigFile     string `json:"config_file"`
	Contracts      string `json:"contracts"`
	Dir            string `json:"dir"`
}

type Info struct {
	Paths   *Paths `json:"paths"`
	ChainID int    `json:"chainID"`
	Port    int    `json:"port"`
	URLs    *URLs
}

func GetInfo() *Info {
	port := 8545 // TODO: Configure this in anvil
	return &Info{
		ChainID: 31337, // TODO: Configure this in anvil
		Port:    port,
		Paths: &Paths{
			ChainStateFile: filepath.Join(chainDir, "chain_state.json"),
			ConfigFile:     chainConfigFilePath,
			Contracts:      filepath.Join(chainDir, "contracts"),
			Dir:            chainDir,
		},
		URLs: &URLs{
			HTTP: fmt.Sprintf("http://localhost:%d", port),
			WS:   fmt.Sprintf("ws://localhost:%d", port),
		},
	}
}

type Config struct {
	Accounts    []string `json:"available_accounts"`
	PrivateKeys []string `json:"private_keys"`
}

func GetConfig() (*Config, error) {
	fileData, err := os.ReadFile(chainConfigFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read chain config file: %v", err)
	}

	var data Config

	if err := json.Unmarshal(fileData, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal chain config JSON: %v", err)
	}

	return &data, nil
}

func startAnvil(silent bool) error {
	chainInfo := GetInfo()
	// Ensure the local directory exists
	if err := os.MkdirAll(chainDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create local directory: %v", err)
	}

	lockFilePath := filepath.Join(chainDir, constants.LockFile)
	if _, err := os.Stat(lockFilePath); err == nil {
		data, err := os.ReadFile(lockFilePath)
		if err != nil {
			return fmt.Errorf("failed to read lock file: %v", err)
		}
		pid, err := strconv.Atoi(string(data))
		if err != nil {
			return fmt.Errorf("failed to read PID from lock file: %v", err)
		}

		fmt.Printf("Chain is already started on PID %d (lock file exists)\n", pid)
		return nil
	}

	// SPIKE: Investigate if we can spin up `anvil` in docker
	// to avoid the need for a local installation

	args := []string{
		"--config-out", chainConfigFilePath,
		"--block-time", "3", // seconds
		"--port", fmt.Sprintf("%d", chainInfo.Port),
		"--state", chainInfo.Paths.ChainStateFile,
	}

	if silent {
		args = append(args, "--silent")
	}

	// Start anvil in the background
	cmd := exec.Command("anvil", args...)
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
	lockFilePath := filepath.Join(chainDir, constants.LockFile)

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
		if err := process.Signal(os.Interrupt); err != nil {
			fmt.Printf("failed to interrupt anvil: %v", err)

			if err2 := process.Kill(); err2 != nil {
				return fmt.Errorf("failed to kill anvil: %v", err2)
			}
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
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "silent",
						Usage: "Silent mode",
						Value: true,
					},
				},
				Action: func(c *cli.Context) error {
					return startAnvil(c.Bool("silent"))
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
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "silent",
						Usage: "Silent mode",
						Value: true,
					},
				},
				Action: func(c *cli.Context) error {
					if err := stopAnvil(); err != nil {
						return err
					}
					return startAnvil(c.Bool("silent"))
				},
			},
		},
	},
}
