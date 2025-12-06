package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/wltechblog/gommail/internal/email"
	"github.com/wltechblog/gommail/internal/logging"
	"github.com/wltechblog/gommail/internal/resources"
)

// FolderSubscriptionDialog represents a window for managing folder subscriptions
type FolderSubscriptionDialog struct {
	app        fyne.App
	window     fyne.Window
	imapClient email.IMAPClient
	logger     *logging.Logger

	// UI components
	folderList     *widget.List
	subscribeBtn   *widget.Button
	unsubscribeBtn *widget.Button
	createBtn      *widget.Button
	deleteBtn      *widget.Button
	refreshBtn     *widget.Button
	statusLabel    *widget.Label

	// Data
	allFolders        []email.Folder
	subscribedFolders []email.Folder
	selectedFolder    *email.Folder

	// State management
	updatingCheckboxes bool // Flag to prevent recursive checkbox updates

	// Callbacks
	onClose         func()                  // Called when dialog is closed to refresh main window
	onFolderAdded   func()                  // Called when a folder is created to reload main window folders
	onFolderDeleted func(folderName string) // Called when a folder is deleted to handle current selection
}

// NewFolderSubscriptionDialog creates a new folder subscription window
func NewFolderSubscriptionDialog(app fyne.App, imapClient email.IMAPClient, logger *logging.Logger, onClose func(), onFolderAdded func(), onFolderDeleted func(string)) *FolderSubscriptionDialog {
	window := app.NewWindow("Folder Subscriptions")
	window.Resize(fyne.NewSize(600, 700))

	// Set window icon
	iconResource := resources.GetAppIcon()
	window.SetIcon(iconResource)

	fsd := &FolderSubscriptionDialog{
		app:             app,
		window:          window,
		imapClient:      imapClient,
		logger:          logger,
		onClose:         onClose,
		onFolderAdded:   onFolderAdded,
		onFolderDeleted: onFolderDeleted,
	}

	fsd.createUI()

	// Set callback for when window is closed
	if fsd.onClose != nil {
		window.SetCloseIntercept(func() {
			fsd.onClose()
			window.Close()
		})
	}

	return fsd
}

// createUI creates the dialog UI components
func (fsd *FolderSubscriptionDialog) createUI() {
	// Status label
	fsd.statusLabel = widget.NewLabel("Loading folders...")

	// Folder list
	fsd.folderList = widget.NewList(
		func() int {
			return len(fsd.allFolders)
		},
		func() fyne.CanvasObject {
			// Create a container with checkbox and folder name
			check := widget.NewCheck("", nil)
			label := widget.NewLabel("Folder Name")
			label.Wrapping = fyne.TextWrapOff

			// Use border layout to give label more space
			return container.NewBorder(nil, nil, check, nil, label)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id >= len(fsd.allFolders) {
				return
			}

			folder := fsd.allFolders[id]
			cont := obj.(*fyne.Container)

			// Find the checkbox and label in the container
			var check *widget.Check
			var label *widget.Label

			for _, obj := range cont.Objects {
				if c, ok := obj.(*widget.Check); ok {
					check = c
				} else if l, ok := obj.(*widget.Label); ok {
					label = l
				}
			}

			// Set folder name
			label.SetText(folder.Name)

			// Set subscription status without triggering callback
			isSubscribed := fsd.isFolderSubscribed(folder.Name)
			fsd.updatingCheckboxes = true
			check.SetChecked(isSubscribed)
			fsd.updatingCheckboxes = false

			// Handle checkbox changes
			check.OnChanged = func(checked bool) {
				// Prevent recursive updates
				if fsd.updatingCheckboxes {
					return
				}

				if checked {
					fsd.subscribeToFolder(folder.Name)
				} else {
					fsd.unsubscribeFromFolder(folder.Name)
				}
			}
		},
	)

	// Selection handler for folder list
	fsd.folderList.OnSelected = func(id widget.ListItemID) {
		if id < len(fsd.allFolders) {
			fsd.selectedFolder = &fsd.allFolders[id]
			fsd.updateButtonStates()
		}
	}

	// Action buttons
	fsd.subscribeBtn = widget.NewButton("Subscribe", func() {
		if fsd.selectedFolder != nil {
			fsd.subscribeToFolder(fsd.selectedFolder.Name)
		}
	})

	fsd.unsubscribeBtn = widget.NewButton("Unsubscribe", func() {
		if fsd.selectedFolder != nil {
			fsd.unsubscribeFromFolder(fsd.selectedFolder.Name)
		}
	})

	fsd.createBtn = widget.NewButton("Create Folder", func() {
		fsd.showCreateFolderDialog()
	})

	fsd.deleteBtn = widget.NewButton("Delete Folder", func() {
		if fsd.selectedFolder != nil {
			fsd.showDeleteFolderDialog(fsd.selectedFolder.Name)
		}
	})

	fsd.refreshBtn = widget.NewButton("Refresh", func() {
		fsd.refreshFolders()
	})

	// Initially disable action buttons
	fsd.updateButtonStates()

	// Create button container with better spacing
	buttonContainer := container.NewBorder(
		nil, nil,
		container.NewHBox(fsd.subscribeBtn, fsd.unsubscribeBtn),         // left side
		container.NewHBox(fsd.createBtn, fsd.deleteBtn, fsd.refreshBtn), // right side
		nil, // center
	)

	// Create main content with proper layout
	header := container.NewVBox(
		widget.NewLabel("Manage Folder Subscriptions"),
		widget.NewSeparator(),
		fsd.statusLabel,
	)

	// Put folder list in a scroll container and give it most of the space
	folderScroll := container.NewScroll(fsd.folderList)
	folderScroll.SetMinSize(fyne.NewSize(450, 400))

	// Create main content using border layout to give folder list priority
	content := container.NewBorder(
		header,          // top
		buttonContainer, // bottom
		nil,             // left
		nil,             // right
		folderScroll,    // center (gets remaining space)
	)

	// Set window content
	fsd.window.SetContent(content)
}

