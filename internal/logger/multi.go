package logger

// LogWriter is the interface for writing log messages.
// Both Logger and MultiLogger implement this interface.
type LogWriter interface {
	Debug(format string, args ...interface{})
	Info(format string, args ...interface{})
	Warn(format string, args ...interface{})
	Error(format string, args ...interface{})
	Close()
}

// Verify both types implement LogWriter.
var _ LogWriter = (*Logger)(nil)
var _ LogWriter = (*MultiLogger)(nil)

// MultiLogger wraps multiple loggers and writes to all of them.
// Used for rule-level dual-write: global logger + rule-specific logger.
type MultiLogger struct {
	loggers []LogWriter
}

// NewMulti creates a MultiLogger that writes to all provided loggers.
func NewMulti(loggers ...LogWriter) *MultiLogger {
	return &MultiLogger{loggers: loggers}
}

// Debug logs a message at DEBUG level to all loggers.
func (m *MultiLogger) Debug(format string, args ...interface{}) {
	for _, l := range m.loggers {
		l.Debug(format, args...)
	}
}

// Info logs a message at INFO level to all loggers.
func (m *MultiLogger) Info(format string, args ...interface{}) {
	for _, l := range m.loggers {
		l.Info(format, args...)
	}
}

// Warn logs a message at WARN level to all loggers.
func (m *MultiLogger) Warn(format string, args ...interface{}) {
	for _, l := range m.loggers {
		l.Warn(format, args...)
	}
}

// Error logs a message at ERROR level to all loggers.
func (m *MultiLogger) Error(format string, args ...interface{}) {
	for _, l := range m.loggers {
		l.Error(format, args...)
	}
}

// Close closes all wrapped loggers.
func (m *MultiLogger) Close() {
	for _, l := range m.loggers {
		l.Close()
	}
}
