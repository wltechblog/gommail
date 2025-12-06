package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// LogLevel represents the severity level of a log message
type LogLevel int

const (
	// LevelDebug is for detailed debugging information
	LevelDebug LogLevel = iota
	// LevelInfo is for general informational messages
	LevelInfo
	// LevelWarn is for warning messages
	LevelWarn
	// LevelError is for error messages
	LevelError
	// LevelFatal is for fatal error messages that cause program termination
	LevelFatal
)

// String returns the string representation of the log level
func (l LogLevel) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	case LevelFatal:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// LogFormat represents the output format for log messages
type LogFormat int

const (
	// FormatText is plain text format
	FormatText LogFormat = iota
	// FormatJSON is JSON format
	FormatJSON
)

// Logger represents a structured logger instance
type Logger struct {
	level        LogLevel
	format       LogFormat
	output       io.Writer
	component    string
	mu           sync.Mutex
	showCaller   bool
	inheritLevel bool // Whether this logger should inherit level from default logger
}

// Config represents logger configuration
type Config struct {
	Level      LogLevel
	Format     LogFormat
	Output     io.Writer
	Component  string
	ShowCaller bool
}

// defaultLogger is the global logger instance
var (
	defaultLogger *Logger
	once          sync.Once
)

// init initializes the default logger
func init() {
	initDefaultLogger()
}

// initDefaultLogger initializes the default logger with sensible defaults
func initDefaultLogger() {
	once.Do(func() {
		level := LevelInfo

		// Check environment variables for debug mode
		if os.Getenv("GOMMAIL_DEBUG") == "true" {
			level = LevelDebug
		}

		defaultLogger = &Logger{
			level:        level,
			format:       FormatText,
			output:       os.Stderr,
			component:    "mail",
			showCaller:   level == LevelDebug,
			inheritLevel: false, // Default logger doesn't inherit from itself
		}
	})
}

// New creates a new logger instance with the given configuration
func New(config Config) *Logger {
	if config.Output == nil {
		config.Output = os.Stderr
	}

	return &Logger{
		level:        config.Level,
		format:       config.Format,
		output:       config.Output,
		component:    config.Component,
		showCaller:   config.ShowCaller,
		inheritLevel: false, // Explicit loggers don't inherit level
	}
}

// NewComponent creates a new logger for a specific component
func NewComponent(component string) *Logger {
	// Create a component logger that inherits level from the default logger
	return &Logger{
		level:        LevelInfo, // This will be overridden by getEffectiveLevel()
		format:       defaultLogger.format,
		output:       defaultLogger.output,
		component:    component,
		showCaller:   defaultLogger.showCaller,
		inheritLevel: true, // This logger inherits level from default logger
	}
}

// SetLevel sets the logging level for the default logger
func SetLevel(level LogLevel) {
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()
	defaultLogger.level = level
	defaultLogger.showCaller = level == LevelDebug
}

// SetOutput sets the output writer for the default logger
func SetOutput(w io.Writer) {
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()
	defaultLogger.output = w
}

// SetFormat sets the output format for the default logger
func SetFormat(format LogFormat) {
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()
	defaultLogger.format = format
}

// EnableDebug enables debug logging
func EnableDebug() {
	SetLevel(LevelDebug)
}

// getLevel returns the current log level of the default logger (internal use only)
func getLevel() LogLevel {
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()
	return defaultLogger.level
}

// getEffectiveLevel returns the effective logging level for this logger
// This method is thread-safe and can be called without holding the logger's mutex
func (l *Logger) getEffectiveLevel() LogLevel {
	// Only component loggers created with NewComponent() inherit from the default logger
	if l.inheritLevel {
		// Need to lock the default logger to read its level
		defaultLogger.mu.Lock()
		level := defaultLogger.level
		defaultLogger.mu.Unlock()
		return level
	}
	// For non-inheriting loggers, we can read our own level without a lock
	// since it's only modified during initialization or via SetLevel (which uses a lock)
	return l.level
}

