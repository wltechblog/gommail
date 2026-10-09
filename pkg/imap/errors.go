package imap

import "fmt"

// wrapCommandError wraps an error from a command execution with context
// This helper reduces boilerplate error handling code throughout the IMAP package
func wrapCommandError(commandName string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s command failed: %w", commandName, err)
}

// wrapResponseError creates an error from a failed command response
// This helper reduces boilerplate error handling code throughout the IMAP package
func wrapResponseError(commandName string, response *IMAPResponse) error {
	if response == nil {
		return fmt.Errorf("%s command failed: nil response", commandName)
	}
	if response.Error != nil {
		return fmt.Errorf("%s command failed: %s", commandName, response.Error)
	}
	if !response.Success {
		return fmt.Errorf("%s command failed: unsuccessful response", commandName)
	}
	return nil
}
