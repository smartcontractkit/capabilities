package testutils

import (
	"fmt"
	"testing"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

var _ logger.Logger = (*lgr)(nil)

// lgr is a simple logger for testing purposes
type lgr struct {
	name string
	t    *testing.T
}

func NewLogger(t *testing.T) *lgr {
	return &lgr{t: t}
}

func (l *lgr) Name() string {
	return l.name
}

func (l *lgr) Debug(args ...interface{}) {
	l.t.Log("[DEBUG]", l.name, fmt.Sprint(args...))
}

func (l *lgr) Info(args ...interface{}) {
	l.t.Log("[INFO]", l.name, fmt.Sprint(args...))
}

func (l *lgr) Warn(args ...interface{}) {
	l.t.Log("[WARN]", l.name, fmt.Sprint(args...))
}

func (l *lgr) Error(args ...interface{}) {
	l.t.Log("[ERROR]", l.name, fmt.Sprint(args...))
}

func (l *lgr) Panic(args ...interface{}) {
	msg := fmt.Sprint(args...)
	l.t.Log("[PANIC]", l.name, msg)
	panic(msg)
}

func (l *lgr) Fatal(args ...interface{}) {
	l.t.Log("[FATAL]", l.name, fmt.Sprint(args...))
	l.t.FailNow()
}

func (l *lgr) Debugf(format string, values ...interface{}) {
	l.t.Logf("[DEBUG] "+l.name+" "+format, values...)
}

func (l *lgr) Infof(format string, values ...interface{}) {
	l.t.Logf("[INFO] "+l.name+" "+format, values...)
}

func (l *lgr) Warnf(format string, values ...interface{}) {
	l.t.Logf("[WARN] "+l.name+" "+format, values...)
}

func (l *lgr) Errorf(format string, values ...interface{}) {
	l.t.Logf("[ERROR] "+l.name+" "+format, values...)
}

func (l *lgr) Panicf(format string, values ...interface{}) {
	msg := fmt.Sprintf(format, values...)
	l.t.Log("[PANIC]", l.name, msg)
	panic(msg)
}

func (l *lgr) Fatalf(format string, values ...interface{}) {
	l.t.Logf("[FATAL] "+l.name+" "+format, values...)
	l.t.FailNow()
}

func (l *lgr) Debugw(msg string, keysAndValues ...interface{}) {
	l.t.Log("[DEBUG]", l.name, msg, fmt.Sprint(keysAndValues...))
}

func (l *lgr) Infow(msg string, keysAndValues ...interface{}) {
	l.t.Log("[INFO]", l.name, msg, fmt.Sprint(keysAndValues...))
}

func (l *lgr) Warnw(msg string, keysAndValues ...interface{}) {
	l.t.Log("[WARN]", l.name, msg, fmt.Sprint(keysAndValues...))
}

func (l *lgr) Errorw(msg string, keysAndValues ...interface{}) {
	l.t.Log("[ERROR]", l.name, msg, fmt.Sprint(keysAndValues...))
}

func (l *lgr) Panicw(msg string, keysAndValues ...interface{}) {
	l.t.Log("[PANIC]", l.name, msg, fmt.Sprint(keysAndValues...))
	panic(msg)
}

func (l *lgr) Fatalw(msg string, keysAndValues ...interface{}) {
	l.t.Log("[FATAL]", l.name, msg, fmt.Sprint(keysAndValues...))
	l.t.FailNow()
}

func (l *lgr) Sync() error {
	return nil
}
