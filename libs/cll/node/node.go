package node

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/smartcontractkit/capabilities/libs/cli/constants"
	"github.com/urfave/cli/v2"
)

const (
	CHAINLINK_NODE_CONFIG_FILENAME         = "config.toml"
	CHAINLINK_NODE_SECRETS_FILENAME        = "secrets.toml"
	CHAINLINK_NODE_UI_CREDENTIALS_FILENAME = "apicredentials"
	CHAINLINK_NODE_KEYSTORE_PASSWORD_FILE  = "password.txt"
	GENERIC_EMAIL                          = "user@example.com"
	GENERIC_PASSWORD                       = "password"
	KEYSTORE_PASSWORD                      = "keystore_password"
)

func getPorts(nodeID int) (int, int) {
	const (
		HTTP_PORT_BASE = 6688
		P2P_PORT_BASE  = 8000
	)
	HTTP_PORT := HTTP_PORT_BASE + nodeID
	P2P_PORT := P2P_PORT_BASE + nodeID
	return HTTP_PORT, P2P_PORT
}

func contains(output []byte, substr string) bool {
	return strings.Contains(string(output), substr)
}

func createChainlinkNodes(nodes int) error {
	// Check if the constants.LocalDbUserName exists
	checkUserCmd := exec.Command("psql", "-U", "postgres", "-c", fmt.Sprintf("SELECT FROM pg_catalog.pg_roles WHERE rolname = '%s';", constants.LocalDbUserName))
	userCheckOutput, err := checkUserCmd.Output()
	if err != nil {
		return err
	}

	// Create the user if it does not exist
	if !contains(userCheckOutput, "1 row") {
		createUserCmd := exec.Command("psql", "-q", "-U", "postgres", "-c", fmt.Sprintf("CREATE USER %s WITH SUPERUSER PASSWORD '%s';", constants.LocalDbUserName, GENERIC_PASSWORD))
		err = createUserCmd.Run()
		if err != nil {
			return err
		}
	}

	// Creating the nodes
	for i := 0; i < nodes; i++ {
		nodeID := i + 1
		nodeDir := getNodeDir(nodeID)

		err := os.MkdirAll(nodeDir, os.ModePerm)
		if err != nil {
			return err
		}

		HTTP_PORT, P2P_PORT := getPorts(nodeID)

		// Write the config file
		configContent := fmt.Sprintf(`RootDir = '%s'

	[Log]
	Level = 'debug'

	[Feature]
	LogPoller = true

	[OCR2]
	Enabled = true
	DatabaseTimeout = '1s'

	[P2P.V2]
	Enabled = true
	ListenAddresses = ['0.0.0.0:%d']

	[WebServer]
	HTTPPort = %d
	AllowOrigins = '*'
	SecureCookies = false

	[WebServer.TLS]
	HTTPSPort = 0

	[[EVM]]
	ChainID = '31337'

	[EVM.BalanceMonitor]
	Enabled = true

	[[EVM.Nodes]]
	Name = 'primary'
	WSURL = 'wss://127.0.0.1:8545'
	HTTPURL = 'http://127.0.0.1:8545'

	`, nodeDir, P2P_PORT, HTTP_PORT)

		configFilePath := filepath.Join(nodeDir, CHAINLINK_NODE_CONFIG_FILENAME)
		err = os.WriteFile(configFilePath, []byte(configContent), 0600)
		if err != nil {
			return err
		}

		// Create the secrets file
		databaseURL := fmt.Sprintf("postgresql://%s:%s@localhost:5432/%s?sslmode=disable", constants.LocalDbUserName, GENERIC_PASSWORD, getNodeDBName(nodeID))
		secretsContent := fmt.Sprintf(`[Database]
	URL = "%s" # Required

	[Password]
	Keystore = "%s" # Required`, databaseURL, KEYSTORE_PASSWORD)

		secretsFilePath := filepath.Join(nodeDir, CHAINLINK_NODE_SECRETS_FILENAME)
		err = os.WriteFile(secretsFilePath, []byte(secretsContent), 0600)
		if err != nil {
			return err
		}

		// Create the UI credentials file
		credentialsContent := fmt.Sprintf(`%s
	%s`, GENERIC_EMAIL, GENERIC_PASSWORD)
		credentialsFilePath := filepath.Join(nodeDir, CHAINLINK_NODE_UI_CREDENTIALS_FILENAME)
		err = os.WriteFile(credentialsFilePath, []byte(credentialsContent), 0600)
		if err != nil {
			return err
		}

		// Create the keystore password file
		keystorePasswordFilePath := filepath.Join(nodeDir, CHAINLINK_NODE_KEYSTORE_PASSWORD_FILE)
		err = os.WriteFile(keystorePasswordFilePath, []byte(KEYSTORE_PASSWORD), 0600)
		if err != nil {
			return err
		}

		// Check if the database exists
		checkDBCmd := exec.Command("psql", "-U", "postgres", "-c", fmt.Sprintf("SELECT FROM pg_database WHERE datname = '%s';", getNodeDBName(nodeID)))
		dbCheckOutput, err := checkDBCmd.Output()
		if err != nil {
			return err
		}

		// Drop the database if it exists
		if contains(dbCheckOutput, "1 row") {
			dropDBCmd := exec.Command("psql", "-q", "-U", "postgres", "-c", fmt.Sprintf("DROP DATABASE %s;", getNodeDBName(nodeID)))
			err = dropDBCmd.Run()
			if err != nil {
				return err
			}
		}

		// Create the database
		createDBCmd := exec.Command("psql", "-q", "-U", "postgres", "-c", fmt.Sprintf("CREATE DATABASE %s;", getNodeDBName(nodeID)))
		err = createDBCmd.Run()
		if err != nil {
			return err
		}

		// Grant privileges
		grantCmd := exec.Command("psql", "-q", "-U", "postgres", "-c", fmt.Sprintf("GRANT ALL ON DATABASE %s TO %s;", getNodeDBName(nodeID), constants.LocalDbUserName))
		err = grantCmd.Run()
		if err != nil {
			return err
		}

		fmt.Printf("Chainlink Node %d created! (%s directory, %s database)\n", nodeID, nodeDir, getNodeDBName(nodeID))
	}

	return nil
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

func removeChainlinkNodes(nodes int) error {
	for i := 0; i < nodes; i++ {
		nodeID := i + 1
		nodeDir := getNodeDir(nodeID)

		if _, err := os.Stat(nodeDir); !os.IsNotExist(err) {
			// Directory exists
			err = os.RemoveAll(nodeDir)
			if err != nil {
				return fmt.Errorf("failed to remove directory %s: %v", nodeDir, err)
			}

			// Check if the database exists
			checkDBCmd := []string{
				"-U", "postgres",
				"-tAc", fmt.Sprintf("SELECT 1 FROM pg_database WHERE datname='%s'", getNodeDBName(nodeID)),
			}
			dbCheckOutput, err := execCommand("psql", checkDBCmd...)
			if err != nil {
				return fmt.Errorf("failed to check database: %v", err)
			}

			dbExists := strings.TrimSpace(string(dbCheckOutput)) == "1"

			// Drop the database if it exists
			if dbExists {
				dropDBCmd := []string{
					"-U", "postgres",
					"-c", fmt.Sprintf("DROP DATABASE %s;", getNodeDBName(nodeID)),
				}
				_, err = execCommand("psql", dropDBCmd...)
				if err != nil {
					return fmt.Errorf("failed to drop database: %v", err)
				}
			}

			fmt.Printf("Chainlink Node %d removed! (%s directory, %s database)\n", nodeID, getNodeDir(nodeID), getNodeDBName(nodeID))
		} else {
			// Directory does not exist
			fmt.Printf("Chainlink Node %d not found! (%s directory)\n", nodeID, nodeDir)
		}

	}

	return nil
}

var Commands = []*cli.Command{
	{
		Name:  "node",
		Usage: "Commands to manage the local nodes",
		Subcommands: []*cli.Command{
			{
				Name:  "create",
				Usage: "Create new local nodes",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:  "nodes",
						Value: 1,
						Usage: "Number of nodes to create",
					},
				},
				Action: func(c *cli.Context) error {
					return createChainlinkNodes(c.Int("nodes"))
				},
			},
			{
				Name:  "remove",
				Usage: "Remove local nodes",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:  "nodes",
						Value: 1,
						Usage: "Number of nodes to remove",
					},
				},
				Action: func(c *cli.Context) error {
					return removeChainlinkNodes(c.Int("nodes"))
				},
			},

			{
				Name:  "reset",
				Usage: "Reset local nodes",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:  "nodes",
						Value: 1,
						Usage: "Number of nodes to reset",
					},
				},
				Action: func(c *cli.Context) error {
					err := removeChainlinkNodes(c.Int("nodes"))
					if err != nil {
						return err
					}

					return createChainlinkNodes(c.Int("nodes"))
				},
			},
		},
	},
}
