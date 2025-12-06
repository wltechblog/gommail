package notification

import (
	"github.com/wltechblog/gommail/internal/logging"
)

// nullNotifier is a no-op notifier for unsupported platforms
type nullNotifier struct {
	logger *logging.Logger
}

// newNullNotifier creates a new null notifier
func newNullNotifier(logger *logging.Logger) Notifier {
	return &nullNotifier{
		logger: logger,
	}
}

// Show logs the notification instead of displaying it
func (n *nullNotifier) Show(notification Notification) error {
	n.logger.Info("Notification (unsupported platform): %s - %s", notification.Title, notification.Body)
	return nil
}

// IsSupported always returns false for null notifier
func (n *nullNotifier) IsSupported() bool {
	return false
}

// Close is a no-op for null notifier
func (n *nullNotifier) Close() error {
	return nil
}