// Show displays the folder subscription window
func (fsd *FolderSubscriptionDialog) Show() {
	// Load folders when showing the window
	go fsd.refreshFolders()
	fsd.window.Show()
}

// refreshFoldersWithRetry refreshes the folder list with connection error handling
func (fsd *FolderSubscriptionDialog) refreshFoldersWithRetry() error {
	fsd.logger.Debug("Refreshing folders with retry logic")

	// Try to refresh folders
	err := fsd.refreshFoldersInternal()
	if err != nil {
		// Check if it's a connection error
		if strings.Contains(err.Error(), "closed network connection") || strings.Contains(err.Error(), "not connected") {
			fsd.logger.Warn("Connection error during folder refresh, forcing fresh reconnection")

			// Try to force reconnect
			if reconnectErr := fsd.imapClient.ForceReconnect(); reconnectErr != nil {
				fsd.logger.Error("Failed to force reconnect during folder refresh: %v", reconnectErr)
				return fmt.Errorf("failed to force reconnect: %w", reconnectErr)
			}

			fsd.logger.Info("Successfully force reconnected, retrying folder refresh")

			// Wait for connection to stabilize
			time.Sleep(500 * time.Millisecond)

			// Retry the refresh
			err = fsd.refreshFoldersInternal()
			if err != nil {
				fsd.logger.Error("Folder refresh still failed after reconnection: %v", err)
				return fmt.Errorf("folder refresh failed after reconnection: %w", err)
			}
		} else {
			return err
		}
	}

	fsd.logger.Debug("Folder refresh completed successfully")
	return nil
}

// refreshFoldersInternal performs the actual folder refresh operation
func (fsd *FolderSubscriptionDialog) refreshFoldersInternal() error {
	fsd.logger.Debug("Performing internal folder refresh")

	// Get fresh folder list from server
	allFolders, err := fsd.imapClient.ListAllFolders()
	if err != nil {
		fsd.logger.Error("Failed to list all folders: %v", err)
		return fmt.Errorf("failed to list folders: %w", err)
	}

	subscribedFolders, err := fsd.imapClient.ForceRefreshSubscribedFolders()
	if err != nil {
		fsd.logger.Error("Failed to list subscribed folders: %v", err)
		return fmt.Errorf("failed to list subscribed folders: %w", err)
	}

	// Sort folders alphabetically
	sort.Slice(allFolders, func(i, j int) bool {
		return allFolders[i].Name < allFolders[j].Name
	})

	// Update data
	fsd.allFolders = allFolders
	fsd.subscribedFolders = subscribedFolders

	// Update UI with flag to prevent checkbox callback loops - must be done on UI thread
	fyne.Do(func() {
		fsd.updatingCheckboxes = true
		fsd.folderList.Refresh()
		fsd.updatingCheckboxes = false

		fsd.statusLabel.SetText(fmt.Sprintf("Loaded %d folders (%d subscribed)", len(allFolders), len(subscribedFolders)))
		fsd.updateButtonStates()
	})

	return nil
}

