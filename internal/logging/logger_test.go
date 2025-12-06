package logging

import (
	"bytes"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestLogLevel_String(t *testing.T) {
	tests := []struct {
		level    LogLevel
		expected string
	}{
		{LevelDebug, "DEBUG"},
		{LevelInfo, "INFO"},
		{LevelWarn, "WARN"},
		{LevelError, "ERROR"},
		{LevelFatal, "FATAL"},
	}

	for _, test := range tests {
		if got := test.level.String(); got != test.expected {
			t.Errorf("LogLevel.String() = %v, want %v", got, test.expected)
		}
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected LogLevel
		hasError bool
	}{
		{"DEBUG", LevelDebug, false},
		{"debug", LevelDebug, false},
		{"INFO", LevelInfo, false},
		{"info", LevelInfo, false},
		{"WARN", LevelWarn, false},
		{"WARNING", LevelWarn, false},
		{"ERROR", LevelError, false},
		{"FATAL", LevelFatal, false},
		{"INVALID", LevelInfo, true},
	}

	for _, test := range tests {
		level, err := ParseLevel(test.input)
		if test.hasError {
			if err == nil {
				t.Errorf("ParseLevel(%s) expected error, got nil", test.input)
			}
		} else {
			if err != nil {
				t.Errorf("ParseLevel(%s) unexpected error: %v", test.input, err)
			}
			if level != test.expected {
				t.Errorf("ParseLevel(%s) = %v, want %v", test.input, level, test.expected)
			}
		}
	}
}

func TestLogger_TextFormat(t *testing.T) {
	var buf bytes.Buffer
	logger := New(Config{
		Level:      LevelDebug,
		Format:     FormatText,
		Output:     &buf,
		Component:  "test",
		ShowCaller: false,
	})

	logger.Info("test message")
	output := buf.String()

	if !strings.Contains(output, "INFO") {
		t.Errorf("Expected INFO in output, got: %s", output)
	}
	if !strings.Contains(output, "[test]") {
		t.Errorf("Expected [test] component in output, got: %s", output)
	}
	if !strings.Contains(output, "test message") {
		t.Errorf("Expected 'test message' in output, got: %s", output)
	}
}

func TestLogger_JSONFormat(t *testing.T) {
	var buf bytes.Buffer
	logger := New(Config{
		Level:      LevelDebug,
		Format:     FormatJSON,
		Output:     &buf,
		Component:  "test",
		ShowCaller: false,
	})

	logger.Info("test message")
	output := buf.String()

	expectedFields := []string{
		`"level":"INFO"`,
		`"component":"test"`,
		`"message":"test message"`,
		`"timestamp"`,
	}

	for _, field := range expectedFields {
		if !strings.Contains(output, field) {
			t.Errorf("Expected %s in JSON output, got: %s", field, output)
		}
	}
}

func TestLogger_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	logger := New(Config{
		Level:     LevelWarn,
		Format:    FormatText,
		Output:    &buf,
		Component: "test",
	})

	logger.Debug("debug message")
	logger.Info("info message")
	logger.Warn("warn message")
	logger.Error("error message")

	output := buf.String()

	// Debug and Info should be filtered out
	if strings.Contains(output, "debug message") {
		t.Errorf("Debug message should be filtered out")
	}
	if strings.Contains(output, "info message") {
		t.Errorf("Info message should be filtered out")
	}

	// Warn and Error should be present
	if !strings.Contains(output, "warn message") {
		t.Errorf("Warn message should be present")
	}
	if !strings.Contains(output, "error message") {
		t.Errorf("Error message should be present")
	}
}

func TestLogger_WithCaller(t *testing.T) {
	var buf bytes.Buffer
	logger := New(Config{
		Level:      LevelDebug,
		Format:     FormatText,
		Output:     &buf,
		Component:  "test",
		ShowCaller: true,
	})

	logger.Info("test message")
	output := buf.String()

	// Should contain caller information (filename:line)
	if !strings.Contains(output, "logger_test.go:") {
		t.Errorf("Expected caller information in output, got: %s", output)
	}
}

func TestNewComponent(t *testing.T) {
	// Set up default logger
	var buf bytes.Buffer
	SetOutput(&buf)
	SetLevel(LevelDebug)

	componentLogger := NewComponent("mycomponent")
	componentLogger.Info("test message")

	output := buf.String()
	if !strings.Contains(output, "[mycomponent]") {
		t.Errorf("Expected [mycomponent] in output, got: %s", output)
	}
}

func TestGlobalLoggingFunctions(t *testing.T) {
	var buf bytes.Buffer
	SetOutput(&buf)
	SetLevel(LevelDebug)

	Debug("debug message")
	Info("info message")
	Warn("warn message")
	Error("error message")

	output := buf.String()

	expectedMessages := []string{"debug message", "info message", "warn message", "error message"}
	for _, msg := range expectedMessages {
		if !strings.Contains(output, msg) {
			t.Errorf("Expected '%s' in output, got: %s", msg, output)
		}
	}
}

func TestSetLevel(t *testing.T) {
	// Test setting debug level
	SetLevel(LevelDebug)
	if getLevel() != LevelDebug {
		t.Errorf("Expected level to be DEBUG, got %v", getLevel())
	}

	// Test setting info level
	SetLevel(LevelInfo)
	if getLevel() != LevelInfo {
		t.Errorf("Expected level to be INFO, got %v", getLevel())
	}

	// Test setting warn level
	SetLevel(LevelWarn)
	if getLevel() != LevelWarn {
		t.Errorf("Expected level to be WARN, got %v", getLevel())
	}
}

func TestEnableDebug(t *testing.T) {
	SetLevel(LevelInfo)
	EnableDebug()

	if getLevel() != LevelDebug {
		t.Errorf("Expected level to be DEBUG after EnableDebug(), got %v", getLevel())
	}
}

func TestEnvironmentVariableInit(t *testing.T) {
	// Save original value
	originalValue := os.Getenv("GOMMAIL_DEBUG")
	defer os.Setenv("GOMMAIL_DEBUG", originalValue)

	// Test with GOMMAIL_DEBUG=true
	os.Setenv("GOMMAIL_DEBUG", "true")

	// Reinitialize the default logger
	defaultLogger = nil
	once = sync.Once{}
	initDefaultLogger()

	if getLevel() != LevelDebug {
		t.Errorf("Expected level to be DEBUG when GOMMAIL_DEBUG=true, got %v", getLevel())
	}

	// Test with GOMMAIL_DEBUG=false
	os.Setenv("GOMMAIL_DEBUG", "false")

	// Reinitialize the default logger
	defaultLogger = nil
	once = sync.Once{}
	initDefaultLogger()

	if getLevel() != LevelInfo {
		t.Errorf("Expected level to be INFO when GOMMAIL_DEBUG=false, got %v", getLevel())
	}
}
