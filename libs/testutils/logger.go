package testutils

import (
	"fmt"
	"testing"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

var _ logger.SugaredLogger = (*lgr)(nil)

// With adds structured context to the logger
func (l *lgr) With(args ...interface{}) logger.SugaredLogger {
	return &lgr{name: l.name, t: l.t}
}

// Trace logs a trace message
func (l *lgr) Trace(args ...interface{}) {
	l.t.Log("[TRACE]", l.name, fmt.Sprint(args...))
}

// Tracef logs a trace message with formatting
func (l *lgr) Tracef(format string, values ...interface{}) {
	l.t.Logf("[TRACE] "+l.name+" "+format, values...)
}

// Tracew logs a trace message with key-value pairs
func (l *lgr) Tracew(msg string, keysAndValues ...interface{}) {
	l.t.Log("[TRACE]", l.name, msg, fmt.Sprint(keysAndValues...))
}

// Named returns a new logger with the specified name
func (l *lgr) Named(name string) logger.SugaredLogger {
	return &lgr{name: name, t: l.t}
}

// Helper marks the function as a test helper function
func (l *lgr) Helper(i int) logger.SugaredLogger {
	l.t.Helper()
	return l
}

// ErrorIfFn logs an error message if the condition is true using a custom function
func (l *lgr) ErrorIfFn(fn func() error, msg string) {
	if err := fn(); err != nil {
		l.t.Log("[ERROR]", l.name, msg, err)
	}
}

// ErrorIf logs an error message if the condition is true
func (l *lgr) ErrorIf(err error, msg string) {
	if err != nil {
		l.t.Log("[ERROR]", l.name, msg, err)
	}
}

// Critical logs a critical message
func (l *lgr) Critical(args ...interface{}) {
	l.t.Log("[CRITICAL]", l.name, fmt.Sprint(args...))
}

// Criticalf logs a critical message with formatting
func (l *lgr) Criticalf(format string, values ...interface{}) {
	l.t.Logf("[CRITICAL] "+l.name+" "+format, values...)
}

// Criticalw logs a critical message with key-value pairs
func (l *lgr) Criticalw(msg string, keysAndValues ...interface{}) {
	l.t.Log("[CRITICAL]", l.name, msg, fmt.Sprint(keysAndValues...))
}

// AssumptionViolation logs an assumption violation message
func (l *lgr) AssumptionViolation(args ...interface{}) {
	l.t.Log("[ASSUMPTION VIOLATION]", l.name, fmt.Sprint(args...))
}

// AssumptionViolationf logs an assumption violation message with formatting
func (l *lgr) AssumptionViolationf(format string, values ...interface{}) {
	l.t.Logf("[ASSUMPTION VIOLATION] "+l.name+" "+format, values...)
}

// AssumptionViolationw logs an assumption violation message with key-value pairs
func (l *lgr) AssumptionViolationw(msg string, keysAndValues ...interface{}) {
	l.t.Log("[ASSUMPTION VIOLATION]", l.name, msg, fmt.Sprint(keysAndValues...))
}

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
