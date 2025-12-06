package imap

import (
	"errors"
	"strings"
	"testing"
)

func TestWrapCommandError(t *testing.T) {
	tests := []struct {
		name        string
		commandName string
		err         error
		wantNil     bool
		wantContain []string
	}{
		{
			name:        "nil error returns nil",
			commandName: "CONNECT",
			err:         nil,
			wantNil:     true,
		},
		{
			name:        "wraps error with command name",
			commandName: "LIST",
			err:         errors.New("connection lost"),
			wantNil:     false,
			wantContain: []string{"LIST command failed", "connection lost"},
		},
		{
			name:        "preserves error chain",
			commandName: "FETCH",
			err:         errors.New("timeout"),
			wantNil:     false,
			wantContain: []string{"FETCH command failed", "timeout"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapCommandError(tt.commandName, tt.err)

			if tt.wantNil {
				if got != nil {
					t.Errorf("wrapCommandError() = %v, want nil", got)
				}
				return
			}

			if got == nil {
				t.Errorf("wrapCommandError() = nil, want error")
				return
			}

			gotStr := got.Error()
			for _, want := range tt.wantContain {
				if !strings.Contains(gotStr, want) {
					t.Errorf("wrapCommandError() = %q, want to contain %q", gotStr, want)
				}
			}

			// Test that error can be unwrapped
			if tt.err != nil && !errors.Is(got, tt.err) {
				t.Errorf("wrapCommandError() error chain broken, cannot unwrap to original error")
			}
		})
	}
}

func TestWrapResponseError(t *testing.T) {
	tests := []struct {
		name        string
		commandName string
		response    *IMAPResponse
		wantNil     bool
		wantContain []string
	}{
		{
			name:        "nil response",
			commandName: "SELECT",
			response:    nil,
			wantNil:     false,
			wantContain: []string{"SELECT command failed", "nil response"},
		},
		{
			name:        "successful response returns nil",
			commandName: "NOOP",
			response:    &IMAPResponse{Success: true, Error: nil},
			wantNil:     true,
		},
		{
			name:        "response with error",
			commandName: "DELETE",
			response:    &IMAPResponse{Success: false, Error: errors.New("folder not found")},
			wantNil:     false,
			wantContain: []string{"DELETE command failed", "folder not found"},
		},
		{
			name:        "unsuccessful response without error",
			commandName: "CREATE",
			response:    &IMAPResponse{Success: false, Error: nil},
			wantNil:     false,
			wantContain: []string{"CREATE command failed", "unsuccessful response"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapResponseError(tt.commandName, tt.response)

			if tt.wantNil {
				if got != nil {
					t.Errorf("wrapResponseError() = %v, want nil", got)
				}
				return
			}

			if got == nil {
				t.Errorf("wrapResponseError() = nil, want error")
				return
			}

			gotStr := got.Error()
			for _, want := range tt.wantContain {
				if !strings.Contains(gotStr, want) {
					t.Errorf("wrapResponseError() = %q, want to contain %q", gotStr, want)
				}
			}
		})
	}
}

func TestWrapTimeoutError(t *testing.T) {
	tests := []struct {
		name        string
		commandName string
		wantContain []string
	}{
		{
			name:        "timeout error",
			commandName: "FETCH",
			wantContain: []string{"FETCH command timed out"},
		},
		{
			name:        "another timeout",
			commandName: "SEARCH",
			wantContain: []string{"SEARCH command timed out"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapTimeoutError(tt.commandName)

			if got == nil {
				t.Errorf("wrapTimeoutError() = nil, want error")
				return
			}

			gotStr := got.Error()
			for _, want := range tt.wantContain {
				if !strings.Contains(gotStr, want) {
					t.Errorf("wrapTimeoutError() = %q, want to contain %q", gotStr, want)
				}
			}
		})
	}
}

func TestWrapValidationError(t *testing.T) {
	tests := []struct {
		name        string
		commandName string
		reason      string
		wantContain []string
	}{
		{
			name:        "validation error",
			commandName: "SELECT",
			reason:      "folder name is empty",
			wantContain: []string{"SELECT command failed", "folder name is empty"},
		},
		{
			name:        "another validation error",
			commandName: "MOVE",
			reason:      "invalid UID",
			wantContain: []string{"MOVE command failed", "invalid UID"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapValidationError(tt.commandName, tt.reason)

			if got == nil {
				t.Errorf("wrapValidationError() = nil, want error")
				return
			}

			gotStr := got.Error()
			for _, want := range tt.wantContain {
				if !strings.Contains(gotStr, want) {
					t.Errorf("wrapValidationError() = %q, want to contain %q", gotStr, want)
				}
			}
		})
	}
}
