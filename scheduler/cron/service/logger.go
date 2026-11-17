package service

import (
	"sync"

	"github.com/go-co-op/gocron/v2"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// CronLogger adapts lggr to gocron. gocron's Shutdown returns before its goroutines stop logging, so it
// stops forwarding once the service closes, keeping logs inside the service's lifetime.
type CronLogger struct {
	lggr    logger.Logger
	mu      sync.Mutex
	stopped bool
}

var _ gocron.Logger = (*CronLogger)(nil)

func NewCronLogger(lggr logger.Logger) *CronLogger {
	return &CronLogger{lggr: lggr}
}

func (cl *CronLogger) Debug(msg string, args ...any) { cl.log(cl.lggr.Debugw, msg, args) }
func (cl *CronLogger) Error(msg string, args ...any) { cl.log(cl.lggr.Errorw, msg, args) }
func (cl *CronLogger) Info(msg string, args ...any)  { cl.log(cl.lggr.Infow, msg, args) }
func (cl *CronLogger) Warn(msg string, args ...any)  { cl.log(cl.lggr.Warnw, msg, args) }

func (cl *CronLogger) log(write func(string, ...any), msg string, args []any) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if !cl.stopped {
		write(msg, "args", args)
	}
}

// stop drops everything logged after it returns.
func (cl *CronLogger) stop() {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	cl.stopped = true
}
