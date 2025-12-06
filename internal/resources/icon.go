// Package resources provides embedded application resources
package resources

import (
	_ "embed"
	"os"

	"fyne.io/fyne/v2"
)

//go:embed Icon.png
var iconData []byte

// GetAppIcon returns the embedded application icon as a Fyne resource
func GetAppIcon() *fyne.StaticResource {
	return fyne.NewStaticResource("Icon.png", iconData)
}

// GetAppIconPath returns a temporary file path for the icon (for notifications)
// This creates a temporary file with the icon data for systems that need file paths
func GetAppIconPath() (string, error) {
	// Create a temporary file with the icon data
	tmpFile, err := os.CreateTemp("", "mail-icon-*.png")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	// Write the embedded icon data to the temporary file
	if _, err := tmpFile.Write(iconData); err != nil {
		os.Remove(tmpFile.Name()) // Clean up on error
		return "", err
	}

	return tmpFile.Name(), nil
}
