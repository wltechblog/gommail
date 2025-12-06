package trace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIMAPTracer(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "imap-trace-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create tracer
	tracer := NewIMAPTracer(true, tempDir)
	if !tracer.IsEnabled() {
		t.Error("Tracer should be enabled")
	}

	// Test account tracer creation
	accountName := "test-account"
	accountTracer := tracer.GetAccountTracer(accountName)
	if accountTracer == nil {
		t.Fatal("Account tracer should not be nil")
	}

	// Test writing IMAP data
	testData := []byte("A001 LOGIN user@example.com password\r\n")
	n, err := accountTracer.Write(testData)
	if err != nil {
		t.Errorf("Failed to write test data: %v", err)
	}
	if n != len(testData) {
		t.Errorf("Expected to write %d bytes, wrote %d", len(testData), n)
	}

	// Test server response
	serverResponse := []byte("* OK IMAP4rev1 Service Ready\r\n")
	n, err = accountTracer.Write(serverResponse)
	if err != nil {
		t.Errorf("Failed to write server response: %v", err)
	}
	if n != len(serverResponse) {
		t.Errorf("Expected to write %d bytes, wrote %d", len(serverResponse), n)
	}

	// Close tracer to flush data
	err = tracer.Close()
	if err != nil {
		t.Errorf("Failed to close tracer: %v", err)
	}

	// Verify trace file was created
	files, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("Failed to read temp dir: %v", err)
	}

	if len(files) != 1 {
		t.Errorf("Expected 1 trace file, found %d", len(files))
	}

	// Check file name format
	expectedPrefix := "imap-trace-" + accountName + "-"
	if !strings.HasPrefix(files[0].Name(), expectedPrefix) {
		t.Errorf("Trace file name should start with %s, got %s", expectedPrefix, files[0].Name())
	}

	// Read and verify file content
	filePath := filepath.Join(tempDir, files[0].Name())
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read trace file: %v", err)
	}

	contentStr := string(content)

	// Check for header
	if !strings.Contains(contentStr, "=== IMAP Trace Started for Account: "+accountName) {
		t.Error("Trace file should contain start header")
	}

	// Check for footer
	if !strings.Contains(contentStr, "=== IMAP Trace Ended for Account: "+accountName) {
		t.Error("Trace file should contain end footer")
	}

	// Check for client command (C->S)
	if !strings.Contains(contentStr, "[C->S]") {
		t.Error("Trace file should contain client-to-server direction marker")
	}

	// Check for server response (S->C)
	if !strings.Contains(contentStr, "[S->C]") {
		t.Error("Trace file should contain server-to-client direction marker")
	}

	// Check for actual data
	if !strings.Contains(contentStr, "LOGIN") {
		t.Error("Trace file should contain LOGIN command")
	}

	if !strings.Contains(contentStr, "IMAP4rev1 Service Ready") {
		t.Error("Trace file should contain server response")
	}
}

func TestIMAPTracerDisabled(t *testing.T) {
	// Create tracer with tracing disabled
	tracer := NewIMAPTracer(false, "")
	if tracer.IsEnabled() {
		t.Error("Tracer should be disabled")
	}

	// Test that GetAccountTracer returns nil when disabled
	accountTracer := tracer.GetAccountTracer("test-account")
	if accountTracer != nil {
		t.Error("Account tracer should be nil when tracing is disabled")
	}
}

func TestDirectionDetection(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "direction-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	tracer := NewIMAPTracer(true, tempDir)
	accountTracer := tracer.GetAccountTracer("test")

	testCases := []struct {
		data      string
		direction string
	}{
		{"A001 LOGIN user pass", "C->S"},
		{"* OK IMAP ready", "S->C"},
		{"A002 SELECT INBOX", "C->S"},
		{"* FLAGS (\\Answered \\Flagged)", "S->C"},
		{"A003 FETCH 1 BODY[]", "C->S"},
		{"* 1 FETCH (BODY[] {1234}", "S->C"},
	}

	for _, tc := range testCases {
		accountTracer.Write([]byte(tc.data + "\r\n"))
	}

	tracer.Close()

	// Read trace file and verify directions
	files, _ := os.ReadDir(tempDir)
	content, _ := os.ReadFile(filepath.Join(tempDir, files[0].Name()))
	contentStr := string(content)

	for _, tc := range testCases {
		if !strings.Contains(contentStr, "["+tc.direction+"]") {
			t.Errorf("Expected direction %s for data %s not found in trace", tc.direction, tc.data)
		}
	}
}
