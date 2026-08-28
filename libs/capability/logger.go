package capability

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// NewLogger returns a logger encoding hclog-compatible JSON on stderr, like a
// LOOP plugin's: a go-plugin host parses and re-levels these entries, while
// standalone they are plain zap JSON logs. Level is Debug because filtering is
// the reader's job (host-side, or the log pipeline).
//
// It is public, and Run takes what it returns rather than calling it, because a
// binary that needs a logger before Run - to build something of its own with -
// would otherwise end up with two: one it made and one Run made behind it.
// Handing the value in is what makes them the same logger.
func NewLogger() (logger.Logger, error) {
	return logger.NewWith(func(cfg *zap.Config) {
		cfg.Level.SetLevel(zap.DebugLevel)
		cfg.EncoderConfig.LevelKey = "@level"
		cfg.EncoderConfig.MessageKey = "@message"
		cfg.EncoderConfig.TimeKey = "@timestamp"
		cfg.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02T15:04:05.000000Z07:00")
	})
}
