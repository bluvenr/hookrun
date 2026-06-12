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

// Logger provides structured file-based logging with daily rotation or single-file mode.
type Logger struct {
	mu            sync.Mutex
	mode          string // "daily" or "single"
	prefix        string // file name prefix (e.g. "hookrun", "deploy")
	logDir        string
	retentionDays int
	maxSizeMB     int64 // bytes, 0 = unlimited (only for single mode)
	minLevel      Level
	currentDate   string
	file          *os.File
	console       bool // also print to stdout/stderr
}

// Options holds configuration for creating a new Logger.
type Options struct {
	Mode          string // "daily" (default) | "single"
	Prefix        string // file name prefix (default: "hookrun")
	Path          string // directory path
	RetentionDays int    // only for daily mode (default: 30)
	MaxSizeMB     int    // only for single mode, 0 = unlimited
	MinLevel      Level  // minimum log level (default: Info)
	Console       bool   // also print to stdout/stderr (default: true)
}

// New creates a new Logger with the given options.
func New(opts Options) *Logger {
	if opts.Prefix == "" {
		opts.Prefix = "hookrun"
	}
	if opts.Mode == "" {
		opts.Mode = "daily"
	}
	if opts.RetentionDays <= 0 {
		opts.RetentionDays = 30
	}

	l := &Logger{
		mode:          opts.Mode,
		prefix:        opts.Prefix,
		logDir:        opts.Path,
		retentionDays: opts.RetentionDays,
		maxSizeMB:     int64(opts.MaxSizeMB) * 1024 * 1024,
		minLevel:      opts.MinLevel,
		console:       opts.Console,
	}

	// Ensure log directory exists
	_ = os.MkdirAll(opts.Path, 0755)

	// Initial cleanup (only for daily mode)
	if l.mode == "daily" {
		l.Cleanup()
	}

	return l
}

// NewRuleLogger creates a logger for a specific rule config file.
// Inherits mode, retention from global settings. Console output is disabled.
func NewRuleLogger(logPath string, mode string, retentionDays int, maxSizeMB int) *Logger {
	dir := filepath.Dir(logPath)
	base := filepath.Base(logPath)
	prefix := strings.TrimSuffix(base, filepath.Ext(base))

	return New(Options{
		Mode:          mode,
		Prefix:        prefix,
		Path:          dir,
		RetentionDays: retentionDays,
		MaxSizeMB:     maxSizeMB,
		MinLevel:      LevelInfo,
		Console:       false, // rule-level loggers don't print to console
	})
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

	// File output
	l.ensureFile(now)

	if l.file != nil {
		_, _ = l.file.WriteString(line)
	}
}

// ensureFile opens or rotates the log file as needed.
func (l *Logger) ensureFile(now time.Time) {
	switch l.mode {
	case "daily":
		today := now.Format("2006-01-02")
		if l.file == nil || l.currentDate != today {
			l.rotateDaily(today)
		}
	case "single":
		if l.file == nil {
			l.openSingle()
		} else if l.maxSizeMB > 0 {
			l.rotateSingleIfNeeded()
		}
	}
}

// rotateDaily opens a new daily log file: {prefix}-{date}.log
func (l *Logger) rotateDaily(date string) {
	if l.file != nil {
		_ = l.file.Close()
	}

	filename := fmt.Sprintf("%s-%s.log", l.prefix, date)
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

// openSingle opens the single log file: {prefix}.log
func (l *Logger) openSingle() {
	if l.file != nil {
		_ = l.file.Close()
	}

	filename := fmt.Sprintf("%s.log", l.prefix)
	path := filepath.Join(l.logDir, filename)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Failed to open log file %s: %v\n", path, err)
		l.file = nil
		return
	}

	l.file = f
}

// rotateSingleIfNeeded rotates the single file when it exceeds max size.
func (l *Logger) rotateSingleIfNeeded() {
	info, err := l.file.Stat()
	if err != nil || info.Size() < l.maxSizeMB {
		return
	}

	// Close current file
	_ = l.file.Close()

	// Rotate: prefix.log -> prefix-1.log (shift existing)
	basePath := filepath.Join(l.logDir, fmt.Sprintf("%s.log", l.prefix))
	rotatedPath := filepath.Join(l.logDir, fmt.Sprintf("%s-1.log", l.prefix))

	// Remove oldest rotated file if exists
	_ = os.Remove(rotatedPath)
	// Rename current to rotated
	_ = os.Rename(basePath, rotatedPath)

	// Open fresh file
	f, err := os.OpenFile(basePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Failed to reopen log file %s: %v\n", basePath, err)
		l.file = nil
		return
	}

	l.file = f
}

// Cleanup removes log files older than retentionDays (daily mode only).
// Matches files with pattern: {prefix}-YYYY-MM-DD.log
func (l *Logger) Cleanup() {
	cutoff := time.Now().AddDate(0, 0, -l.retentionDays)

	entries, err := os.ReadDir(l.logDir)
	if err != nil {
		return
	}

	matchPrefix := l.prefix + "-"
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Match pattern: {prefix}-YYYY-MM-DD.log
		if !strings.HasPrefix(name, matchPrefix) || !strings.HasSuffix(name, ".log") {
			continue
		}
		dateStr := strings.TrimPrefix(name, matchPrefix)
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
