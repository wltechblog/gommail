package ui

// Code in this file was mechanically split from main_window.go.

import (
	"fmt"
	_ "image/gif"  // Register GIF format
	_ "image/jpeg" // Register JPEG format
	_ "image/png"  // Register PNG format

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"

	"github.com/wltechblog/gommail/internal/resources"
)

// registerKeyboardShortcuts registers keyboard shortcuts with the window canvas
func (mw *MainWindow) registerKeyboardShortcuts() {
	canvas := mw.window.Canvas()

	// File shortcuts
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyN, Modifier: fyne.KeyModifierControl}, func(shortcut fyne.Shortcut) {
		mw.composeMessage()
	})
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyF5}, func(shortcut fyne.Shortcut) {
		mw.refreshAll()
	})
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyF5, Modifier: fyne.KeyModifierControl}, func(shortcut fyne.Shortcut) {
		mw.clearCache()
	})

	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyQ, Modifier: fyne.KeyModifierControl}, func(shortcut fyne.Shortcut) {
		mw.app.Quit()
	})

	// Message shortcuts
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyR, Modifier: fyne.KeyModifierControl}, func(shortcut fyne.Shortcut) {
		mw.replyToMessage()
	})
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyR, Modifier: fyne.KeyModifierControl | fyne.KeyModifierShift}, func(shortcut fyne.Shortcut) {
		mw.replyAllToMessage()
	})
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyL, Modifier: fyne.KeyModifierControl}, func(shortcut fyne.Shortcut) {
		mw.forwardMessage()
	})

	// Delete shortcuts - using DEL key (not letter "D")
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyDelete}, func(shortcut fyne.Shortcut) {
		mw.moveToTrash()
	})
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyDelete, Modifier: fyne.KeyModifierControl}, func(shortcut fyne.Shortcut) {
		mw.deleteMessage()
	})

	// Select all / deselect - multi-select support for the message list
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyA, Modifier: fyne.KeyModifierControl}, func(shortcut fyne.Shortcut) {
		if len(mw.messages) == 0 {
			return
		}
		mw.selectAllMessages()
		mw.refreshMessageList()
		mw.statusBar.SetText(fmt.Sprintf("Selected %d messages", mw.selectionManager.GetSelectionCount()))
	})

	// View shortcuts
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyH, Modifier: fyne.KeyModifierControl}, func(shortcut fyne.Shortcut) {
		mw.toggleHTMLView()
	})

	// Edit shortcuts
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyC, Modifier: fyne.KeyModifierControl}, func(shortcut fyne.Shortcut) {
		mw.copyMessageText()
	})
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyF, Modifier: fyne.KeyModifierControl | fyne.KeyModifierShift}, func(shortcut fyne.Shortcut) {
		mw.showSearch()
	})

	// Selection shortcuts
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyA, Modifier: fyne.KeyModifierControl}, func(shortcut fyne.Shortcut) {
		mw.selectAllMessages()
	})
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyEscape}, func(shortcut fyne.Shortcut) {
		mw.clearSelection()
	})

	// Tools shortcuts
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyComma, Modifier: fyne.KeyModifierControl}, func(shortcut fyne.Shortcut) {
		mw.showSettings()
	})

	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyB, Modifier: fyne.KeyModifierControl | fyne.KeyModifierShift}, func(shortcut fyne.Shortcut) {
		mw.showAddressbook()
	})

}

// showAbout displays the about dialog
func (mw *MainWindow) showAbout() {
	about := fmt.Sprintf(`gommail client
Version %s

A high-performance email client written in Go using the Fyne UI toolkit.

Features:
• Advanced IMAP/SMTP Support
• Multi-Account Management
• Attachment Management
• Filesystem Caching
• Cross-Platform Support

Built with ❤️ using Go and Fyne`, resources.AppVersion)

	dialog.ShowInformation("About gommail client", about, mw.window)
}
