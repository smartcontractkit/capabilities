package testutils

import (
	"testing"

	"github.com/smartcontractkit/chainlink-common/pkg/loop"
)

func NewPluginServer(t *testing.T) *loop.Server {
	return &loop.Server{
		Logger: NewLogger(t),
	}
}
