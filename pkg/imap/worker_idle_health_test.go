package imap

import (
	"testing"
	"time"

	"github.com/wltechblog/gommail/internal/email"
)

func TestIDLEHealthCheck(t *testing.T) {
	// Create a test worker
	cfg := &email.ServerConfig{
		Host:     "test.example.com",
		Port:     993,
		Username: "test@example.com",
		Password: "password",
		TLS:      true,
	}

	worker := NewIMAPWorker(cfg, "test@example.com", "test-key", nil)

	tests := []struct {
		name          string
		idleActive    bool
		idlePaused    bool
		lastActivity  time.Time
		expectHealthy bool
		expectError   bool
		errorContains string
	}{
		{
			name:          "IDLE not active - should be healthy",
			idleActive:    false,
			idlePaused:    false,
			lastActivity:  time.Time{}, // zero time
			expectHealthy: true,
			expectError:   false,
		},
		{
			name:          "IDLE active and paused - should be healthy",
			idleActive:    true,
			idlePaused:    true,
			lastActivity:  time.Now().Add(-1 * time.Hour),
			expectHealthy: true,
			expectError:   false,
		},
		{
			name:          "IDLE active with recent activity - should be healthy",
			idleActive:    true,
			idlePaused:    false,
			lastActivity:  time.Now().Add(-10 * time.Minute),
			expectHealthy: true,
			expectError:   false,
		},
		{
			name:          "IDLE active with old activity - should be unhealthy",
			idleActive:    true,
			idlePaused:    false,
			lastActivity:  time.Now().Add(-2 * time.Hour), // Much older than 2x IDLE timeout
			expectHealthy: false,
			expectError:   true,
			errorContains: "IDLE monitoring stuck",
		},
		{
			name:          "IDLE active at exactly 2x timeout - should be unhealthy",
			idleActive:    true,
			idlePaused:    false,
			lastActivity:  time.Now().Add(-58*time.Minute - 1*time.Second), // Just over 2x 29min timeout
			expectHealthy: false,
			expectError:   true,
			errorContains: "IDLE monitoring stuck",
		},
		{
			name:          "IDLE active just under 2x timeout - should be healthy",
			idleActive:    true,
			idlePaused:    false,
			lastActivity:  time.Now().Add(-57 * time.Minute), // Just under 2x 29min timeout
			expectHealthy: true,
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up worker state
			worker.mu.Lock()
			worker.idleActive = tt.idleActive
			worker.idlePaused = tt.idlePaused
			worker.lastIDLEActivity = tt.lastActivity
			worker.idleFolder = "INBOX"
			worker.mu.Unlock()

			// Run health check
			healthy, err := worker.checkIDLEHealth()

			// Verify results
			if healthy != tt.expectHealthy {
				t.Errorf("Expected healthy=%v, got %v", tt.expectHealthy, healthy)
			}

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got nil")
				} else if tt.errorContains != "" && !contains(err.Error(), tt.errorContains) {
					t.Errorf("Expected error to contain '%s', got: %v", tt.errorContains, err)
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
			}
		})
	}
}

func TestIDLEActivityTracking(t *testing.T) {
	cfg := &email.ServerConfig{
		Host:     "test.example.com",
		Port:     993,
		Username: "test@example.com",
		Password: "password",
		TLS:      true,
	}

	worker := NewIMAPWorker(cfg, "test@example.com", "test-key", nil)

	// Initially, lastIDLEActivity should be zero
	worker.mu.RLock()
	initialActivity := worker.lastIDLEActivity
	worker.mu.RUnlock()

	if !initialActivity.IsZero() {
		t.Errorf("Expected initial lastIDLEActivity to be zero, got %v", initialActivity)
	}

	// Simulate IDLE activity by setting the timestamp
	now := time.Now()
	worker.mu.Lock()
	worker.lastIDLEActivity = now
	worker.mu.Unlock()

	// Verify it was set
	worker.mu.RLock()
	updatedActivity := worker.lastIDLEActivity
	worker.mu.RUnlock()

	if updatedActivity.IsZero() {
		t.Error("Expected lastIDLEActivity to be set, but it's still zero")
	}

	// Check that the time is approximately correct (within 1 second)
	diff := updatedActivity.Sub(now)
	if diff < 0 {
		diff = -diff
	}
	if diff > time.Second {
		t.Errorf("Expected lastIDLEActivity to be close to now, but diff is %v", diff)
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
