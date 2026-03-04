package contracts

import (
	_ "embed"
)

//go:embed idl/log_read_test.json
var logReadTestIDL string

func LoadLogReadTestIDL() (string, error) {
	return logReadTestIDL, nil
}