// refreshFolders loads all folders and subscription status
func (fsd *FolderSubscriptionDialog) refreshFolders() {
	fsd.logger.Debug("Refreshing folders for subscription dialog")

	// Update status - must be done on UI thread
	fyne.Do(func() {
		fsd.statusLabel.SetText("Loading folders...")
	})

	// Load all folders
	allFolders, err := fsd.imapClient.ListAllFolders()
	if err != nil {
		fsd.logger.Error("Failed to load all folders: %v", err)
		fyne.Do(func() {
			fsd.statusLabel.SetText(fmt.Sprintf("Error loading folders: %v", err))
		})
		return
	}

	// Load subscribed folders
	subscribedFolders, err := fsd.imapClient.ListSubscribedFolders()
	if err != nil {
		fsd.logger.Error("Failed to load subscribed folders: %v", err)
		fyne.Do(func() {
			fsd.statusLabel.SetText(fmt.Sprintf("Error loading subscriptions: %v", err))
		})
		return
	}

	// Sort folders alphabetically
	sort.Slice(allFolders, func(i, j int) bool {
		return allFolders[i].Name < allFolders[j].Name
	})

	// Update data
	fsd.allFolders = allFolders
	fsd.subscribedFolders = subscribedFolders

	// Update UI with flag to prevent checkbox callback loops - must be done on UI thread
	fyne.Do(func() {
		fsd.updatingCheckboxes = true
		fsd.folderList.Refresh()
		fsd.updatingCheckboxes = false

		fsd.statusLabel.SetText(fmt.Sprintf("Loaded %d folders (%d subscribed)", len(allFolders), len(subscribedFolders)))
		fsd.updateButtonStates()
	})
}

// isFolderSubscribed checks if a folder is currently subscribed
func (fsd *FolderSubscriptionDialog) isFolderSubscribed(folderName string) bool {
	for _, folder := range fsd.subscribedFolders {
		if folder.Name == folderName {
			return true
		}
	}
	return false
}

// subscribeToFolder subscribes to a folder
func (fsd *FolderSubscriptionDialog) subscribeToFolder(folderName string) {
	fsd.logger.Debug("Subscribing to folder: %s", folderName)

	err := fsd.imapClient.SubscribeFolder(folderName)
	if err != nil {
		fsd.logger.Error("Failed to subscribe to folder %s: %v", folderName, err)
		fyne.Do(func() {
			fsd.statusLabel.SetText(fmt.Sprintf("Failed to subscribe to %s: %v", folderName, err))
		})
		return
	}

	fsd.logger.Info("Successfully subscribed to folder: %s", folderName)
	fyne.Do(func() {
		fsd.statusLabel.SetText(fmt.Sprintf("Subscribed to %s", folderName))
	})

	// Refresh subscription status
	go fsd.refreshSubscriptionStatus()
}

// unsubscribeFromFolder unsubscribes from a folder
func (fsd *FolderSubscriptionDialog) unsubscribeFromFolder(folderName string) {
	fsd.logger.Debug("Unsubscribing from folder: %s", folderName)

	err := fsd.imapClient.UnsubscribeFolder(folderName)
	if err != nil {
		fsd.logger.Error("Failed to unsubscribe from folder %s: %v", folderName, err)
		fyne.Do(func() {
			fsd.statusLabel.SetText(fmt.Sprintf("Failed to unsubscribe from %s: %v", folderName, err))
		})
		return
	}

	fsd.logger.Info("Successfully unsubscribed from folder: %s", folderName)
	fyne.Do(func() {
		fsd.statusLabel.SetText(fmt.Sprintf("Unsubscribed from %s", folderName))
	})

	// Refresh subscription status
	go fsd.refreshSubscriptionStatus()
}

