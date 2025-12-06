package imap

import (
	"testing"

	"github.com/wltechblog/gommail/internal/email"
)

// TestClientWrapperImplementsInterface verifies that ClientWrapper implements email.IMAPClient
func TestClientWrapperImplementsInterface(t *testing.T) {
	config := email.ServerConfig{
		Host:     "test.example.com",
		Port:     993,
		Username: "test@example.com",
		Password: "password",
		TLS:      true,
	}

	// Create client wrapper
	wrapper := NewClientWrapper(config)

	// Verify it implements the interface
	var _ email.IMAPClient = wrapper

	// Test basic properties
	if wrapper == nil {
		t.Fatal("NewClientWrapper returned nil")
	}

	if wrapper.worker == nil {
		t.Fatal("ClientWrapper worker is nil")
	}

	if wrapper.logger == nil {
		t.Fatal("ClientWrapper logger is nil")
	}

	// Test that we can call interface methods without panicking
	// (These will fail due to no connection, but should not panic)

	// Connection methods
	err := wrapper.Connect()
	if err == nil {
		t.Log("Connect unexpectedly succeeded (should fail without real server)")
	}

	connected := wrapper.IsConnected()
	if connected {
		t.Error("IsConnected should return false when not connected")
	}

	// Folder methods (should fail gracefully)
	_, err = wrapper.ListFolders()
	if err == nil {
		t.Log("ListFolders unexpectedly succeeded")
	}

	_, err = wrapper.ListSubscribedFolders()
	if err == nil {
		t.Log("ListSubscribedFolders unexpectedly succeeded")
	}

	// Message methods (should fail gracefully)
	_, err = wrapper.FetchMessages("INBOX", 10)
	if err == nil {
		t.Log("FetchMessages unexpectedly succeeded")
	}

	_, err = wrapper.FetchMessage("INBOX", 1)
	if err == nil {
		t.Log("FetchMessage unexpectedly succeeded")
	}

	// Search methods (should fail gracefully)
	criteria := email.SearchCriteria{
		From: "test@example.com",
	}
	_, err = wrapper.SearchMessages(criteria)
	if err == nil {
		t.Log("SearchMessages unexpectedly succeeded")
	}

	_, err = wrapper.SearchMessagesInFolder("INBOX", criteria)
	if err == nil {
		t.Log("SearchMessagesInFolder unexpectedly succeeded")
	}

	_, err = wrapper.SearchCachedMessages(criteria)
	if err == nil {
		t.Log("SearchCachedMessages unexpectedly succeeded")
	}

	// Monitoring methods
	isMonitoring := wrapper.IsMonitoring()
	if isMonitoring {
		t.Error("IsMonitoring should return false when not started")
	}

	isPaused := wrapper.IsMonitoringPaused()
	if isPaused {
		t.Error("IsMonitoringPaused should return false when not started")
	}

	folder := wrapper.GetMonitoredFolder()
	if folder != "" {
		t.Error("GetMonitoredFolder should return empty string when not monitoring")
	}

	// Test callback setters (should not panic)
	wrapper.SetNewMessageCallback(func(folder string, messages []email.Message) {
		// Test callback
	})

	wrapper.SetConnectionStateCallback(func(event email.ConnectionEvent) {
		// Test callback
	})

	wrapper.SetMonitorCallbacks(
		func(folder string) {
			// Update callback
		},
		func(err error) {
			// Error callback
		},
	)

	// Test health and configuration methods
	healthStatus := wrapper.GetHealthStatus()
	if healthStatus == nil {
		t.Error("GetHealthStatus should not return nil")
	}

	// Test cleanup
	wrapper.Stop() // Stop() no longer returns an error
}

// TestClientWrapperWithCache tests the cache-enabled constructor
func TestClientWrapperWithCache(t *testing.T) {
	config := email.ServerConfig{
		Host:     "test.example.com",
		Port:     993,
		Username: "test@example.com",
		Password: "password",
		TLS:      true,
	}

	// Create client wrapper with cache (nil cache is acceptable)
	wrapper := NewClientWrapperWithCache(config, nil, "test-account")

	// Verify it implements the interface
	var _ email.IMAPClient = wrapper

	if wrapper == nil {
		t.Fatal("NewClientWrapperWithCache returned nil")
	}

	if wrapper.accountKey != "test-account" {
		t.Errorf("Expected accountKey 'test-account', got '%s'", wrapper.accountKey)
	}

	// Test cleanup
	wrapper.Stop() // Stop() no longer returns an error
}

// TestWorkerIntegration tests basic worker integration
func TestWorkerIntegration(t *testing.T) {
	config := email.ServerConfig{
		Host:     "test.example.com",
		Port:     993,
		Username: "test@example.com",
		Password: "password",
		TLS:      true,
	}

	wrapper := NewClientWrapper(config)
	defer wrapper.Stop()

	// Test worker state
	if !wrapper.worker.IsRunning() {
		// Start the worker
		err := wrapper.Start()
		if err != nil {
			t.Fatalf("Failed to start worker: %v", err)
		}
	}

	if !wrapper.worker.IsRunning() {
		t.Error("Worker should be running after Start()")
	}

	// Test worker state methods
	state := wrapper.worker.GetState()
	if state != ConnectionStateDisconnected {
		t.Errorf("Expected ConnectionStateDisconnected, got %s", state.String())
	}

	// Test IDLE methods
	isIDLEActive := wrapper.worker.IsIDLEActive()
	if isIDLEActive {
		t.Error("IDLE should not be active initially")
	}

	isIDLEPaused := wrapper.worker.IsIDLEPaused()
	if isIDLEPaused {
		t.Error("IDLE should not be paused initially")
	}

	idleFolder := wrapper.worker.GetIDLEFolder()
	if idleFolder != "" {
		t.Error("IDLE folder should be empty initially")
	}

	// Test health status
	healthStatus := wrapper.worker.GetHealthStatus()
	if healthStatus == nil {
		t.Error("Health status should not be nil")
	}

	if healthStatus["state"] == nil {
		t.Error("Health status should include state")
	}

	// Test configuration methods
	wrapper.worker.SetHealthCheckInterval(10)
	wrapper.worker.SetReconnectConfig(1, 30, 5)

	// These should not panic
	wrapper.worker.MarkInitialSyncComplete("INBOX")
}

// BenchmarkClientWrapperCreation benchmarks client wrapper creation
func BenchmarkClientWrapperCreation(b *testing.B) {
	config := email.ServerConfig{
		Host:     "test.example.com",
		Port:     993,
		Username: "test@example.com",
		Password: "password",
		TLS:      true,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wrapper := NewClientWrapper(config)
		wrapper.Stop() // Clean up
	}
}
