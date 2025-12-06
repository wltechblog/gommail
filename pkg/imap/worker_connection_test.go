package imap

import (
	"fmt"
	"testing"
)

// TestIsConnectionError tests the isConnectionError function to ensure it properly
// identifies timeout errors as connection errors
func TestIsConnectionError(t *testing.T) {
	// Create a minimal worker just for testing the error classification
	worker := &IMAPWorker{}

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "NOOP command timeout",
			err:      fmt.Errorf("NOOP command timed out after 10 seconds"),
			expected: true,
		},
		{
			name:     "connection timed out",
			err:      fmt.Errorf("connection timed out"),
			expected: true,
		},
		{
			name:     "i/o timeout",
			err:      fmt.Errorf("i/o timeout"),
			expected: true,
		},
		{
			name:     "use of closed network connection",
			err:      fmt.Errorf("use of closed network connection"),
			expected: true,
		},
		{
			name:     "connection reset by peer",
			err:      fmt.Errorf("connection reset by peer"),
			expected: true,
		},
		{
			name:     "broken pipe",
			err:      fmt.Errorf("broken pipe"),
			expected: true,
		},
		{
			name:     "EOF",
			err:      fmt.Errorf("EOF"),
			expected: true,
		},
		{
			name:     "connection refused",
			err:      fmt.Errorf("connection refused"),
			expected: true,
		},
		{
			name:     "network is unreachable",
			err:      fmt.Errorf("network is unreachable"),
			expected: true,
		},
		{
			name:     "non-connection error",
			err:      fmt.Errorf("invalid credentials"),
			expected: false,
		},
		{
			name:     "another non-connection error",
			err:      fmt.Errorf("mailbox does not exist"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := worker.isConnectionError(tt.err)
			if result != tt.expected {
				t.Errorf("isConnectionError(%v) = %v, expected %v", tt.err, result, tt.expected)
			}
		})
	}
}