// refreshSubscriptionStatus refreshes only the subscription status without reloading all folders
func (fsd *FolderSubscriptionDialog) refreshSubscriptionStatus() {
	subscribedFolders, err := fsd.imapClient.ListSubscribedFolders()
	if err != nil {
		fsd.logger.Error("Failed to refresh subscription status: %v", err)
		return
	}

	fsd.subscribedFolders = subscribedFolders

	// Set flag to prevent checkbox callback loops during refresh - must be done on UI thread
	fyne.Do(func() {
		fsd.updatingCheckboxes = true
		fsd.folderList.Refresh()
		fsd.updatingCheckboxes = false

		fsd.updateButtonStates()
	})
}

// updateButtonStates updates the enabled/disabled state of action buttons
func (fsd *FolderSubscriptionDialog) updateButtonStates() {
	hasSelection := fsd.selectedFolder != nil
	fsd.subscribeBtn.Enable()
	fsd.unsubscribeBtn.Enable()
	fsd.deleteBtn.Enable()

	if hasSelection {
		isSubscribed := fsd.isFolderSubscribed(fsd.selectedFolder.Name)
		if isSubscribed {
			fsd.subscribeBtn.Disable()
		} else {
			fsd.unsubscribeBtn.Disable()
		}

		// Disable delete button for special folders
		folderName := fsd.selectedFolder.Name
		if fsd.isSpecialFolder(folderName) {
			fsd.deleteBtn.Disable()
		}
	} else {
		fsd.subscribeBtn.Disable()
		fsd.unsubscribeBtn.Disable()
		fsd.deleteBtn.Disable()
	}
}

// showCreateFolderDialog displays a dialog for creating a new folder
func (fsd *FolderSubscriptionDialog) showCreateFolderDialog() {
	fsd.logger.Debug("Showing create folder dialog")

	// Create entry widget for folder name
	folderNameEntry := widget.NewEntry()
	folderNameEntry.SetPlaceHolder("Enter folder name...")

	// Create form content
	content := container.NewVBox(
		widget.NewLabel("Create New Folder"),
		widget.NewSeparator(),
		widget.NewLabel("Folder Name:"),
		folderNameEntry,
		widget.NewLabel("Note: The new folder will be automatically subscribed."),
	)

	// Create dialog
	createDialog := dialog.NewCustomConfirm(
		"Create Folder",
		"Create",
		"Cancel",
		content,
		func(confirmed bool) {
			if confirmed {
				folderName := strings.TrimSpace(folderNameEntry.Text)
				if folderName == "" {
					dialog.ShowError(fmt.Errorf("folder name cannot be empty"), fsd.window)
					return
				}
				fsd.createFolder(folderName)
			}
		},
		fsd.window,
	)

	// Set dialog size
	createDialog.Resize(fyne.NewSize(400, 250))
	createDialog.Show()

	// Focus on the entry field
	fsd.window.Canvas().Focus(folderNameEntry)
}

// createFolder creates a new folder using the IMAP client
func (fsd *FolderSubscriptionDialog) createFolder(folderName string) {
	fsd.logger.Debug("Creating folder: %s", folderName)

	// Update status - must be done on UI thread
	fyne.Do(func() {
		fsd.statusLabel.SetText(fmt.Sprintf("Creating folder: %s...", folderName))
	})

	// Create the folder (this will also automatically subscribe to it)
	err := fsd.imapClient.CreateFolder(folderName)
	if err != nil {
		fsd.logger.Error("Failed to create folder %s: %v", folderName, err)
		fyne.Do(func() {
			fsd.statusLabel.SetText(fmt.Sprintf("Failed to create folder: %v", err))
			dialog.ShowError(fmt.Errorf("failed to create folder '%s': %v", folderName, err), fsd.window)
		})
		return
	}

	fsd.logger.Info("Successfully created and subscribed to folder: %s", folderName)
	fyne.Do(func() {
		fsd.statusLabel.SetText(fmt.Sprintf("Created and subscribed to folder: %s", folderName))
	})

	// Refresh the folder list to show the new folder, and then refresh main window
	go func() {
		fsd.refreshFolders()
		// After dialog refresh completes, reload the main window folder list
		// Use the specific callback for folder creation to avoid cache invalidation
		if fsd.onFolderAdded != nil {
			fsd.onFolderAdded()
		}
	}()
}

