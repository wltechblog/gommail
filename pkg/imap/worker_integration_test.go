package imap

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/wltechblog/gommail/internal/email"
)

// TestWorkerBasicOperations tests basic worker functionality
func TestWorkerBasicOperations(t *testing.T) {
	// Create a test server config (this won't actually connect)
	config := &email.ServerConfig{
		Host:     "imap.example.com",
		Port:     993,
		Username: "test@example.com",
		Password: "password",
		TLS:      true,
	}

	// Create worker
	worker := NewIMAPWorker(config, "test-account", "test-cache-key", nil)
	if worker == nil {
		t.Fatal("NewIMAPWorker returned nil")
	}

	// Test worker creation
	if worker.accountName != "test-account" {
		t.Errorf("Expected account name 'test-account', got '%s'", worker.accountName)
	}

	if worker.cacheKey != "test-cache-key" {
		t.Errorf("Expected cache key 'test-cache-key', got '%s'", worker.cacheKey)
	}

	// Test initial state
	if worker.GetState() != ConnectionStateDisconnected {
		t.Errorf("Expected initial state to be Disconnected, got %s", worker.GetState())
	}

	// Test status
	status := worker.GetStatus()
	if status.State != ConnectionStateDisconnected {
		t.Errorf("Expected status state to be Disconnected, got %s", status.State)
	}

	if status.CommandsQueued != 0 {
		t.Errorf("Expected 0 commands queued, got %d", status.CommandsQueued)
	}
}

// TestWorkerCommandCreation tests command creation utilities
func TestWorkerCommandCreation(t *testing.T) {
	// Test basic command creation
	cmd := NewCommand(CmdConnect, nil)
	if cmd == nil {
		t.Fatal("NewCommand returned nil")
	}

	if cmd.Type != CmdConnect {
		t.Errorf("Expected command type CmdConnect, got %s", cmd.Type)
	}

	if cmd.ID == "" {
		t.Error("Command ID should not be empty")
	}

	if cmd.ResponseCh == nil {
		t.Error("Response channel should not be nil")
	}

	// Test command with context
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmdWithCtx := NewCommandWithContext(ctx, CmdSelectFolder, map[string]interface{}{
		ParamFolderName: "INBOX",
	})

	if cmdWithCtx.Context != ctx {
		t.Error("Command context not set correctly")
	}

	folderName, ok := cmdWithCtx.GetString(ParamFolderName)
	if !ok || folderName != "INBOX" {
		t.Errorf("Expected folder name 'INBOX', got '%s' (ok: %v)", folderName, ok)
	}
}

// TestWorkerResponseCreation tests response creation utilities
func TestWorkerResponseCreation(t *testing.T) {
	// Test successful response
	response := NewResponse("test-cmd-123", true, map[string]interface{}{
		"test_data": "test_value",
	}, nil)

	if response == nil {
		t.Fatal("NewResponse returned nil")
	}

	if response.ID != "test-cmd-123" {
		t.Errorf("Expected response ID 'test-cmd-123', got '%s'", response.ID)
	}

	if !response.Success {
		t.Error("Expected response to be successful")
	}

	if response.Error != nil {
		t.Errorf("Expected no error, got %v", response.Error)
	}

	// Test error response
	testErr := fmt.Errorf("test error")
	errorResponse := NewResponse("test-cmd-456", false, nil, testErr)

	if errorResponse.Success {
		t.Error("Expected response to be unsuccessful")
	}

	if errorResponse.Error != testErr {
		t.Errorf("Expected error to be %v, got %v", testErr, errorResponse.Error)
	}
}

// TestWorkerEventCreation tests event creation utilities
func TestWorkerEventCreation(t *testing.T) {
	// Test connection event
	event := NewConnectionEvent(ConnectionStateConnected, nil)
	if event == nil {
		t.Fatal("NewConnectionEvent returned nil")
	}

	if event.State != ConnectionStateConnected {
		t.Errorf("Expected state Connected, got %s", event.State)
	}

	if event.Error != nil {
		t.Errorf("Expected no error, got %v", event.Error)
	}

	// Test new message event
	messages := []email.Message{
		{ID: "1", Subject: "Test Message 1"},
		{ID: "2", Subject: "Test Message 2"},
	}

	msgEvent := NewNewMessageEvent("INBOX", messages, 2)
	if msgEvent == nil {
		t.Fatal("NewNewMessageEvent returned nil")
	}

	if msgEvent.Folder != "INBOX" {
		t.Errorf("Expected folder 'INBOX', got '%s'", msgEvent.Folder)
	}

	if msgEvent.Count != 2 {
		t.Errorf("Expected count 2, got %d", msgEvent.Count)
	}

	if len(msgEvent.Messages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(msgEvent.Messages))
	}
}

// TestClientWrapperCreation tests client wrapper creation
func TestClientWrapperCreation(t *testing.T) {
	config := email.ServerConfig{
		Host:     "imap.example.com",
		Port:     993,
		Username: "test@example.com",
		Password: "password",
		TLS:      true,
	}

	// Test basic wrapper creation
	wrapper := NewClientWrapper(config)
	if wrapper == nil {
		t.Fatal("NewClientWrapper returned nil")
	}

	if wrapper.worker == nil {
		t.Fatal("Wrapper worker should not be nil")
	}

	if wrapper.config.Host != config.Host {
		t.Errorf("Expected host '%s', got '%s'", config.Host, wrapper.config.Host)
	}

	// Test wrapper with cache
	wrapperWithCache := NewClientWrapperWithCache(config, nil, "test-account")
	if wrapperWithCache == nil {
		t.Fatal("NewClientWrapperWithCache returned nil")
	}

	if wrapperWithCache.accountKey != "test-account" {
		t.Errorf("Expected account key 'test-account', got '%s'", wrapperWithCache.accountKey)
	}
}

// TestWorkerParameterHelpers tests parameter helper methods
func TestWorkerParameterHelpers(t *testing.T) {
	params := map[string]interface{}{
		"string_param": "test_string",
		"int_param":    42,
		"uint32_param": uint32(123),
		"bool_param":   true,
	}

	cmd := NewCommand(CmdConnect, params)

	// Test string parameter
	strVal, ok := cmd.GetString("string_param")
	if !ok || strVal != "test_string" {
		t.Errorf("Expected string 'test_string', got '%s' (ok: %v)", strVal, ok)
	}

	// Test int parameter
	intVal, ok := cmd.GetInt("int_param")
	if !ok || intVal != 42 {
		t.Errorf("Expected int 42, got %d (ok: %v)", intVal, ok)
	}

	// Test uint32 parameter
	uint32Val, ok := cmd.GetUint32("uint32_param")
	if !ok || uint32Val != 123 {
		t.Errorf("Expected uint32 123, got %d (ok: %v)", uint32Val, ok)
	}

	// Test bool parameter
	boolVal, ok := cmd.GetBool("bool_param")
	if !ok || !boolVal {
		t.Errorf("Expected bool true, got %v (ok: %v)", boolVal, ok)
	}

	// Test non-existent parameter
	_, ok = cmd.GetString("non_existent")
	if ok {
		t.Error("Expected non-existent parameter to return false")
	}
}