// log writes a log message with the specified level
func (l *Logger) log(level LogLevel, format string, args ...interface{}) {
	// Check if we should log this level BEFORE acquiring the lock
	// This is a performance optimization to avoid lock contention for filtered messages
	effectiveLevel := l.getEffectiveLevel()
	if level < effectiveLevel {
		return
	}

	// Only format the message if we're actually going to log it
	message := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format("2006-01-02 15:04:05.000")

	// Acquire lock only for the actual write operation
	l.mu.Lock()
	defer l.mu.Unlock()

	var logLine string

	switch l.format {
	case FormatJSON:
		caller := ""
		if l.showCaller {
			caller = l.getCaller()
		}
		logLine = fmt.Sprintf(`{"timestamp":"%s","level":"%s","component":"%s","caller":"%s","message":"%s"}`,
			timestamp, level.String(), l.component, caller, strings.ReplaceAll(message, `"`, `\"`))
	default: // FormatText
		if l.showCaller {
			caller := l.getCaller()
			logLine = fmt.Sprintf("[%s] %s [%s] %s: %s",
				timestamp, level.String(), l.component, caller, message)
		} else {
			logLine = fmt.Sprintf("[%s] %s [%s]: %s",
				timestamp, level.String(), l.component, message)
		}
	}

	fmt.Fprintln(l.output, logLine)
}

// getCaller returns the caller information
func (l *Logger) getCaller() string {
	_, file, line, ok := runtime.Caller(3)
	if !ok {
		return "unknown"
	}
	return fmt.Sprintf("%s:%d", filepath.Base(file), line)
}

// Debug logs a debug message
func (l *Logger) Debug(format string, args ...interface{}) {
	l.log(LevelDebug, format, args...)
}

// Info logs an info message
func (l *Logger) Info(format string, args ...interface{}) {
	l.log(LevelInfo, format, args...)
}

// Warn logs a warning message
func (l *Logger) Warn(format string, args ...interface{}) {
	l.log(LevelWarn, format, args...)
}

// Error logs an error message
func (l *Logger) Error(format string, args ...interface{}) {
	l.log(LevelError, format, args...)
}

// Fatal logs a fatal message and exits the program
func (l *Logger) Fatal(format string, args ...interface{}) {
	l.log(LevelFatal, format, args...)
	os.Exit(1)
}

// Global logging functions using the default logger

// Debug logs a debug message using the default logger
func Debug(format string, args ...interface{}) {
	defaultLogger.Debug(format, args...)
}

// Info logs an info message using the default logger
func Info(format string, args ...interface{}) {
	defaultLogger.Info(format, args...)
}

// Warn logs a warning message using the default logger
func Warn(format string, args ...interface{}) {
	defaultLogger.Warn(format, args...)
}

// Error logs an error message using the default logger
func Error(format string, args ...interface{}) {
	defaultLogger.Error(format, args...)
}

// Fatal logs a fatal message using the default logger and exits
func Fatal(format string, args ...interface{}) {
	defaultLogger.Fatal(format, args...)
}

// SetupFileLogging configures logging to write to a file in addition to stderr
// This function creates a dated log file in the specified directory
func SetupFileLogging(logDir string) error {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	logFile := filepath.Join(logDir, fmt.Sprintf("mail-%s.log", time.Now().Format("2006-01-02")))
	file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}

	// Create a multi-writer to write to both stderr and file
	multiWriter := io.MultiWriter(os.Stderr, file)
	SetOutput(multiWriter)

	return nil
}

// SetupFileLoggingWithPath configures logging to write to a specific file path in addition to stderr
// This function uses the exact file path specified by the user
func SetupFileLoggingWithPath(logFilePath string) error {
	// Create the directory if it doesn't exist
	logDir := filepath.Dir(logFilePath)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	file, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}

	// Create a multi-writer to write to both stderr and file
	multiWriter := io.MultiWriter(os.Stderr, file)
	SetOutput(multiWriter)

	return nil
}

// ParseLevel parses a string into a LogLevel
func ParseLevel(level string) (LogLevel, error) {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return LevelDebug, nil
	case "INFO":
		return LevelInfo, nil
	case "WARN", "WARNING":
		return LevelWarn, nil
	case "ERROR":
		return LevelError, nil
	case "FATAL":
		return LevelFatal, nil
	default:
		return LevelInfo, fmt.Errorf("unknown log level: %s", level)
	}
}
