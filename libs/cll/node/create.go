package node

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/smartcontractkit/capabilities/libs/cli/constants"
	"github.com/smartcontractkit/capabilities/libs/cli/utils"
)

func createNodes(nodeIDs []int) error {
	// Check if the constants.LocalDbUserName exists
	checkUserCmd := exec.Command( //nolint:gosec
		"psql",
		"-U", "postgres",
		"-c", fmt.Sprintf("SELECT FROM pg_catalog.pg_roles WHERE rolname = '%s';", constants.LocalDbUserName),
	)
	userCheckOutput, err := checkUserCmd.Output()
	if err != nil {
		return err
	}

	// Create the user if it does not exist
	if !utils.Contains(userCheckOutput, "1 row") {
		createUserCmd := exec.Command( //nolint:gosec
			"psql",
			"-q",
			"-U", "postgres",
			"-c", fmt.Sprintf("CREATE USER %s WITH SUPERUSER PASSWORD '%s';", constants.LocalDbUserName, constants.GenericPassword),
		)
		err = createUserCmd.Run()
		if err != nil {
			return err
		}
	}

	// Creating the nodes
	for _, nodeID := range nodeIDs {
		nodeInfo := utils.GetNodeInfo(nodeID)

		err = os.MkdirAll(nodeInfo.Paths.Capabilities, os.ModePerm)
		if err != nil {
			return fmt.Errorf("failed to create node capabilities directory: %v", err)
		}
		err = os.MkdirAll(nodeInfo.Paths.Jobs, os.ModePerm)
		if err != nil {
			return fmt.Errorf("failed to create node jobs directory: %v", err)
		}

		ports := utils.GetPorts(nodeID)

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

	`, nodeInfo.Paths.Dir, ports.P2P, ports.HTTP)

		configFilePath := filepath.Join(nodeInfo.Paths.Dir, constants.ChainlinkNodeConfigFilename)
		err = os.WriteFile(configFilePath, []byte(configContent), 0600)
		if err != nil {
			return err
		}

		// Create the secrets file
		databaseURL := fmt.Sprintf("postgresql://%s:%s@localhost:5432/%s?sslmode=disable", constants.LocalDbUserName, constants.GenericPassword, utils.GetNodeDBName(nodeID))
		secretsContent := fmt.Sprintf(`[Database]
	URL = "%s" # Required

	[Password]
	Keystore = "%s" # Required`, databaseURL, constants.KeystorePassword)

		secretsFilePath := filepath.Join(nodeInfo.Paths.Dir, constants.ChainlinkNodeSecretsFilename)
		err = os.WriteFile(secretsFilePath, []byte(secretsContent), 0600)
		if err != nil {
			return err
		}

		// Create the UI credentials file
		credentialsContent := fmt.Sprintf(`%s
	%s`, constants.GenericEmail, constants.GenericPassword)
		err = os.WriteFile(nodeInfo.Paths.Credentials, []byte(credentialsContent), 0600)
		if err != nil {
			return err
		}

		// Create the keystore password file
		keystorePasswordFilePath := filepath.Join(nodeInfo.Paths.Dir, constants.ChainlinkNodeKeystorePasswordFile)
		err = os.WriteFile(keystorePasswordFilePath, []byte(constants.KeystorePassword), 0600)
		if err != nil {
			return err
		}

		// Check if the database exists
		checkDBCmd := exec.Command( //nolint:gosec
			"psql",
			"-U", "postgres",
			"-c", fmt.Sprintf("SELECT FROM pg_database WHERE datname = '%s';", utils.GetNodeDBName(nodeID)),
		)
		dbCheckOutput, err := checkDBCmd.Output()
		if err != nil {
			return err
		}

		// Drop the database if it exists
		if utils.Contains(dbCheckOutput, "1 row") {
			dropDBCmd := exec.Command( //nolint:gosec
				"psql", "-q",
				"-U", "postgres",
				"-c", fmt.Sprintf("DROP DATABASE %s;", utils.GetNodeDBName(nodeID)),
			)
			err = dropDBCmd.Run()
			if err != nil {
				return err
			}
		}

		// Create the database
		createDBCmd := exec.Command( //nolint:gosec
			"psql", "-q",
			"-U", "postgres",
			"-c", fmt.Sprintf("CREATE DATABASE %s;", utils.GetNodeDBName(nodeID)),
		)
		err = createDBCmd.Run()
		if err != nil {
			return err
		}

		// Grant privileges
		grantCmd := exec.Command( //nolint:gosec
			"psql", "-q",
			"-U", "postgres",
			"-c", fmt.Sprintf("GRANT ALL ON DATABASE %s TO %s;", utils.GetNodeDBName(nodeID), constants.LocalDbUserName),
		)
		err = grantCmd.Run()
		if err != nil {
			return err
		}

		fmt.Printf("Chainlink Node %d created! (%s directory, %s database)\n", nodeID, nodeInfo.Paths.Dir, utils.GetNodeDBName(nodeID))
	}

	return nil
}
