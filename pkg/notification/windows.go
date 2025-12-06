//go:build windows

package notification

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/wltechblog/gommail/internal/logging"
)

// windowsNotifier implements desktop notifications on Windows using PowerShell
type windowsNotifier struct {
	config Config
	logger *logging.Logger
}

// newWindowsNotifier creates a new Windows notifier
func newWindowsNotifier(config Config, logger *logging.Logger) (Notifier, error) {
	notifier := &windowsNotifier{
		config: config,
		logger: logger,
	}

	return notifier, nil
}

// Show displays a notification using PowerShell and Windows Toast notifications
func (n *windowsNotifier) Show(notification Notification) error {
	appName := n.config.AppName
	if appName == "" {
		appName = "gommail client"
	}

	// Escape quotes and special characters
	title := strings.ReplaceAll(notification.Title, `"`, `'`)
	body := strings.ReplaceAll(notification.Body, `"`, `'`)
	body = strings.ReplaceAll(body, "\n", " - ")

	// Build PowerShell command for toast notification
	script := fmt.Sprintf(`
		[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
		[Windows.UI.Notifications.ToastNotification, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
		[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null

		$template = @"
		<toast>
			<visual>
				<binding template="ToastGeneric">
					<text>%s</text>
					<text>%s</text>
				</binding>
			</visual>
		</toast>
"@

		$xml = New-Object Windows.Data.Xml.Dom.XmlDocument
		$xml.LoadXml($template)
		$toast = New-Object Windows.UI.Notifications.ToastNotification $xml
		[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier("%s").Show($toast)
	`, title, body, appName)

	// Execute PowerShell command
	cmd := exec.Command("powershell", "-Command", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		n.logger.Error("Failed to show notification: %v, output: %s", err, string(output))
		// Fallback to simple message box
		return n.showFallbackNotification(notification)
	}

	n.logger.Debug("Toast notification sent successfully")
	return nil
}

// showFallbackNotification shows a simple message box as fallback
func (n *windowsNotifier) showFallbackNotification(notification Notification) error {
	title := strings.ReplaceAll(notification.Title, `"`, `'`)
	body := strings.ReplaceAll(notification.Body, `"`, `'`)
	body = strings.ReplaceAll(body, "\n", " - ")

	// Use msg command as fallback
	script := fmt.Sprintf(`msg * /time:5 "%s: %s"`, title, body)
	cmd := exec.Command("cmd", "/C", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		n.logger.Error("Fallback notification failed: %v, output: %s", err, string(output))
		return fmt.Errorf("failed to show notification: %w", err)
	}

	n.logger.Debug("Fallback notification sent successfully")
	return nil
}

// IsSupported checks if PowerShell is available
func (n *windowsNotifier) IsSupported() bool {
	_, err := exec.LookPath("powershell")
	return err == nil
}

// Close cleans up resources (no-op for Windows)
func (n *windowsNotifier) Close() error {
	return nil
}
