package testutils

import (
	"testing"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// NewLogger creates a new logger
func NewLogger(t *testing.T) logger.SugaredLogger {
	return logger.Sugared(logger.Test(t))
}
