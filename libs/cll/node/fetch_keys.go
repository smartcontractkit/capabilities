package node

func fetchKeys() {
	// app := &cli.App{
	// 	Name:  "chainlink-cli",
	// 	Usage: "Authenticate and fetch all keys from Chainlink node",
	// 	Action: func(c *cli.Context) error {
	// 		// Authenticate with Chainlink node
	// 		authCmd := exec.Command("chainlink", "admin", "login", "-f", "path/to/api/file")
	// 		authCmd.Stdout = os.Stdout
	// 		authCmd.Stderr = os.Stderr
	// 		if err := authCmd.Run(); err != nil {
	// 			return fmt.Errorf("failed to authenticate: %w", err)
	// 		}

	// 		// Fetch all keys
	// 		keysCmd := exec.Command("chainlink", "keys", "list")
	// 		keysCmd.Stdout = os.Stdout
	// 		keysCmd.Stderr = os.Stderr
	// 		if err := keysCmd.Run(); err != nil {
	// 			return fmt.Errorf("failed to fetch keys: %w", err)
	// 		}

	// 		return nil
	// 	},
	// }

}
