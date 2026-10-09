package imap

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/wltechblog/gommail/internal/email"
	"github.com/wltechblog/gommail/pkg/cache"
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

func TestClientWrapperSearchTimeout(t *testing.T) {
	config := email.ServerConfig{Host: "test.example.com", Port: 993, Username: "test@example.com", Password: "password", TLS: true}
	wrapper := NewClientWrapper(config)
	t.Cleanup(wrapper.Stop)

	defaultTimeout := wrapper.defaultTimeout
	baseCriteria := email.SearchCriteria{MaxResults: 50}

	timeout := wrapper.getSearchTimeout(baseCriteria, true)
	if timeout != defaultTimeout {
		t.Fatalf("expected default timeout %v, got %v", defaultTimeout, timeout)
	}

	noFolderTimeout := wrapper.getSearchTimeout(baseCriteria, false)
	if noFolderTimeout <= defaultTimeout {
		t.Fatalf("expected no-folder timeout greater than default (%v <= %v)", noFolderTimeout, defaultTimeout)
	}

	serverTimeout := wrapper.getSearchTimeout(email.SearchCriteria{SearchServer: true}, true)
	if serverTimeout <= defaultTimeout {
		t.Fatalf("expected server search timeout greater than default (%v <= %v)", serverTimeout, defaultTimeout)
	}

	largeResultTimeout := wrapper.getSearchTimeout(email.SearchCriteria{MaxResults: 1000}, true)
	if largeResultTimeout <= 2*time.Minute {
		t.Fatalf("expected large-result timeout > 2m, got %v", largeResultTimeout)
	}
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

func TestCacheInvalidationDeletesWorkerKeys(t *testing.T) {
	dir := t.TempDir()
	c := cache.New(dir, false, 100)
	wrapper := NewClientWrapperWithCache(email.ServerConfig{Host: "h", Port: 1}, c, "acct")

	// Seed entries with the same keys the worker writes.
	for _, tc := range []struct{ keyType, id string }{
		{"messages", "INBOX"}, {"folders", "list"}, {"folders", "subscribed"},
	} {
		key := cache.GenerateAccountKey("acct", tc.keyType, tc.id)
		if err := c.Set(key, []byte("data"), time.Hour); err != nil {
			t.Fatalf("seed Set(%s) failed: %v", key, err)
		}
	}

	wrapper.InvalidateMessageCache("INBOX")
	wrapper.InvalidateFolderCache()
	wrapper.InvalidateSubscribedFolderCache()

	for _, tc := range []struct{ keyType, id string }{
		{"messages", "INBOX"}, {"folders", "list"}, {"folders", "subscribed"},
	} {
		key := cache.GenerateAccountKey("acct", tc.keyType, tc.id)
		if _, found, _ := c.Get(key); found {
			t.Errorf("key %s still present after invalidation", key)
		}
	}
}

func TestCleanupUnsubscribedFolderCache(t *testing.T) {
	dir := t.TempDir()
	c := cache.New(dir, false, 100)
	wrapper := NewClientWrapperWithCache(email.ServerConfig{Host: "h", Port: 1}, c, "acct")

	subscribed := []email.Folder{{Name: "INBOX"}, {Name: "Sent"}}
	data, _ := json.Marshal(subscribed)
	if err := c.Set(cache.GenerateAccountKey("acct", "folders", "subscribed"), data, time.Hour); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	for _, folder := range []string{"INBOX", "Sent", "OldFolder"} {
		key := cache.GenerateAccountKey("acct", "messages", folder)
		if err := c.Set(key, []byte("messages"), time.Hour); err != nil {
			t.Fatalf("seed failed: %v", err)
		}
	}

	if err := wrapper.CleanupUnsubscribedFolderCache(); err != nil {
		t.Fatalf("CleanupUnsubscribedFolderCache failed: %v", err)
	}

	if _, found, _ := c.Get(cache.GenerateAccountKey("acct", "messages", "OldFolder")); found {
		t.Error("unsubscribed folder cache should have been deleted")
	}
	for _, folder := range []string{"INBOX", "Sent"} {
		if _, found, _ := c.Get(cache.GenerateAccountKey("acct", "messages", folder)); !found {
			t.Errorf("subscribed folder %s cache should have been kept", folder)
		}
	}
}
