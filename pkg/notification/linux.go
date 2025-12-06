//go:build linux

package notification

import (
	"fmt"

	"github.com/godbus/dbus/v5"
	"github.com/wltechblog/gommail/internal/logging"
)

// linuxNotifier implements desktop notifications on Linux using D-Bus
type linuxNotifier struct {
	config Config
	logger *logging.Logger
	conn   *dbus.Conn
}

// newLinuxNotifier creates a new Linux notifier using D-Bus
func newLinuxNotifier(config Config, logger *logging.Logger) (Notifier, error) {
	// Connect to session bus
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to D-Bus session bus: %w", err)
	}

	notifier := &linuxNotifier{
		config: config,
		logger: logger,
		conn:   conn,
	}

	// Test if notification service is available
	if !notifier.IsSupported() {
		logger.Warn("Desktop notification service not available")
	}

	return notifier, nil
}

// Show displays a notification using the freedesktop.org notification specification
func (n *linuxNotifier) Show(notification Notification) error {
	obj := n.conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")

	// Prepare notification parameters
	appName := n.config.AppName
	if appName == "" {
		appName = "gommail client"
	}

	replaceID := uint32(0) // 0 means don't replace any existing notification
	appIcon := notification.Icon
	if appIcon == "" {
		appIcon = "mail-unread" // Standard icon name
	}

	summary := notification.Title
	body := notification.Body
	actions := []string{} // No actions for now
	hints := map[string]dbus.Variant{
		"urgency": dbus.MakeVariant(byte(1)), // Normal urgency
	}

	// Convert timeout to milliseconds (-1 means use server default)
	timeoutMs := int32(-1)
	if notification.Timeout > 0 {
		timeoutMs = int32(notification.Timeout.Milliseconds())
	}

	// Call the Notify method
	call := obj.Call("org.freedesktop.Notifications.Notify", 0,
		appName, replaceID, appIcon, summary, body, actions, hints, timeoutMs)

	if call.Err != nil {
		return fmt.Errorf("failed to send notification: %w", call.Err)
	}

	// Get the notification ID from the response
	var notificationID uint32
	if err := call.Store(&notificationID); err != nil {
		n.logger.Warn("Failed to get notification ID: %v", err)
	} else {
		n.logger.Debug("Notification sent with ID: %d", notificationID)
	}

	return nil
}

// IsSupported checks if the notification service is available
func (n *linuxNotifier) IsSupported() bool {
	if n.conn == nil {
		return false
	}

	// Check if the notification service exists
	obj := n.conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	call := obj.Call("org.freedesktop.Notifications.GetCapabilities", 0)

	return call.Err == nil
}

// Close closes the D-Bus connection
func (n *linuxNotifier) Close() error {
	if n.conn != nil {
		return n.conn.Close()
	}
	return nil
}
