package imap

import "github.com/wltechblog/gommail/internal/email"

// Type aliases for connection state types from email package
// These were previously defined in client.go but are now centralized in the email package

// ConnectionState represents the state of an IMAP connection
type ConnectionState = email.ConnectionState

// ConnectionEvent represents a connection state change event
type ConnectionEvent = email.ConnectionEvent

// Connection state constants
const (
	ConnectionStateDisconnected  = email.ConnectionStateDisconnected
	ConnectionStateConnecting    = email.ConnectionStateConnecting
	ConnectionStateConnected     = email.ConnectionStateConnected
	ConnectionStateReconnecting  = email.ConnectionStateReconnecting
	ConnectionStateFailed        = email.ConnectionStateFailed
)

