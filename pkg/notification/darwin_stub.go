//go:build !darwin

package notification

import (
	"github.com/wltechblog/gommail/internal/logging"
)

// newMacNotifier is a stub for non-macOS platforms
func newMacNotifier(config Config, logger *logging.Logger) (Notifier, error) {
	return newNullNotifier(logger), nil
}