// isSpecialFolder checks if a folder is a special folder that shouldn't be deleted
func (fsd *FolderSubscriptionDialog) isSpecialFolder(folderName string) bool {
	// Convert to lowercase for case-insensitive comparison
	lowerName := strings.ToLower(folderName)

	// Common special folder names that should not be deleted
	specialFolders := []string{
		"inbox",
		"sent",
		"sent items",
		"sent mail",
		"[gmail]/sent mail",
		"drafts",
		"draft",
		"trash",
		"deleted items",
		"[gmail]/trash",
		"spam",
		"junk",
		"[gmail]/spam",
		"all mail",
		"[gmail]/all mail",
		"important",
		"[gmail]/important",
		"starred",
		"[gmail]/starred",
	}

	for _, special := range specialFolders {
		if lowerName == special {
			return true
		}
	}

	return false
}

// showDeleteFolderDialog displays a dialog for deleting a folder with message handling options
func (fsd *FolderSubscriptionDialog) showDeleteFolderDialog(folderName string) {
	fsd.logger.Debug("Showing delete folder dialog for: %s", folderName)

	// Comprehensive validation before showing delete dialog
	if err := fsd.validateFolderDeletion(folderName); err != nil {
		dialog.ShowError(err, fsd.window)
		return
	}

	// First, check if the folder has any messages
	go func() {
		messages, err := fsd.imapClient.FetchMessages(folderName, 1) // Just check if there are any messages
		if err != nil {
			fsd.logger.Error("Failed to check messages in folder %s: %v", folderName, err)
			// If we can't check messages, show simple confirmation with warning
			fsd.showSimpleDeleteConfirmationWithWarning(folderName, "Unable to check folder contents. The folder may contain messages.")
			return
		}

		if len(messages) == 0 {
			// No messages, show simple confirmation
			fsd.showSimpleDeleteConfirmation(folderName)
		} else {
			// Has messages, show message handling options
			fsd.showMessageHandlingDialog(folderName)
		}
	}()
}

// validateFolderDeletion performs comprehensive validation before allowing folder deletion
func (fsd *FolderSubscriptionDialog) validateFolderDeletion(folderName string) error {
	// Check if folder name is empty
	if strings.TrimSpace(folderName) == "" {
		return fmt.Errorf("folder name cannot be empty")
	}

	// Check if it's a special folder
	if fsd.isSpecialFolder(folderName) {
		return fmt.Errorf("cannot delete special folder '%s'. Special folders like INBOX, Sent, Trash, and Drafts are protected from deletion", folderName)
	}

	// Check if folder exists in the current folder list
	folderExists := false
	for _, folder := range fsd.allFolders {
		if folder.Name == folderName {
			folderExists = true
			break
		}
	}

	if !folderExists {
		return fmt.Errorf("folder '%s' does not exist or is not accessible", folderName)
	}

	// Check if we're connected to the IMAP server
	// Note: We can't directly check connection status from the interface,
	// but we can try a simple operation to verify connectivity

	return nil
}

// validateTargetFolder validates that the target folder is suitable for moving messages
func (fsd *FolderSubscriptionDialog) validateTargetFolder(targetFolder, sourceFolder string) error {
	// Check if target folder is the same as source folder
	if targetFolder == sourceFolder {
		return fmt.Errorf("target folder cannot be the same as the folder being deleted")
	}

	// Check if target folder exists in subscribed folders
	targetExists := false
	for _, folder := range fsd.subscribedFolders {
		if folder.Name == targetFolder {
			targetExists = true
			break
		}
	}

	if !targetExists {
		return fmt.Errorf("target folder '%s' is not subscribed or does not exist", targetFolder)
	}

	// Check if target folder is a special folder that might not accept moved messages
	// (This is more of a warning, but we'll allow it)
	if fsd.isSpecialFolder(targetFolder) {
		fsd.logger.Warn("Moving messages to special folder '%s' - this may not be supported by all email providers", targetFolder)
	}

	return nil
}

// showSimpleDeleteConfirmationWithWarning shows a confirmation dialog with a warning message
func (fsd *FolderSubscriptionDialog) showSimpleDeleteConfirmationWithWarning(folderName, warning string) {
	content := container.NewVBox(
		widget.NewLabel(fmt.Sprintf("Delete folder '%s'?", folderName)),
		widget.NewSeparator(),
		widget.NewLabel("⚠️ Warning:"),
		widget.NewLabel(warning),
		widget.NewSeparator(),
		widget.NewLabel("This action cannot be undone."),
	)

	confirmDialog := dialog.NewCustomConfirm(
		"Delete Folder",
		"Delete",
		"Cancel",
		content,
		func(confirmed bool) {
			if confirmed {
				fsd.deleteFolder(folderName, "", false)
			}
		},
		fsd.window,
	)

	confirmDialog.Resize(fyne.NewSize(400, 250))
	confirmDialog.Show()
}

