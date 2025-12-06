//go:build !windows

package notification

import (
	"github.com/wltechblog/gommail/internal/logging"
)

// newWindowsNotifier is a stub for non-Windows platforms
func newWindowsNotifier(config Config, logger *logging.Logger) (Notifier, error) {
	return newNullNotifier(logger), nil
}
