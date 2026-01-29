package config

// Config represents the configuration for the Solana chain capability.
type Config struct {
	// ChainID is the Solana chain identifier (e.g., mainnet-beta, devnet, testnet)
	ChainID string `json:"chainId"`
	// Network is the network type (e.g., "solana")
	Network string `json:"network"`
	// NodeAddress is the address of this node
	NodeAddress string `json:"nodeAddress"`
}
