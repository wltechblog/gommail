//go:build darwin

package notification

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/wltechblog/gommail/internal/logging"
)

// macNotifier implements desktop notifications on macOS using osascript
type macNotifier struct {
	config Config
	logger *logging.Logger
}

// newMacNotifier creates a new macOS notifier
func newMacNotifier(config Config, logger *logging.Logger) (Notifier, error) {
	notifier := &macNotifier{
		config: config,
		logger: logger,
	}

	return notifier, nil
}

// Show displays a notification using AppleScript
func (n *macNotifier) Show(notification Notification) error {
	appName := n.config.AppName
	if appName == "" {
		appName = "gommail client"
	}

	// Escape quotes in the text
	title := strings.ReplaceAll(notification.Title, `"`, `\"`)
	body := strings.ReplaceAll(notification.Body, `"`, `\"`)

	// Build AppleScript command
	script := fmt.Sprintf(`display notification "%s" with title "%s" sound name "Glass"`, body, title)

	// Execute the AppleScript
	cmd := exec.Command("osascript", "-e", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		n.logger.Error("Failed to show notification: %v, output: %s", err, string(output))
		return fmt.Errorf("failed to show notification: %w", err)
	}

	n.logger.Debug("Notification sent successfully")
	return nil
}

// IsSupported checks if osascript is available
func (n *macNotifier) IsSupported() bool {
	_, err := exec.LookPath("osascript")
	return err == nil
}

// Close cleans up resources (no-op for macOS)
func (n *macNotifier) Close() error {
	return nil
}
