package node

import (
	"fmt"
	"os/exec"
	"time"

	"github.com/smartcontractkit/capabilities/libs/cli/constants"
	"github.com/smartcontractkit/capabilities/libs/cli/utils"
)

func Login(nodeID int) error {
	nodeInfo := utils.GetNodeInfo(nodeID)
	var err error

	fmt.Printf("Logging in to Node %d.", nodeID)

	for i := 0; i < 5; i++ {
		// Login to the node
		cmd := exec.Command( //nolint:gosec
			constants.ChainlinkBinaryLocation,
			"--remote-node-url", nodeInfo.URLs.HTTP,
			"admin", "login",
			"--file", nodeInfo.Paths.Credentials,
			"--bypass-version-check",
		)

		err = cmd.Run()
		if err == nil {
			fmt.Printf(" Success!\n")
			return nil
		}

		fmt.Printf(".")
		time.Sleep(1 * time.Second)
	}

	return err
}
