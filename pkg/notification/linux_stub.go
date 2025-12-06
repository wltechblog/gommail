//go:build !linux

package notification

import (
	"github.com/wltechblog/gommail/internal/logging"
)

// newLinuxNotifier is a stub for non-Linux platforms
func newLinuxNotifier(config Config, logger *logging.Logger) (Notifier, error) {
	return newNullNotifier(logger), nil
}