// showSimpleDeleteConfirmation shows a simple confirmation dialog for empty folders
func (fsd *FolderSubscriptionDialog) showSimpleDeleteConfirmation(folderName string) {
	confirmDialog := dialog.NewConfirm(
		"Delete Folder",
		fmt.Sprintf("Are you sure you want to delete the folder '%s'?\n\nThis action cannot be undone.", folderName),
		func(confirmed bool) {
			if confirmed {
				fsd.deleteFolder(folderName, "", false)
			}
		},
		fsd.window,
	)

	confirmDialog.Show()
}

// showMessageHandlingDialog shows a dialog with options for handling messages in the folder
func (fsd *FolderSubscriptionDialog) showMessageHandlingDialog(folderName string) {
	// Create radio buttons for message handling options
	deleteMessagesRadio := widget.NewRadioGroup([]string{
		"Delete all messages in the folder",
		"Move messages to another folder",
	}, nil)
	deleteMessagesRadio.SetSelected("Delete all messages in the folder")

	// Create dropdown for target folder selection (initially disabled)
	targetFolderSelect := widget.NewSelect([]string{}, nil)
	targetFolderSelect.Disable()

	// Populate target folder dropdown with subscribed folders (excluding the folder being deleted)
	var targetFolders []string
	for _, folder := range fsd.subscribedFolders {
		if folder.Name != folderName && !fsd.isSpecialFolder(folder.Name) {
			targetFolders = append(targetFolders, folder.Name)
		}
	}

	// Add special folders that are commonly used for moving messages
	specialTargets := []string{"INBOX", "Trash", "Archive"}
	for _, special := range specialTargets {
		found := false
		for _, existing := range targetFolders {
			if strings.EqualFold(existing, special) {
				found = true
				break
			}
		}
		if !found {
			// Check if this special folder exists in subscribed folders
			for _, folder := range fsd.subscribedFolders {
				if strings.EqualFold(folder.Name, special) {
					targetFolders = append(targetFolders, folder.Name)
					break
				}
			}
		}
	}

	targetFolderSelect.Options = targetFolders
	if len(targetFolders) > 0 {
		targetFolderSelect.SetSelected(targetFolders[0])
	}

	// Handle radio button changes
	deleteMessagesRadio.OnChanged = func(selected string) {
		if selected == "Move messages to another folder" {
			targetFolderSelect.Enable()
		} else {
			targetFolderSelect.Disable()
		}
	}

	// Create the dialog content
	content := container.NewVBox(
		widget.NewLabel(fmt.Sprintf("The folder '%s' contains messages.", folderName)),
		widget.NewLabel("What would you like to do with the messages?"),
		widget.NewSeparator(),
		deleteMessagesRadio,
		container.NewBorder(nil, nil, widget.NewLabel("Target folder:"), nil, targetFolderSelect),
		widget.NewSeparator(),
		widget.NewLabel("Warning: This action cannot be undone."),
	)

	// Create the confirmation dialog
	confirmDialog := dialog.NewCustomConfirm(
		"Delete Folder",
		"Delete",
		"Cancel",
		content,
		func(confirmed bool) {
			if confirmed {
				deleteMessages := deleteMessagesRadio.Selected == "Delete all messages in the folder"
				targetFolder := ""
				if !deleteMessages {
					targetFolder = targetFolderSelect.Selected
					if targetFolder == "" {
						dialog.ShowError(fmt.Errorf("please select a target folder for the messages"), fsd.window)
						return
					}

					// Validate target folder
					if err := fsd.validateTargetFolder(targetFolder, folderName); err != nil {
						dialog.ShowError(err, fsd.window)
						return
					}
				}
				fsd.deleteFolder(folderName, targetFolder, !deleteMessages)
			}
		},
		fsd.window,
	)

	// Set dialog size
	confirmDialog.Resize(fyne.NewSize(500, 350))
	confirmDialog.Show()
}

