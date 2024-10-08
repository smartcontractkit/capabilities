package testutils

import (
	"fmt"
	"testing"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

var _ logger.SugaredLogger = (*lgr)(nil)

// lgr is a simple logger for testing purposes
type lgr struct {
	name string
	t    *testing.T
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
	l.t.Log("[ASSUMPTION VIOLATION]", l.name, msg, prettyPrint(keysAndValues...))
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
	l.t.Log("[CRITICAL]", l.name, msg, prettyPrint(keysAndValues...))
}

// Debug logs a debug message
func (l *lgr) Debug(args ...interface{}) {
	l.t.Log("[DEBUG]", l.name, fmt.Sprint(args...))
}

// Debugf logs a debug message with formatting
func (l *lgr) Debugf(format string, values ...interface{}) {
	l.t.Logf("[DEBUG] "+l.name+" "+format, values...)
}

// Debugw logs a debug message with key-value pairs
func (l *lgr) Debugw(msg string, keysAndValues ...interface{}) {
	l.t.Log("[DEBUG]", l.name, msg, prettyPrint(keysAndValues...))
}

// Error logs an error message
func (l *lgr) Error(args ...interface{}) {
	l.t.Log("[ERROR]", l.name, fmt.Sprint(args...))
}

// Errorf logs an error message with formatting
func (l *lgr) Errorf(format string, values ...interface{}) {
	l.t.Logf("[ERROR] "+l.name+" "+format, values...)
}

// ErrorIf logs an error message if the condition is true
func (l *lgr) ErrorIf(err error, msg string) {
	if err != nil {
		l.t.Log("[ERROR]", l.name, msg, err)
	}
}

// ErrorIfFn logs an error message if the condition is true using a custom function
func (l *lgr) ErrorIfFn(fn func() error, msg string) {
	if err := fn(); err != nil {
		l.t.Log("[ERROR]", l.name, msg, err)
	}
}

// Errorw logs an error message with key-value pairs
func (l *lgr) Errorw(msg string, keysAndValues ...interface{}) {
	l.t.Log("[ERROR]", l.name, msg, prettyPrint(keysAndValues...))
}

// Fatal logs a fatal message
func (l *lgr) Fatal(args ...interface{}) {
	l.t.Log("[FATAL]", l.name, fmt.Sprint(args...))
	l.t.FailNow()
}

// Fatalf logs a fatal message with formatting
func (l *lgr) Fatalf(format string, values ...interface{}) {
	l.t.Logf("[FATAL] "+l.name+" "+format, values...)
	l.t.FailNow()
}

// Fatalw logs a fatal message with key-value pairs
func (l *lgr) Fatalw(msg string, keysAndValues ...interface{}) {
	l.t.Log("[FATAL]", l.name, msg, prettyPrint(keysAndValues...))
	l.t.FailNow()
}

// Helper marks the function as a test helper function
func (l *lgr) Helper(i int) logger.SugaredLogger {
	l.t.Helper()
	return l
}

// Info logs an info message
func (l *lgr) Info(args ...interface{}) {
	l.t.Log("[INFO]", l.name, fmt.Sprint(args...))
}

// Infof logs an info message with formatting
func (l *lgr) Infof(format string, values ...interface{}) {
	l.t.Logf("[INFO] "+l.name+" "+format, values...)
}

// Infow logs an info message with key-value pairs
func (l *lgr) Infow(msg string, keysAndValues ...interface{}) {
	l.t.Log("[INFO]", l.name, msg, prettyPrint(keysAndValues...))
}

// Name returns the name of the logger
func (l *lgr) Name() string {
	return l.name
}

// Named returns a new logger with the specified name
func (l *lgr) Named(name string) logger.SugaredLogger {
	return &lgr{name: name, t: l.t}
}

// NewLogger creates a new logger
func NewLogger(t *testing.T) *lgr {
	return &lgr{t: t}
}

// Panic logs a panic message
func (l *lgr) Panic(args ...interface{}) {
	msg := fmt.Sprint(args...)
	l.t.Log("[PANIC]", l.name, msg)
	panic(msg)
}

// Panicf logs a panic message with formatting
func (l *lgr) Panicf(format string, values ...interface{}) {
	msg := fmt.Sprintf(format, values...)
	l.t.Log("[PANIC]", l.name, msg)
	panic(msg)
}

// Panicw logs a panic message with key-value pairs
func (l *lgr) Panicw(msg string, keysAndValues ...interface{}) {
	l.t.Log("[PANIC]", l.name, msg, prettyPrint(keysAndValues...))
	panic(msg)
}

// Sync is a no-op for this logger
func (l *lgr) Sync() error {
	return nil
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
	l.t.Log("[TRACE]", l.name, msg, prettyPrint(keysAndValues...))
}

// Warn logs a warn message
func (l *lgr) Warn(args ...interface{}) {
	l.t.Log("[WARN]", l.name, fmt.Sprint(args...))
}

// Warnf logs a warn message with formatting
func (l *lgr) Warnf(format string, values ...interface{}) {
	l.t.Logf("[WARN] "+l.name+" "+format, values...)
}

// Warnw logs a warn message with key-value pairs
func (l *lgr) Warnw(msg string, keysAndValues ...interface{}) {
	l.t.Log("[WARN]", l.name, msg, prettyPrint(keysAndValues...))
}

// With adds structured context to the logger
func (l *lgr) With(args ...interface{}) logger.SugaredLogger {
	return &lgr{name: l.name, t: l.t}
}

// Withf adds structured context to the logger with formatting
func prettyPrint(keysAndValues ...interface{}) string {
	var s string
	for i := 0; i < len(keysAndValues); i += 2 {
		s += fmt.Sprintf("%s=%v", keysAndValues[i], keysAndValues[i+1])
		if i+2 < len(keysAndValues) {
			s += ", "
		}
	}
	return s
}
