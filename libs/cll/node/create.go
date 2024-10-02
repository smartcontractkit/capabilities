package node

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/smartcontractkit/capabilities/libs/cli/constants"
)

func createNodes(nodes int) error {
	// Check if the constants.LocalDbUserName exists
	checkUserCmd := exec.Command("psql", "-U", "postgres", "-c", fmt.Sprintf("SELECT FROM pg_catalog.pg_roles WHERE rolname = '%s';", constants.LocalDbUserName))
	userCheckOutput, err := checkUserCmd.Output()
	if err != nil {
		return err
	}

	// Create the user if it does not exist
	if !contains(userCheckOutput, "1 row") {
		createUserCmd := exec.Command("psql", "-q", "-U", "postgres", "-c", fmt.Sprintf("CREATE USER %s WITH SUPERUSER PASSWORD '%s';", constants.LocalDbUserName, constants.GenericPassword))
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

		HTTPPort, P2PPort := getPorts(nodeID)

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
	WSURL = 'ws://127.0.0.1:8545'
	HTTPURL = 'http://127.0.0.1:8545'

	`, nodeDir, P2PPort, HTTPPort)

		configFilePath := filepath.Join(nodeDir, constants.ChainlinkNodeConfigFilename)
		err = os.WriteFile(configFilePath, []byte(configContent), 0600)
		if err != nil {
			return err
		}

		// Create the secrets file
		databaseURL := fmt.Sprintf("postgresql://%s:%s@localhost:5432/%s?sslmode=disable", constants.LocalDbUserName, constants.GenericPassword, getNodeDBName(nodeID))
		secretsContent := fmt.Sprintf(`[Database]
	URL = "%s" # Required

	[Password]
	Keystore = "%s" # Required`, databaseURL, constants.KeystorePassword)

		secretsFilePath := filepath.Join(nodeDir, constants.ChainlinkNodeSecretsFilename)
		err = os.WriteFile(secretsFilePath, []byte(secretsContent), 0600)
		if err != nil {
			return err
		}

		// Create the UI credentials file
		credentialsContent := fmt.Sprintf(`%s
	%s`, constants.GenericEmail, constants.GenericPassword)
		credentialsFilePath := filepath.Join(nodeDir, constants.ChainlinkNodeUICredentialsFilename)
		err = os.WriteFile(credentialsFilePath, []byte(credentialsContent), 0600)
		if err != nil {
			return err
		}

		// Create the keystore password file
		keystorePasswordFilePath := filepath.Join(nodeDir, constants.ChainlinkNodeKeystorePasswordFile)
		err = os.WriteFile(keystorePasswordFilePath, []byte(constants.KeystorePassword), 0600)
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
