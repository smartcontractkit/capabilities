package evmlogtrigger

// RuntimeConfig is shared between the workflow main files and the integration tests
// to avoid duplication of the YAML structure used to configure the log trigger.
type RuntimeConfig struct {
	Addresses []string `yaml:"addresses"`
	Topics    []struct {
		Values []string `yaml:"values"`
	} `yaml:"topics" json:"Topics,omitempty"`
	Confidence int32  `yaml:"confidence"`
	Abi        string `yaml:"abi,omitempty"`
	Event      string `yaml:"event,omitempty"`
}
