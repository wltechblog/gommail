// Package controllers provides UI controller implementations
package controllers

import (
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/wltechblog/gommail/internal/config"
	"github.com/wltechblog/gommail/internal/email"
	"github.com/wltechblog/gommail/internal/logging"
	"github.com/wltechblog/gommail/pkg/imap"
)

// FolderControllerImpl implements the FolderController interface.
// It manages folder list, selection, and folder-related operations.
type FolderControllerImpl struct {
	// Folder state
	folders       []email.Folder
	currentFolder string
	folderLoading bool

	// UI components
	folderList *widget.List

	// Dependencies
	imapClient *imap.ClientWrapper
	window     fyne.Window

	// Callbacks
	onFolderSelected     func(folder string)
	onFoldersChanged     func()
	onFolderCountUpdated func(folderName string, count int)

	// Logger
	logger *logging.Logger
}

// NewFolderController creates a new FolderController instance.
func NewFolderController(window fyne.Window) *FolderControllerImpl {
	return &FolderControllerImpl{
		folders:       make([]email.Folder, 0),
		currentFolder: "",
		folderLoading: false,
		window:        window,
		logger:        logging.NewComponent("folder-controller"),
	}
}

// SetCallbacks sets the callback functions for folder events.
func (fc *FolderControllerImpl) SetCallbacks(
	onFolderSelected func(folder string),
	onFoldersChanged func(),
	onFolderCountUpdated func(folderName string, count int),
) {
	fc.onFolderSelected = onFolderSelected
	fc.onFoldersChanged = onFoldersChanged
	fc.onFolderCountUpdated = onFolderCountUpdated
}

// SetIMAPClient sets the IMAP client for folder operations.
func (fc *FolderControllerImpl) SetIMAPClient(client *imap.ClientWrapper) {
	fc.imapClient = client
}

// SetFolderList sets the folder list widget.
func (fc *FolderControllerImpl) SetFolderList(list *widget.List) {
	fc.folderList = list
}

// SelectFolder handles folder selection.
func (fc *FolderControllerImpl) SelectFolder(folder string) {
	if folder == "" {
		// Allow clearing the current folder by calling ClearCurrentFolder instead
		fc.ClearCurrentFolder()
		return
	}

	// Check if the folder exists in our current folder list
	folderExists := false
	for _, f := range fc.folders {
		if f.Name == folder {
			folderExists = true
			break
		}
	}

	if !folderExists {
		fc.logger.Warn("Folder %s not found in folder list", folder)
		return
	}

	fc.currentFolder = folder
	fc.logger.Debug("Selected folder: %s", folder)

	// Refresh folder list to update highlighting
	if fc.folderList != nil {
		fc.folderList.Refresh()
	}

	// Notify callback
	if fc.onFolderSelected != nil {
		fc.onFolderSelected(folder)
	}
}

// ClearCurrentFolder clears the currently selected folder.
func (fc *FolderControllerImpl) ClearCurrentFolder() {
	fc.currentFolder = ""
	fc.logger.Debug("Cleared current folder")

	// Refresh folder list to update highlighting
	if fc.folderList != nil {
		fc.folderList.Refresh()
	}
}

// GetCurrentFolder returns the currently selected folder.
func (fc *FolderControllerImpl) GetCurrentFolder() string {
	return fc.currentFolder
}

// GetFolders returns the list of folders.
func (fc *FolderControllerImpl) GetFolders() []email.Folder {
	return fc.folders
}

// SetFolders sets the folder list and refreshes the UI.
func (fc *FolderControllerImpl) SetFolders(folders []email.Folder) {
	fc.folders = folders
	fc.logger.Debug("Set %d folders", len(folders))

	if fc.folderList != nil {
		fc.folderList.Refresh()
	}

	if fc.onFoldersChanged != nil {
		fc.onFoldersChanged()
	}
}

// SortFolders sorts folders with INBOX first, then alphabetically.
func (fc *FolderControllerImpl) SortFolders(folders []email.Folder) []email.Folder {
	if len(folders) == 0 {
		return folders
	}

	// Create a copy to avoid modifying the original
	sorted := make([]email.Folder, len(folders))
	copy(sorted, folders)

	// Sort with INBOX first, then alphabetically
	sort.Slice(sorted, func(i, j int) bool {
		// INBOX always comes first
		if sorted[i].Name == "INBOX" {
			return true
		}
		if sorted[j].Name == "INBOX" {
			return false
		}

		// Common folders in preferred order
		commonFolders := map[string]int{
			"Drafts":  1,
			"Sent":    2,
			"Trash":   3,
			"Junk":    4,
			"Spam":    5,
			"Archive": 6,
		}

		iOrder, iIsCommon := commonFolders[sorted[i].Name]
		jOrder, jIsCommon := commonFolders[sorted[j].Name]

		// Both are common folders - sort by order
		if iIsCommon && jIsCommon {
			return iOrder < jOrder
		}

		// One is common, one is not - common comes first
		if iIsCommon {
			return true
		}
		if jIsCommon {
			return false
		}

		// Both are custom folders - sort alphabetically (case-insensitive)
		return strings.ToLower(sorted[i].Name) < strings.ToLower(sorted[j].Name)
	})

	return sorted
}

// IsFolderLoading returns whether folders are currently being loaded.
func (fc *FolderControllerImpl) IsFolderLoading() bool {
	return fc.folderLoading
}

// SetFolderLoading sets the folder loading state.
func (fc *FolderControllerImpl) SetFolderLoading(loading bool) {
	fc.folderLoading = loading
}