// deleteFolder performs the actual folder deletion with message handling
func (fsd *FolderSubscriptionDialog) deleteFolder(folderName, targetFolder string, moveMessages bool) {
	fsd.logger.Debug("Deleting folder: %s (moveMessages: %t, targetFolder: %s)", folderName, moveMessages, targetFolder)

	// Create a progress dialog for long-running operations
	progressLabel := widget.NewLabel("Preparing to delete folder...")
	progressContent := container.NewVBox(
		widget.NewLabel(fmt.Sprintf("Deleting folder: %s", folderName)),
		widget.NewSeparator(),
		progressLabel,
	)

	progressDialog := dialog.NewCustom(
		"Deleting Folder",
		"",
		progressContent,
		fsd.window,
	)
	progressDialog.Resize(fyne.NewSize(400, 150))
	progressDialog.Show()

	// Perform the deletion in a goroutine to avoid blocking the UI
	go func() {
		defer progressDialog.Hide()

		// Update status - must be done on UI thread
		fyne.Do(func() {
			if moveMessages {
				fsd.statusLabel.SetText(fmt.Sprintf("Moving messages and deleting folder: %s...", folderName))
				progressLabel.SetText("Moving messages to target folder...")
			} else {
				fsd.statusLabel.SetText(fmt.Sprintf("Deleting folder: %s...", folderName))
				progressLabel.SetText("Deleting folder...")
			}
		})

		// Handle messages if needed
		if moveMessages && targetFolder != "" {
			err := fsd.moveAllMessages(folderName, targetFolder)
			if err != nil {
				fsd.logger.Error("Failed to move messages from folder %s to %s: %v", folderName, targetFolder, err)
				fyne.Do(func() {
					fsd.statusLabel.SetText(fmt.Sprintf("Failed to move messages: %v", err))
				})

				// Show error with option to continue with folder deletion
				errorMsg := fmt.Sprintf("Failed to move messages from '%s' to '%s': %v\n\nDo you want to delete the folder anyway? This will permanently delete all messages in the folder.", folderName, targetFolder, err)
				confirmDialog := dialog.NewConfirm(
					"Message Move Failed",
					errorMsg,
					func(confirmed bool) {
						if confirmed {
							// Continue with folder deletion (this will delete messages)
							fsd.deleteFolderOnly(folderName)
						}
					},
					fsd.window,
				)
				confirmDialog.Show()
				return
			}
			fsd.logger.Info("Successfully moved all messages from %s to %s", folderName, targetFolder)
		}

		// Update progress - must be done on UI thread
		fyne.Do(func() {
			progressLabel.SetText("Deleting folder from server...")
		})

		// Delete the folder
		err := fsd.imapClient.DeleteFolder(folderName)
		if err != nil {
			fsd.logger.Error("Failed to delete folder %s: %v", folderName, err)
			fyne.Do(func() {
				fsd.statusLabel.SetText(fmt.Sprintf("Failed to delete folder: %v", err))
				dialog.ShowError(fmt.Errorf("failed to delete folder '%s': %v", folderName, err), fsd.window)
			})
			return
		}

		fsd.logger.Info("Successfully deleted folder: %s", folderName)

		// Update progress - must be done on UI thread
		fyne.Do(func() {
			progressLabel.SetText("Refreshing folder list...")
		})

		// Wait a moment for server to process the deletion
		time.Sleep(500 * time.Millisecond)

		// Refresh the folder list to remove the deleted folder with retry logic
		go func() {
			maxAttempts := 3
			for attempt := 1; attempt <= maxAttempts; attempt++ {
				fsd.logger.Debug("Folder list refresh attempt %d/%d after deletion", attempt, maxAttempts)

				// Try to refresh the folder list
				err := fsd.refreshFoldersWithRetry()
				if err == nil {
					fsd.logger.Info("Successfully refreshed folder list after deletion on attempt %d", attempt)
					break
				}

				fsd.logger.Warn("Folder list refresh attempt %d failed: %v", attempt, err)
				if attempt < maxAttempts {
					time.Sleep(time.Duration(attempt) * 1000 * time.Millisecond)
				}
			}
		}()

		// Update final status - must be done on UI thread
		fyne.Do(func() {
			if moveMessages {
				fsd.statusLabel.SetText(fmt.Sprintf("Moved messages and deleted folder: %s", folderName))
			} else {
				fsd.statusLabel.SetText(fmt.Sprintf("Deleted folder: %s", folderName))
			}
		})

		// Notify main window about the deleted folder to handle current selection
		if fsd.onFolderDeleted != nil {
			fsd.onFolderDeleted(folderName)
		}

		// After dialog refresh completes, reload the main window folder list
		if fsd.onClose != nil {
			fsd.onClose()
		}
	}()
}

