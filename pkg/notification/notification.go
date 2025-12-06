// Package notification provides cross-platform desktop notification support
package notification

import (
	"fmt"
	"runtime"
	"time"

	"github.com/wltechblog/gommail/internal/logging"
)

// Notification represents a desktop notification
type Notification struct {
	Title   string        // Notification title
	Body    string        // Notification body text
	Icon    string        // Path to icon file (optional)
	Timeout time.Duration // Auto-dismiss timeout (0 = no timeout)
}

// Notifier interface defines the notification system
type Notifier interface {
	// Show displays a desktop notification
	Show(notification Notification) error

	// IsSupported returns true if notifications are supported on this platform
	IsSupported() bool

	// Close cleans up the notifier resources
	Close() error
}

// Config contains notification system configuration
type Config struct {
	Enabled        bool          // Enable/disable notifications
	DefaultTimeout time.Duration // Default notification timeout
	AppName        string        // Application name for notifications
	AppIcon        string        // Default application icon path
}

// Manager manages the notification system
type Manager struct {
	config   Config
	notifier Notifier
	logger   *logging.Logger
}

// NewManager creates a new notification manager
func NewManager(config Config) (*Manager, error) {
	logger := logging.NewComponent("notification")

	// Create platform-specific notifier
	notifier, err := createPlatformNotifier(config, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create platform notifier: %w", err)
	}

	manager := &Manager{
		config:   config,
		notifier: notifier,
		logger:   logger,
	}

	logger.Info("Notification manager initialized for platform: %s", runtime.GOOS)
	return manager, nil
}

// Show displays a notification if enabled
func (m *Manager) Show(notification Notification) error {
	if !m.config.Enabled {
		m.logger.Debug("Notifications disabled, skipping: %s", notification.Title)
		return nil
	}

	if !m.notifier.IsSupported() {
		m.logger.Warn("Notifications not supported on this platform")
		return fmt.Errorf("notifications not supported")
	}

	// Apply default timeout if not specified
	if notification.Timeout == 0 {
		notification.Timeout = m.config.DefaultTimeout
	}

	// Apply default icon if not specified
	if notification.Icon == "" {
		notification.Icon = m.config.AppIcon
	}

	m.logger.Debug("Showing notification: %s", notification.Title)
	return m.notifier.Show(notification)
}

// ShowNewMessage shows a notification for a new email message
func (m *Manager) ShowNewMessage(sender, subject, mailbox string) error {
	// Truncate long subjects
	displaySubject := subject
	if len(displaySubject) > 50 {
		displaySubject = displaySubject[:47] + "..."
	}

	// Truncate long sender names
	displaySender := sender
	if len(displaySender) > 30 {
		displaySender = displaySender[:27] + "..."
	}

	notification := Notification{
		Title:   fmt.Sprintf("New message in %s", mailbox),
		Body:    fmt.Sprintf("From: %s\nSubject: %s", displaySender, displaySubject),
		Timeout: m.config.DefaultTimeout,
	}

	return m.Show(notification)
}

// ShowNewMessages shows a batched notification for multiple new email messages
// This helps avoid D-Bus rate limiting when many messages arrive simultaneously
func (m *Manager) ShowNewMessages(messages []MessageInfo, mailbox string) error {
	if len(messages) == 0 {
		return nil
	}

	// For single message, use the existing method
	if len(messages) == 1 {
		msg := messages[0]
		return m.ShowNewMessage(msg.Sender, msg.Subject, mailbox)
	}

	// For multiple messages, create a summary notification
	var title string
	var body string

	if len(messages) <= 3 {
		// Show individual messages for small batches
		title = fmt.Sprintf("%d new messages in %s", len(messages), mailbox)
		for i, msg := range messages {
			// Truncate long sender names and subjects
			displaySender := msg.Sender
			if len(displaySender) > 25 {
				displaySender = displaySender[:22] + "..."
			}
			displaySubject := msg.Subject
			if len(displaySubject) > 40 {
				displaySubject = displaySubject[:37] + "..."
			}
			if displaySubject == "" {
				displaySubject = "(No Subject)"
			}

			if i > 0 {
				body += "\n"
			}
			body += fmt.Sprintf("• %s: %s", displaySender, displaySubject)
		}
	} else {
		// Show summary for large batches
		title = fmt.Sprintf("%d new messages in %s", len(messages), mailbox)

		// Show first 2 messages and indicate there are more
		for i := 0; i < 2 && i < len(messages); i++ {
			msg := messages[i]
			displaySender := msg.Sender
			if len(displaySender) > 25 {
				displaySender = displaySender[:22] + "..."
			}
			displaySubject := msg.Subject
			if len(displaySubject) > 40 {
				displaySubject = displaySubject[:37] + "..."
			}
			if displaySubject == "" {
				displaySubject = "(No Subject)"
			}

			if i > 0 {
				body += "\n"
			}
			body += fmt.Sprintf("• %s: %s", displaySender, displaySubject)
		}

		if len(messages) > 2 {
			body += fmt.Sprintf("\n... and %d more messages", len(messages)-2)
		}
	}

	notification := Notification{
		Title:   title,
		Body:    body,
		Timeout: m.config.DefaultTimeout,
	}

	return m.Show(notification)
}

// MessageInfo represents basic message information for notifications
type MessageInfo struct {
	Sender  string
	Subject string
}

// IsEnabled returns true if notifications are enabled
func (m *Manager) IsEnabled() bool {
	return m.config.Enabled
}

// IsSupported returns true if notifications are supported
func (m *Manager) IsSupported() bool {
	return m.notifier.IsSupported()
}

// SetEnabled enables or disables notifications
func (m *Manager) SetEnabled(enabled bool) {
	m.config.Enabled = enabled
	m.logger.Info("Notifications %s", map[bool]string{true: "enabled", false: "disabled"}[enabled])
}

// Close cleans up the notification manager
func (m *Manager) Close() error {
	m.logger.Debug("Closing notification manager")
	return m.notifier.Close()
}

// createPlatformNotifier creates the appropriate notifier for the current platform
func createPlatformNotifier(config Config, logger *logging.Logger) (Notifier, error) {
	switch runtime.GOOS {
	case "linux":
		return newLinuxNotifier(config, logger)
	case "darwin":
		return newMacNotifier(config, logger)
	case "windows":
		return newWindowsNotifier(config, logger)
	default:
		logger.Warn("Unsupported platform for notifications: %s", runtime.GOOS)
		return newNullNotifier(logger), nil
	}
}

// Platform-specific constructor functions are implemented in platform-specific files
