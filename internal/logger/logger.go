package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Level represents the log severity level.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// ParseLevel converts a string to a Level.
func ParseLevel(s string) Level {
	switch strings.ToLower(s) {
	case "debug":
		return LevelDebug
	case "info":
		return LevelInfo
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

// Logger provides structured file-based logging with daily rotation.
type Logger struct {
	mu            sync.Mutex
	logDir        string
	retentionDays int
	minLevel      Level
	currentDate   string
	file          *os.File
	console       bool // also print to stdout/stderr
}

// New creates a new Logger instance.
func New(logDir string, retentionDays int) *Logger {
	if retentionDays <= 0 {
		retentionDays = 30
	}
	l := &Logger{
		logDir:        logDir,
		retentionDays: retentionDays,
		minLevel:      LevelInfo,
		console:       true,
	}
	// Ensure log directory exists
	_ = os.MkdirAll(logDir, 0755)
	// Initial cleanup
	l.Cleanup()
	return l
}

// SetLevel sets the minimum log level.
func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.minLevel = level
}

// SetConsole enables or disables console output.
func (l *Logger) SetConsole(enabled bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.console = enabled
}

// Debug logs a message at DEBUG level.
func (l *Logger) Debug(format string, args ...interface{}) {
	l.log(LevelDebug, format, args...)
}

// Info logs a message at INFO level.
func (l *Logger) Info(format string, args ...interface{}) {
	l.log(LevelInfo, format, args...)
}

// Warn logs a message at WARN level.
func (l *Logger) Warn(format string, args ...interface{}) {
	l.log(LevelWarn, format, args...)
}

// Error logs a message at ERROR level.
func (l *Logger) Error(format string, args ...interface{}) {
	l.log(LevelError, format, args...)
}

// log writes a log entry at the given level.
func (l *Logger) log(level Level, format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if level < l.minLevel {
		return
	}

	now := time.Now()
	msg := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("[%s] [%s] %s\n", now.Format("2006-01-02 15:04:05"), level.String(), msg)

	// Console output
	if l.console {
		if level >= LevelError {
			fmt.Fprint(os.Stderr, line)
		} else {
			fmt.Fprint(os.Stdout, line)
		}
	}

	// File output with daily rotation
	today := now.Format("2006-01-02")
	if l.file == nil || l.currentDate != today {
		l.rotate(today)
	}

	if l.file != nil {
		_, _ = l.file.WriteString(line)
	}
}

// rotate switches to a new log file for the given date.
func (l *Logger) rotate(date string) {
	if l.file != nil {
		_ = l.file.Close()
	}

	filename := fmt.Sprintf("hookrun-%s.log", date)
	path := filepath.Join(l.logDir, filename)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Failed to open log file %s: %v\n", path, err)
		l.file = nil
		return
	}

	l.file = f
	l.currentDate = date
}

// Cleanup removes log files older than retentionDays.
func (l *Logger) Cleanup() {
	cutoff := time.Now().AddDate(0, 0, -l.retentionDays)

	entries, err := os.ReadDir(l.logDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Match pattern: hookrun-YYYY-MM-DD.log
		if !strings.HasPrefix(name, "hookrun-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		dateStr := strings.TrimPrefix(name, "hookrun-")
		dateStr = strings.TrimSuffix(dateStr, ".log")
		t, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}
		if t.Before(cutoff) {
			_ = os.Remove(filepath.Join(l.logDir, name))
		}
	}
}

// Close closes the current log file.
func (l *Logger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
}