// GetFolderMessageCount returns the message count for a specific folder.
func (fc *FolderControllerImpl) GetFolderMessageCount(folderName string) int {
	for _, folder := range fc.folders {
		if folder.Name == folderName {
			return folder.MessageCount
		}
	}
	return 0
}

// UpdateFolderMessageCount updates the message count for a specific folder.
func (fc *FolderControllerImpl) UpdateFolderMessageCount(folderName string, newCount int) {
	for i, folder := range fc.folders {
		if folder.Name == folderName {
			fc.folders[i].MessageCount = newCount
			fc.logger.Debug("Updated message count for folder %s: %d", folderName, newCount)

			if fc.folderList != nil {
				fc.folderList.Refresh()
			}

			if fc.onFolderCountUpdated != nil {
				fc.onFolderCountUpdated(folderName, newCount)
			}
			return
		}
	}
	fc.logger.Warn("Folder %s not found for message count update", folderName)
}

// FoldersChanged checks if two folder lists are different.
func (fc *FolderControllerImpl) FoldersChanged(current, updated []email.Folder) bool {
	if len(current) != len(updated) {
		return true
	}

	// Create maps for quick lookup
	currentMap := make(map[string]email.Folder)
	for _, folder := range current {
		currentMap[folder.Name] = folder
	}

	// Check if any folder is different
	for _, updatedFolder := range updated {
		currentFolder, exists := currentMap[updatedFolder.Name]
		if !exists {
			return true // New folder
		}

		// Check if folder properties changed
		if currentFolder.MessageCount != updatedFolder.MessageCount ||
			currentFolder.UnreadCount != updatedFolder.UnreadCount {
			return true
		}
	}

	return false
}

// HandleDeletedFolder handles the case where the currently selected folder has been deleted.
func (fc *FolderControllerImpl) HandleDeletedFolder(deletedFolder string) {
	fc.logger.Info("handleDeletedFolder called for folder: %s (current folder: %s)", deletedFolder, fc.currentFolder)

	// Only handle if the deleted folder is the currently selected one
	if fc.currentFolder == deletedFolder {
		fc.logger.Info("Currently selected folder %s was deleted, switching to INBOX", deletedFolder)

		// Clear current folder
		fc.currentFolder = ""

		// Try to select INBOX as fallback
		for i, folder := range fc.folders {
			if folder.Name == "INBOX" {
				if fc.folderList != nil {
					fc.folderList.Select(i)
				}
				fc.SelectFolder("INBOX")
				return
			}
		}

		// If no INBOX, select first available folder
		if len(fc.folders) > 0 {
			if fc.folderList != nil {
				fc.folderList.Select(0)
			}
			fc.SelectFolder(fc.folders[0].Name)
		}
	}
}

// GetFolderList returns the folder list widget.
func (fc *FolderControllerImpl) GetFolderList() *widget.List {
	return fc.folderList
}

// ShowFolderSubscriptions shows the folder subscription dialog.
// This is a placeholder - the actual implementation will be in MainWindow
// since it requires access to RefreshCoordinator and other components.
func (fc *FolderControllerImpl) ShowFolderSubscriptions() {
	fc.logger.Debug("ShowFolderSubscriptions called - should be implemented by MainWindow")
}

// ShowFolderSubscriptionsForAccount shows folder subscriptions for a specific account.
// This is a placeholder - the actual implementation will be in MainWindow.
func (fc *FolderControllerImpl) ShowFolderSubscriptionsForAccount(account *config.Account) {
	fc.logger.Debug("ShowFolderSubscriptionsForAccount called for account: %s", account.Name)
}

// GetFolderNames returns a list of folder names (excluding the current folder if specified).
func (fc *FolderControllerImpl) GetFolderNames(excludeCurrent bool) []string {
	var names []string
	for _, folder := range fc.folders {
		if excludeCurrent && folder.Name == fc.currentFolder {
			continue
		}
		names = append(names, folder.Name)
	}
	return names
}

// FindFolderByName finds a folder by name.
func (fc *FolderControllerImpl) FindFolderByName(name string) *email.Folder {
	for i := range fc.folders {
		if fc.folders[i].Name == name {
			return &fc.folders[i]
		}
	}
	return nil
}

// AutoSelectInbox automatically selects the INBOX folder if available.
func (fc *FolderControllerImpl) AutoSelectInbox() bool {
	for i, folder := range fc.folders {
		if folder.Name == "INBOX" {
			if fc.folderList != nil {
				fc.folderList.Select(i)
			}
			fc.SelectFolder("INBOX")
			return true
		}
	}
	return false
}

// AutoSelectFirstFolder automatically selects the first folder if available.
func (fc *FolderControllerImpl) AutoSelectFirstFolder() bool {
	if len(fc.folders) > 0 {
		if fc.folderList != nil {
			fc.folderList.Select(0)
		}
		fc.SelectFolder(fc.folders[0].Name)
		return true
	}
	return false
}

// ShowMoveToFolderDialog shows a dialog to select a target folder for moving messages.
func (fc *FolderControllerImpl) ShowMoveToFolderDialog(onFolderSelected func(folder string)) {
	folderNames := fc.GetFolderNames(true) // Exclude current folder

	if len(folderNames) == 0 {
		dialog.ShowInformation("No Folders", "No other folders available", fc.window)
		return
	}

	folderSelect := widget.NewSelect(folderNames, func(selected string) {
		if selected != "" && onFolderSelected != nil {
			onFolderSelected(selected)
		}
	})
	folderSelect.PlaceHolder = "Select a folder..."

	content := fyne.NewContainerWithLayout(
		nil,
		widget.NewLabel("Select destination folder:"),
		folderSelect,
	)

	dialog.ShowCustom("Move to Folder", "Close", content, fc.window)
}