// moveAllMessages moves all messages from one folder to another
func (fsd *FolderSubscriptionDialog) moveAllMessages(sourceFolder, targetFolder string) error {
	fsd.logger.Debug("Moving all messages from %s to %s", sourceFolder, targetFolder)

	// Fetch all messages from the source folder
	messages, err := fsd.imapClient.FetchMessages(sourceFolder, 0) // 0 means fetch all
	if err != nil {
		return fmt.Errorf("failed to fetch messages from %s: %w", sourceFolder, err)
	}

	if len(messages) == 0 {
		fsd.logger.Debug("No messages to move from %s", sourceFolder)
		return nil
	}

	fsd.logger.Info("Moving %d messages from %s to %s", len(messages), sourceFolder, targetFolder)

	// Move each message with progress logging
	var moveErrors []error
	movedCount := 0
	totalMessages := len(messages)

	for i, message := range messages {
		// Log progress for every 10 messages or for small batches
		if totalMessages <= 10 || (i+1)%10 == 0 || i == totalMessages-1 {
			fsd.logger.Debug("Moving message %d of %d (UID: %d)", i+1, totalMessages, message.UID)
		}

		err := fsd.imapClient.MoveMessage(sourceFolder, message.UID, targetFolder)
		if err != nil {
			fsd.logger.Error("Failed to move message UID %d from %s to %s: %v", message.UID, sourceFolder, targetFolder, err)
			moveErrors = append(moveErrors, fmt.Errorf("message UID %d: %w", message.UID, err))
		} else {
			movedCount++
		}
	}

	fsd.logger.Info("Successfully moved %d out of %d messages from %s to %s", movedCount, len(messages), sourceFolder, targetFolder)

	// If there were any errors, return them
	if len(moveErrors) > 0 {
		if movedCount == 0 {
			return fmt.Errorf("failed to move any messages: %v", moveErrors[0])
		} else {
			// Some messages moved successfully, but some failed
			return fmt.Errorf("moved %d messages successfully, but %d failed (first error: %v)", movedCount, len(moveErrors), moveErrors[0])
		}
	}

	return nil
}

// deleteFolderOnly deletes the folder without handling messages (used for error recovery)
func (fsd *FolderSubscriptionDialog) deleteFolderOnly(folderName string) {
	fsd.logger.Debug("Deleting folder only (no message handling): %s", folderName)

	// Update status - must be done on UI thread
	fyne.Do(func() {
		fsd.statusLabel.SetText(fmt.Sprintf("Deleting folder: %s...", folderName))
	})

	// Delete the folder
	err := fsd.imapClient.DeleteFolder(folderName)
	if err != nil {
		fsd.logger.Error("Failed to delete folder %s: %v", folderName, err)
		fyne.Do(func() {
			fsd.statusLabel.SetText(fmt.Sprintf("Failed to delete folder: %v", err))
			dialog.ShowError(fmt.Errorf("failed to delete folder '%s': %v", folderName, err), fsd.window)
		})
		return
	}

	fsd.logger.Info("Successfully deleted folder: %s", folderName)
	fyne.Do(func() {
		fsd.statusLabel.SetText(fmt.Sprintf("Deleted folder: %s", folderName))
	})

	// Refresh the folder list to remove the deleted folder with retry logic
	go func() {
		// Wait a moment for server to process the deletion
		time.Sleep(500 * time.Millisecond)

		maxAttempts := 3
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			fsd.logger.Debug("Folder list refresh attempt %d/%d after folder-only deletion", attempt, maxAttempts)

			err := fsd.refreshFoldersWithRetry()
			if err == nil {
				fsd.logger.Info("Successfully refreshed folder list after folder-only deletion on attempt %d", attempt)
				break
			}

			fsd.logger.Warn("Folder list refresh attempt %d failed: %v", attempt, err)
			if attempt < maxAttempts {
				time.Sleep(time.Duration(attempt) * 1000 * time.Millisecond)
			}
		}

		// After dialog refresh completes, force a complete refresh of the main window
		// This ensures the deleted folder is removed from the main window's folder list
		if fsd.onClose != nil {
			fsd.onClose()
		}
	}()
}
