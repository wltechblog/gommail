package ui

// Code in this file was mechanically split from main_window.go.

import (
	"fmt"
	_ "image/gif"  // Register GIF format
	_ "image/jpeg" // Register JPEG format
	_ "image/png"  // Register PNG format
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/wltechblog/gommail/internal/config"
	"github.com/wltechblog/gommail/internal/email"
	"github.com/wltechblog/gommail/pkg/imap"
	"github.com/wltechblog/gommail/pkg/smtp"
)

// moveToTrashMultiple moves all selected messages to trash
func (mw *MainWindow) moveToTrashMultiple() {
	selectedMessages := mw.getSelectedMessages()
	if len(selectedMessages) == 0 {
		dialog.ShowInformation("No Messages", "Please select messages to move to trash", mw.window)
		return
	}

	// Show confirmation dialog
	dialog.ShowConfirm("Move Messages to Trash",
		fmt.Sprintf("Are you sure you want to move %d messages to trash?", len(selectedMessages)),
		func(confirmed bool) {
			if confirmed {
				mw.performMoveToTrashMultiple(selectedMessages)
			}
		}, mw.window)
}

// performMoveToTrashMultiple performs the actual move to trash operation for multiple messages
func (mw *MainWindow) performMoveToTrashMultiple(messages []*email.MessageIndexItem) {
	mw.runBatchMessageOp(messages, mw.performSingleMoveToTrash,
		func(n int) string { return fmt.Sprintf("Moving %d messages to trash...", n) },
		func(ok, fail int) string {
			if fail == 0 {
				return fmt.Sprintf("Moved %d messages to trash", ok)
			}
			return fmt.Sprintf("Moved %d messages to trash, %d failed", ok, fail)
		})
}

// runBatchMessageOp runs a blocking per-message IMAP operation off the UI
// thread, then reports the tally, clears the selection and refreshes the
// message list on the UI thread.
func (mw *MainWindow) runBatchMessageOp(messages []*email.MessageIndexItem,
	op func(*email.MessageIndexItem) error,
	progressMsg func(n int) string,
	doneMsg func(ok, fail int) string) {

	mw.statusBar.SetText(progressMsg(len(messages)))

	go func() {
		successCount := 0
		errorCount := 0

		for _, msg := range messages {
			if err := op(msg); err != nil {
				mw.logger.Error("Batch message operation failed: %v", err)
				errorCount++
			} else {
				successCount++
			}
		}

		fyne.Do(func() {
			mw.statusBar.SetText(doneMsg(successCount, errorCount))
			mw.clearSelection()
			mw.refreshMessageList()
		})
	}()
}

// performSingleMoveToTrash moves a single message to trash (extracted from existing logic)
func (mw *MainWindow) performSingleMoveToTrash(msg *email.MessageIndexItem) error {
	// Get the correct account for the message
	var currentAccount *config.Account

	if mw.accountController.IsUnifiedInbox() {
		// Find the account for this message
		currentAccount = mw.getAccountByName(msg.AccountName)
		if currentAccount == nil {
			return fmt.Errorf("account not found: %s", msg.AccountName)
		}
	} else {
		// Regular mode
		if mw.accountController.GetCurrentAccount() == nil {
			return fmt.Errorf("no account available")
		}
		currentAccount = mw.accountController.GetCurrentAccount()
	}

	// Determine trash folder (use configured or default)
	trashFolder := currentAccount.TrashFolder
	if trashFolder == "" {
		trashFolder = "Trash" // Default fallback
	}

	// Use the MessageIndexItem's MoveTo method to move to trash
	return msg.MoveTo(trashFolder)
}

// showMoveToFolderDialogMultiple shows folder selection dialog for multiple messages
func (mw *MainWindow) showMoveToFolderDialogMultiple() {
	selectedMessages := mw.getSelectedMessages()
	if len(selectedMessages) == 0 {
		dialog.ShowInformation("No Messages", "Please select messages to move", mw.window)
		return
	}

	// For unified inbox, we need to handle messages from different accounts
	// For now, we'll only allow moving messages from the same account
	if mw.accountController.IsUnifiedInbox() {
		// Check if all selected messages are from the same account
		accountName := selectedMessages[0].AccountName
		for _, msg := range selectedMessages[1:] {
			if msg.AccountName != accountName {
				dialog.ShowInformation("Mixed Accounts",
					"Cannot move messages from different accounts at once. Please select messages from a single account.",
					mw.window)
				return
			}
		}
	}

	// Get folders for the account
	var folders []email.Folder
	if mw.accountController.IsUnifiedInbox() {
		// Get folders for the specific account
		accountName := selectedMessages[0].AccountName
		client, exists := mw.accountController.GetIMAPClientForAccount(accountName)
		if exists && client != nil {
			var err error
			folders, err = client.ListFolders()
			if err != nil {
				dialog.ShowError(fmt.Errorf("failed to get folders: %v", err), mw.window)
				return
			}
		}
	} else {
		folders = mw.folderController.GetFolders()
	}

	if len(folders) == 0 {
		dialog.ShowInformation("No Folders", "No folders available", mw.window)
		return
	}

	// Create folder selection dialog (widget construction must stay on the
	// UI thread; the folder list was already fetched above).
	folderNames := make([]string, len(folders))
	for i, folder := range folders {
		folderNames[i] = folder.Name
	}

	folderSelect := widget.NewSelect(folderNames, nil)
	folderSelect.PlaceHolder = "Select a folder..."

	content := container.NewVBox(
		widget.NewLabel(fmt.Sprintf("Move %d messages to folder:", len(selectedMessages))),
		folderSelect,
	)

	dialog.ShowCustomConfirm("Move Messages", "Move", "Cancel", content, func(confirmed bool) {
		if confirmed && folderSelect.Selected != "" {
			mw.logger.Debug("Moving %d messages to folder: %s", len(selectedMessages), folderSelect.Selected)
			mw.moveToFolderMultiple(selectedMessages, folderSelect.Selected)
		}
	}, mw.window)
}

// moveToFolderMultiple moves multiple messages to the specified folder
func (mw *MainWindow) moveToFolderMultiple(messages []*email.MessageIndexItem, folderName string) {
	mw.runBatchMessageOp(messages,
		func(msg *email.MessageIndexItem) error { return mw.performSingleMoveToFolder(msg, folderName) },
		func(n int) string { return fmt.Sprintf("Moving %d messages to %s...", n, folderName) },
		func(ok, fail int) string {
			if fail == 0 {
				return fmt.Sprintf("Moved %d messages to %s", ok, folderName)
			}
			return fmt.Sprintf("Moved %d messages to %s, %d failed", ok, folderName, fail)
		})
}

// performSingleMoveToFolder moves a single message to the specified folder
func (mw *MainWindow) performSingleMoveToFolder(msg *email.MessageIndexItem, folderName string) error {
	// Use the MessageIndexItem's MoveTo method
	return msg.MoveTo(folderName)
}

// deleteMessagesMultiple permanently deletes all selected messages
func (mw *MainWindow) deleteMessagesMultiple() {
	selectedMessages := mw.getSelectedMessages()
	if len(selectedMessages) == 0 {
		dialog.ShowInformation("No Messages", "Please select messages to delete", mw.window)
		return
	}

	// Show confirmation dialog
	dialog.ShowConfirm("Delete Messages",
		fmt.Sprintf("Are you sure you want to permanently delete %d messages?\n\nThis action cannot be undone.", len(selectedMessages)),
		func(confirmed bool) {
			if confirmed {
				mw.performDeleteMultiple(selectedMessages)
			}
		}, mw.window)
}

// performDeleteMultiple performs the actual delete operation for multiple messages
func (mw *MainWindow) performDeleteMultiple(messages []*email.MessageIndexItem) {
	mw.runBatchMessageOp(messages,
		func(msg *email.MessageIndexItem) error { return msg.Delete() },
		func(n int) string { return fmt.Sprintf("Deleting %d messages...", n) },
		func(ok, fail int) string {
			if fail == 0 {
				return fmt.Sprintf("Deleted %d messages", ok)
			}
			return fmt.Sprintf("Deleted %d messages, %d failed", ok, fail)
		})
}

// moveToFolder moves the selected message to the specified folder
func (mw *MainWindow) moveToFolder(targetFolder string) {
	if mw.selectedMessage == nil {
		dialog.ShowInformation("No Message", "Please select a message to move", mw.window)
		return
	}

	if mw.accountController.GetCurrentAccount() == nil || mw.imapClient == nil {
		dialog.ShowInformation("No Connection", "Please connect to an account first", mw.window)
		return
	}

	currentFolder := mw.folderController.GetCurrentFolder()
	if targetFolder == currentFolder {
		dialog.ShowInformation("Same Folder", "Message is already in this folder", mw.window)
		return
	}

	// Show confirmation dialog
	dialog.ShowConfirm("Move Message",
		fmt.Sprintf("Move message '%s' to folder '%s'?", mw.selectedMessage.Message.Subject, targetFolder),
		func(confirmed bool) {
			if confirmed {
				mw.performMoveToFolder(targetFolder)
			}
		}, mw.window)
}

// performMoveToFolder performs the actual move operation
func (mw *MainWindow) performMoveToFolder(targetFolder string) {
	mw.statusBar.SetText(fmt.Sprintf("Moving message to folder '%s'...", targetFolder))

	// Capture selectedMessage before spawning goroutine to avoid data race
	mw.messagesMu.RLock()
	msg := mw.selectedMessage
	mw.messagesMu.RUnlock()
	if msg == nil {
		return
	}

	go func() {
		// Use the MessageIndexItem's MoveTo method - much simpler!
		err := msg.MoveTo(targetFolder)
		if err != nil {
			fyne.Do(func() {
				mw.statusBar.SetText(fmt.Sprintf("Failed to move message: %v", err))
				dialog.ShowError(fmt.Errorf("Failed to move message: %v", err), mw.window)
			})
			mw.logger.Error("Failed to move message %d to folder %s: %v", msg.Message.UID, targetFolder, err)
			return
		}

		mw.logger.Info("Successfully moved message %d to folder %s", msg.Message.UID, targetFolder)

		// Clear the selected message since it's no longer in the current folder
		fyne.Do(func() {
			mw.selectedMessage = nil
			if mw.messageList != nil {
				mw.refreshMessageList() // Update highlighting
			}
		})

		// Use RefreshCoordinator to refresh both source and target folders
		sourceFolder := mw.folderController.GetCurrentFolder()
		mw.refreshMessagesDebounced(sourceFolder, func(err error) {
			if err != nil {
				mw.logger.Error("Failed to refresh source folder after move: %v", err)
				fyne.Do(func() {
					mw.statusBar.SetText(fmt.Sprintf("Message moved to '%s', but refresh failed: %v", targetFolder, err))
				})
			} else {
				// Also refresh target folder if it's different
				if targetFolder != sourceFolder {
					mw.refreshMessagesDebounced(targetFolder, func(err error) {
						if err != nil {
							mw.logger.Error("Failed to refresh target folder after move: %v", err)
						}
					})
				}
				fyne.Do(func() {
					mw.statusBar.SetText(fmt.Sprintf("Message moved to '%s'", targetFolder))
				})
			}
		})
	}()
}

func (mw *MainWindow) deleteMessage() {
	// Check if multiple messages are selected
	selectedMessages := mw.getSelectedMessages()
	if len(selectedMessages) > 1 {
		mw.deleteMessagesMultiple()
		return
	}

	if mw.selectedMessage == nil {
		dialog.ShowInformation("No Message", "Please select a message to delete", mw.window)
		return
	}

	// Show confirmation dialog with account info (works for both unified and regular inbox)
	if mw.accountController.IsUnifiedInbox() {
		dialog.ShowConfirm("Delete Message",
			fmt.Sprintf("Are you sure you want to permanently delete the message from %s:\n\n%s",
				mw.selectedMessage.AccountName, mw.selectedMessage.Message.Subject),
			func(confirmed bool) {
				if confirmed {
					mw.performDelete()
				}
			}, mw.window)
	} else {
		// Regular mode
		if mw.accountController.GetCurrentAccount() == nil || mw.imapClient == nil {
			dialog.ShowInformation("No Connection", "Please connect to an account first", mw.window)
			return
		}

		// Show confirmation dialog
		dialog.ShowConfirm("Delete Message",
			fmt.Sprintf("Are you sure you want to permanently delete the message:\n\n%s", mw.selectedMessage.Message.Subject),
			func(confirmed bool) {
				if confirmed {
					mw.performDelete()
				}
			}, mw.window)
	}
}

func (mw *MainWindow) moveToTrash() {
	// Check if multiple messages are selected
	selectedMessages := mw.getSelectedMessages()
	if len(selectedMessages) > 1 {
		mw.moveToTrashMultiple()
		return
	}

	if mw.selectedMessage == nil {
		dialog.ShowInformation("No Message", "Please select a message to move to trash", mw.window)
		return
	}

	// Get the correct IMAP client and account for the selected message
	var imapClient *imap.ClientWrapper
	var currentAccount *config.Account

	if mw.accountController.IsUnifiedInbox() {
		// In unified inbox mode, get the account-specific IMAP client
		// Lock handled by AccountController
		accountClient, exists := mw.accountController.GetIMAPClientForAccount(mw.selectedMessage.AccountName)
		// Unlock handled by AccountController

		if !exists || accountClient == nil {
			dialog.ShowInformation("No Connection", fmt.Sprintf("No connection available for account %s", mw.selectedMessage.AccountName), mw.window)
			return
		}

		imapClient = accountClient

		// Get the account configuration
		currentAccount = mw.getAccountByName(mw.selectedMessage.AccountName)
		if currentAccount == nil {
			dialog.ShowInformation("Account Error", fmt.Sprintf("Account configuration not found for %s", mw.selectedMessage.AccountName), mw.window)
			return
		}
	} else {
		// Regular mode
		if mw.accountController.GetCurrentAccount() == nil || mw.imapClient == nil {
			dialog.ShowInformation("No Connection", "Please connect to an account first", mw.window)
			return
		}

		imapClient = mw.imapClient
		currentAccount = mw.accountController.GetCurrentAccount()
	}

	var trashFolder string

	// Get subscribed folders to ensure we only use folders the user has access to
	subscribedFolders, err := imapClient.ListSubscribedFolders()
	if err != nil {
		dialog.ShowError(fmt.Errorf("Failed to get subscribed folders: %v", err), mw.window)
		return
	}

	// Create a map of subscribed folder names for quick lookup
	subscribedMap := make(map[string]bool)
	for _, folder := range subscribedFolders {
		subscribedMap[folder.Name] = true
	}

	// First, check if a trash folder is configured for this account and is subscribed
	if currentAccount.TrashFolder != "" {
		if subscribedMap[currentAccount.TrashFolder] {
			trashFolder = currentAccount.TrashFolder
			mw.logger.Debug("Using configured trash folder: %s", trashFolder)
		} else {
			mw.logger.Warn("Configured trash folder %s is not subscribed", currentAccount.TrashFolder)
		}
	}

	// If no configured trash folder or it's not subscribed, fall back to auto-detection
	if trashFolder == "" {
		trashFolders := []string{"Trash", "INBOX.Trash", "Deleted Items", "Deleted Messages", "[Gmail]/Trash"}

		for _, folder := range subscribedFolders {
			for _, trashName := range trashFolders {
				if strings.EqualFold(folder.Name, trashName) {
					trashFolder = folder.Name
					break
				}
			}
			if trashFolder != "" {
				break
			}
		}

		if trashFolder == "" {
			// No trash folder found among subscribed folders
			dialog.ShowConfirm("No Trash Folder",
				"No trash folder found among your subscribed folders. You can:\n\n1. Configure a trash folder in your account settings\n2. Subscribe to a trash folder using the folder subscription dialog\n\nDelete message permanently?",
				func(confirmed bool) {
					if confirmed {
						mw.performDelete()
					}
				}, mw.window)
			return
		}

		mw.logger.Debug("Auto-detected trash folder among subscribed folders: %s", trashFolder)
	}

	// Capture the selected message to avoid race conditions in the goroutine
	mw.messagesMu.RLock()
	selectedMessage := mw.selectedMessage
	mw.messagesMu.RUnlock()
	if selectedMessage == nil {
		mw.statusBar.SetText("No message selected")
		return
	}

	// Move to trash folder
	mw.statusBar.SetText(fmt.Sprintf("Moving message to trash folder '%s'...", trashFolder))
	go func() {
		mw.logger.Debug("Starting move operation for message %d to trash folder %s", selectedMessage.Message.UID, trashFolder)

		// For unified inbox mode, set a flag to prevent IDLE monitoring from triggering
		// automatic refresh that would interfere with our optimistic update
		if mw.accountController.IsUnifiedInbox() {
			mw.deleteOperationMutex.Lock()
			if mw.unifiedInboxDeleteInProgress == nil {
				mw.unifiedInboxDeleteInProgress = make(map[string]bool)
			}
			mw.unifiedInboxDeleteInProgress[selectedMessage.AccountName] = true
			mw.deleteOperationMutex.Unlock()
			mw.logger.Debug("Set unified inbox delete operation flag for account %s", selectedMessage.AccountName)
		}

		// Use the MessageIndexItem's MoveTo method - much simpler!
		mw.logger.Debug("Calling MoveTo method for message %d", selectedMessage.Message.UID)
		err := selectedMessage.MoveTo(trashFolder)
		mw.logger.Debug("MoveTo method returned for message %d, error: %v", selectedMessage.Message.UID, err)

		// Clear the delete operation flag after the operation completes
		if mw.accountController.IsUnifiedInbox() {
			// Use a small delay to allow our optimistic update to complete first
			go func() {
				accountName := selectedMessage.AccountName // Capture the account name
				time.Sleep(200 * time.Millisecond)
				mw.deleteOperationMutex.Lock()
				if mw.unifiedInboxDeleteInProgress != nil {
					delete(mw.unifiedInboxDeleteInProgress, accountName)
				}
				mw.deleteOperationMutex.Unlock()
				mw.logger.Debug("Cleared unified inbox delete operation flag for account %s", accountName)
			}()
		}

		if err != nil {
			fyne.Do(func() {
				mw.statusBar.SetText(fmt.Sprintf("Failed to move to trash: %v", err))
			})
			mw.logger.Error("Failed to move message %d to trash folder %s: %v", selectedMessage.Message.UID, trashFolder, err)
			return
		}

		mw.logger.Info("Successfully moved message %d to trash folder %s", selectedMessage.Message.UID, trashFolder)

		// Handle refresh differently for unified inbox vs regular mode
		if mw.accountController.IsUnifiedInbox() {
			// For unified inbox, use optimistic UI update followed by proper refresh coordination
			fyne.Do(func() {
				// Find and remove the deleted message from the local array for immediate UI feedback
				deletedUID := selectedMessage.Message.UID
				deletedAccountName := selectedMessage.AccountName

				mw.logger.Debug("Attempting to remove message UID=%d from account %s (current array size: %d)", deletedUID, deletedAccountName, len(mw.messages))

				// Remove the message from the local array and track the index for auto-selection
				messageRemoved := false
				deletedIndex := -1
				for i, msg := range mw.messages {
					if msg.Message.UID == deletedUID && msg.AccountName == deletedAccountName {
						// Remove the message at index i
						mw.messages = append(mw.messages[:i], mw.messages[i+1:]...)
						mw.logger.Debug("Successfully removed message from local array at index %d (UID=%d, Account=%s, new size: %d)", i, deletedUID, deletedAccountName, len(mw.messages))
						messageRemoved = true
						deletedIndex = i
						break
					}
				}

				if !messageRemoved {
					mw.logger.Error("Failed to find message to remove from local array (UID=%d, Account=%s)", deletedUID, deletedAccountName)
					mw.statusBar.SetText("Message moved to trash (UI update failed)")
					return
				}

				// Validate message array consistency after removal
				if !mw.validateMessageArrayConsistency() {
					mw.logger.Error("Message array consistency check failed after removing deleted message")
				}

				// Clear current selection first to avoid stale references
				mw.selectedMessage = nil
				mw.clearMessageSelection()

				// Force the message list to refresh with new length
				mw.refreshMessageList()
				mw.logger.Debug("Forced message list refresh after removal (new length: %d)", len(mw.messages))

				// Auto-select next message if we have messages remaining
				if len(mw.messages) > 0 {
					// Try to select the message at the same index, or the last message if we were at the end
					nextIndex := deletedIndex
					if nextIndex >= len(mw.messages) {
						nextIndex = len(mw.messages) - 1
					}

					mw.logger.Debug("Auto-selecting next message at index %d after deletion", nextIndex)

					// Use a small delay to ensure the list widget has processed the refresh
					go func() {
						time.Sleep(50 * time.Millisecond)
						fyne.Do(func() {
							mw.selectMessage(nextIndex)
							mw.messageList.Select(nextIndex)
						})
					}()
				} else {
					// No messages left - clear the message display
					mw.messageViewController.ClearMessageView()
				}

				mw.statusBar.SetText("Message moved to trash")
			})

			// Skip the expensive full unified inbox refresh since the optimistic update is sufficient
			// The message has been successfully moved on the server, and we've updated the local UI
			// Real-time monitoring will catch any other changes from other clients
			mw.logger.Debug("Skipping full unified inbox refresh after trash move - using optimistic update only")
		} else {
			// Use RefreshCoordinator for regular folder refresh
			currentFolder := mw.folderController.GetCurrentFolder()
			mw.refreshMessagesDebounced(currentFolder, func(err error) {
				if err != nil {
					mw.logger.Error("Failed to refresh messages after moving to trash: %v", err)
					fyne.Do(func() {
						mw.statusBar.SetText(fmt.Sprintf("Message moved to trash, but refresh failed: %v", err))
					})
				} else {
					fyne.Do(func() {
						mw.statusBar.SetText("Message moved to trash")
					})
				}
			})
		}
	}()
}

func (mw *MainWindow) performDelete() {
	// Capture the selected message to avoid race conditions in the goroutine
	mw.messagesMu.RLock()
	selectedMessage := mw.selectedMessage
	mw.messagesMu.RUnlock()
	if selectedMessage == nil {
		mw.statusBar.SetText("No message selected")
		return
	}

	mw.statusBar.SetText("Deleting message...")
	go func() {
		// For unified inbox mode, set a flag to prevent IDLE monitoring from triggering
		// automatic refresh that would interfere with our optimistic update
		if mw.accountController.IsUnifiedInbox() {
			mw.deleteOperationMutex.Lock()
			if mw.unifiedInboxDeleteInProgress == nil {
				mw.unifiedInboxDeleteInProgress = make(map[string]bool)
			}
			mw.unifiedInboxDeleteInProgress[selectedMessage.AccountName] = true
			mw.deleteOperationMutex.Unlock()
			mw.logger.Debug("Set unified inbox delete operation flag for account %s", selectedMessage.AccountName)
		}

		// Use the MessageIndexItem's Delete method - much simpler!
		err := selectedMessage.Delete()

		// Clear the delete operation flag after the operation completes
		if mw.accountController.IsUnifiedInbox() {
			// Use a small delay to allow our optimistic update to complete first
			go func() {
				accountName := selectedMessage.AccountName // Capture the account name
				time.Sleep(200 * time.Millisecond)
				mw.deleteOperationMutex.Lock()
				if mw.unifiedInboxDeleteInProgress != nil {
					delete(mw.unifiedInboxDeleteInProgress, accountName)
				}
				mw.deleteOperationMutex.Unlock()
				mw.logger.Debug("Cleared unified inbox delete operation flag for account %s", accountName)
			}()
		}
		if err != nil {
			fyne.Do(func() {
				mw.statusBar.SetText(fmt.Sprintf("Failed to delete message: %v", err))
			})
			return
		}

		// Use RefreshCoordinator to refresh messages after deletion
		if mw.accountController.IsUnifiedInbox() {
			// For unified inbox, use optimistic UI update followed by proper refresh coordination
			fyne.Do(func() {
				// Find and remove the deleted message from the local array for immediate UI feedback
				deletedUID := selectedMessage.Message.UID
				deletedAccountName := selectedMessage.AccountName

				mw.logger.Debug("Attempting to remove deleted message UID=%d from account %s (current array size: %d)", deletedUID, deletedAccountName, len(mw.messages))

				// Remove the message from the local array
				messageRemoved := false
				for i, msg := range mw.messages {
					if msg.Message.UID == deletedUID && msg.AccountName == deletedAccountName {
						// Remove the message at index i
						mw.messages = append(mw.messages[:i], mw.messages[i+1:]...)
						mw.logger.Debug("Successfully removed deleted message from local array at index %d (UID=%d, Account=%s, new size: %d)", i, deletedUID, deletedAccountName, len(mw.messages))
						messageRemoved = true
						break
					}
				}

				if !messageRemoved {
					mw.logger.Error("Failed to find message to remove from local array (UID=%d, Account=%s)", deletedUID, deletedAccountName)
					mw.statusBar.SetText("Message deleted (UI update failed)")
					return
				}

				// Validate message array consistency after removal
				if !mw.validateMessageArrayConsistency() {
					mw.logger.Error("Message array consistency check failed after removing deleted message")
				}

				// Clear current selection first to avoid stale references
				mw.selectedMessage = nil
				mw.clearMessageSelection()

				// Force the message list to refresh with new length
				mw.refreshMessageList()
				mw.logger.Debug("Forced message list refresh after deletion (new length: %d)", len(mw.messages))

				// Clear the message display since no message is selected
				mw.messageViewController.ClearMessageView()

				mw.statusBar.SetText("Message deleted")
			})

			// Skip the expensive full unified inbox refresh since the optimistic update is sufficient
			// The message has been successfully deleted on the server, and we've updated the local UI
			// Real-time monitoring will catch any other changes from other clients
			mw.logger.Debug("Skipping full unified inbox refresh after deletion - using optimistic update only")
		} else {
			// Use RefreshCoordinator for regular folder refresh
			currentFolder := mw.folderController.GetCurrentFolder()
			mw.refreshMessagesDebounced(currentFolder, func(err error) {
				if err != nil {
					mw.logger.Error("Failed to refresh messages after deletion: %v", err)
					fyne.Do(func() {
						mw.statusBar.SetText(fmt.Sprintf("Message deleted, but refresh failed: %v", err))
					})
				} else {
					fyne.Do(func() {
						mw.statusBar.SetText("Message deleted")
					})
				}
			})
		}
	}()
}

// composeReplyOrForward opens a compose window in one of three modes.
func (mw *MainWindow) composeReplyOrForward(mode string) {
	if mw.selectedMessage == nil {
		var action string
		switch mode {
		case "reply", "replyAll":
			action = "reply to"
		default:
			action = "forward"
		}
		dialog.ShowInformation("No Message", "Please select a message to "+action, mw.window)
		return
	}

	account := mw.getAccountByName(mw.selectedMessage.AccountName)
	if account == nil {
		dialog.ShowError(fmt.Errorf("Cannot %s: account %s not found", mode, mw.selectedMessage.AccountName), mw.window)
		return
	}

	opts := ComposeOptions{
		Account:        account,
		SMTPClient:     mw.smtpClientForAccount(account),
		IMAPClient:     mw.imapClientForAccount(account),
		AddressbookMgr: mw.addressbookMgr,
		OnSent: func() {
			mw.refreshSentFolderAfterSend(account)
			fyne.Do(func() {
				if mode == "forward" {
					mw.statusBar.SetText("Message forwarded successfully")
				} else {
					mw.statusBar.SetText("Reply sent successfully")
				}
			})
		},
		OnClosed: func() {
			if mode == "forward" {
				mw.statusBar.SetText("Forward window closed")
			} else {
				mw.statusBar.SetText("Reply window closed")
			}
		},
	}
	switch mode {
	case "reply":
		opts.ReplyTo = &mw.selectedMessage.Message
	case "replyAll":
		opts.ReplyTo = &mw.selectedMessage.Message
		opts.ReplyAll = true
	default:
		opts.Forward = &mw.selectedMessage.Message
	}

	composeWindow := NewComposeWindow(mw.app, mw.configManagerToConfig(), opts)
	composeWindow.Show()
}

func (mw *MainWindow) replyToMessage() { mw.composeReplyOrForward("reply") }

func (mw *MainWindow) replyAllToMessage() { mw.composeReplyOrForward("replyAll") }

func (mw *MainWindow) forwardMessage() { mw.composeReplyOrForward("forward") }

func (mw *MainWindow) composeMessage() {
	if mw.accountController.GetCurrentAccount() == nil {
		// In unified inbox mode — let the user pick which account/persona to send from
		mw.showAccountSelectionForCompose()
		return
	}

	opts := ComposeOptions{
		Account:        mw.accountController.GetCurrentAccount(),
		SMTPClient:     mw.smtpClient,
		IMAPClient:     mw.imapClientForAccount(mw.accountController.GetCurrentAccount()),
		AddressbookMgr: mw.addressbookMgr,
		OnSent: func() {
			mw.refreshSentFolderAfterSend(mw.accountController.GetCurrentAccount())
			fyne.Do(func() {
				mw.statusBar.SetText("Message sent successfully")
			})
		},
		OnClosed: func() {
			mw.statusBar.SetText("Compose window closed")
		},
	}

	composeWindow := NewComposeWindow(mw.app, mw.configManagerToConfig(), opts)
	composeWindow.Show()
}

// refreshSentFolderAfterSend refreshes the given account's Sent folder after a
// successful send, regardless of which inbox view is active.
//
// In single-account mode, when the sent folder of the current account is on
// screen, this triggers a debounced refresh. In unified inbox mode there is no
// "current account" (the compose account selection dialog is the source of
// truth), so the sent-folder cache is invalidated instead — the copy was just
// stored server-side, and the next time the folder is viewed it will be
// fetched fresh.
func (mw *MainWindow) refreshSentFolderAfterSend(account *config.Account) {
	if account == nil || account.SentFolder == "" {
		return
	}

	currentAccount := mw.accountController.GetCurrentAccount()
	if !mw.accountController.IsUnifiedInbox() && currentAccount != nil && currentAccount.Name == account.Name {
		mw.refreshMessagesDebounced(account.SentFolder, func(err error) {
			if err != nil {
				mw.logger.Error("Failed to refresh sent folder after sending: %v", err)
			}
		})
		return
	}

	// Unified inbox mode (or another account's folder): the Sent folder is not
	// on screen. Invalidate its cache so the next view picks up the new copy.
	if client, exists := mw.accountController.GetIMAPClientForAccount(account.Name); exists && client != nil {
		client.InvalidateMessageCache(account.SentFolder)
		mw.logger.Debug("Invalidated %s cache for account %s after send", account.SentFolder, account.Name)
	} else {
		mw.logger.Warn("Cannot refresh sent folder for account %s: no IMAP client available", account.Name)
	}
}

// showAccountSelectionForCompose shows a dialog for selecting a sending account/persona
// when composing a new message from the unified inbox view.
func (mw *MainWindow) showAccountSelectionForCompose() {
	accounts := mw.config.GetAccounts()
	if len(accounts) == 0 {
		dialog.ShowInformation("No Accounts", "No email accounts are configured.", mw.window)
		return
	}

	type accountEntry struct {
		accountIndex     int
		personalityEmail string // empty = main account identity
		label            string
	}

	entries := make([]accountEntry, 0)
	for i, acc := range accounts {
		// Main account identity
		entries = append(entries, accountEntry{
			accountIndex:     i,
			personalityEmail: "",
			label:            fmt.Sprintf("%s (%s)", acc.Name, acc.Email),
		})
		// Personas
		for _, p := range acc.Personalities {
			entries = append(entries, accountEntry{
				accountIndex:     i,
				personalityEmail: p.Email,
				label:            fmt.Sprintf("%s — %s (%s)", acc.Name, p.DisplayName, p.Email),
			})
		}
	}

	options := make([]string, len(entries))
	for i, e := range entries {
		options[i] = e.label
	}

	sel := widget.NewSelect(options, nil)
	if len(options) > 0 {
		sel.SetSelected(options[0])
	}

	content := container.NewVBox(
		widget.NewLabel("Select the account or persona to send from:"),
		sel,
	)

	dialog.ShowCustomConfirm("Select Sending Account", "Compose", "Cancel", content, func(confirmed bool) {
		if !confirmed || sel.Selected == "" {
			return
		}

		// Find the selected entry
		var selectedEntry *accountEntry
		for i, option := range options {
			if option == sel.Selected {
				e := entries[i]
				selectedEntry = &e
				break
			}
		}
		if selectedEntry == nil {
			return
		}

		allAccounts := mw.config.GetAccounts()
		account := &allAccounts[selectedEntry.accountIndex]

		smtpClient := smtp.NewClient(email.ServerConfig{
			Host:     account.SMTP.Host,
			Port:     account.SMTP.Port,
			Username: account.SMTP.Username,
			Password: account.SMTP.Password,
			TLS:      account.SMTP.TLS,
		})

		opts := ComposeOptions{
			Account:        account,
			SMTPClient:     smtpClient,
			IMAPClient:     mw.imapClientForAccount(account),
			AddressbookMgr: mw.addressbookMgr,
			SelectedFrom:   selectedEntry.personalityEmail,
			OnSent: func() {
				mw.refreshSentFolderAfterSend(account)
				fyne.Do(func() {
					mw.statusBar.SetText("Message sent successfully")
				})
			},
			OnClosed: func() {
				mw.statusBar.SetText("Compose window closed")
			},
		}

		composeWindow := NewComposeWindow(mw.app, mw.configManagerToConfig(), opts)
		composeWindow.Show()
	}, mw.window)
}

// showMessageContextMenu displays a context menu for the selected message
func (mw *MainWindow) showMessageContextMenu() {
	if mw.selectedMessage == nil {
		dialog.ShowInformation("No Message", "Please select a message first", mw.window)
		return
	}

	mw.logger.Debug("Creating context menu for message: %s", mw.selectedMessage.Message.Subject)

	// Show the context menu at a reasonable position
	canvasSize := mw.window.Canvas().Size()
	menuPos := fyne.NewPos(canvasSize.Width/2-100, canvasSize.Height/2-100)

	mw.logger.Debug("Showing context menu at position: %v", menuPos)
	mw.showCustomContextMenuAtPosition(menuPos)
}

// showMessageContextMenuAtPosition shows the context menu at a specific position
func (mw *MainWindow) showMessageContextMenuAtPosition(pos fyne.Position) {
	if mw.selectedMessage == nil {
		dialog.ShowInformation("No Message", "Please select a message first", mw.window)
		return
	}

	// Create a custom popup with buttons instead of using ShowPopUpMenuAtPosition
	mw.showCustomContextMenuAtPosition(pos)
}

// showCustomContextMenuAtPosition creates a custom popup menu with buttons that can be properly dismissed
func (mw *MainWindow) showCustomContextMenuAtPosition(pos fyne.Position) {
	selectedIndices := mw.getSelectedMessageIndices()
	selectedCount := len(selectedIndices)

	var content *fyne.Container

	if selectedCount <= 1 {
		// Single message context menu
		content = mw.createSingleMessageContextMenu()
	} else {
		// Multiple messages context menu
		content = mw.createMultipleMessagesContextMenu(selectedCount)
	}

	// Add padding around the content for better appearance
	paddedContent := container.NewPadded(content)

	// Create popup and store reference for dismissal
	mw.currentContextPopup = widget.NewPopUp(paddedContent, mw.window.Canvas())

	// Set a minimum size for the popup to ensure consistent width
	mw.currentContextPopup.Resize(fyne.NewSize(180, 0)) // Fixed width, auto height
	mw.currentContextPopup.ShowAtPosition(pos)
}

// createSingleMessageContextMenu creates context menu for single message selection
func (mw *MainWindow) createSingleMessageContextMenu() *fyne.Container {
	replyButton := mw.createContextMenuButton("Reply", func() {
		mw.currentContextPopup.Hide()
		mw.replyToMessage()
	})

	replyAllButton := mw.createContextMenuButton("Reply All", func() {
		mw.currentContextPopup.Hide()
		mw.replyAllToMessage()
	})

	forwardButton := mw.createContextMenuButton("Forward", func() {
		mw.currentContextPopup.Hide()
		mw.forwardMessage()
	})

	viewHeadersButton := mw.createContextMenuButton("View Headers", func() {
		mw.currentContextPopup.Hide()
		mw.showMessageHeaders()
	})

	copyTextButton := mw.createContextMenuButton("Copy Text", func() {
		mw.currentContextPopup.Hide()
		mw.copyMessageText()
	})

	moveToFolderButton := mw.createContextMenuButton("Move to Folder...", func() {
		mw.currentContextPopup.Hide()
		mw.showMoveToFolderDialog()
	})

	deleteButton := mw.createContextMenuButton("Delete", func() {
		mw.currentContextPopup.Hide()
		mw.moveToTrash()
	})

	return container.NewVBox(
		replyButton,
		replyAllButton,
		forwardButton,
		widget.NewSeparator(),
		viewHeadersButton,
		copyTextButton,
		widget.NewSeparator(),
		moveToFolderButton,
		widget.NewSeparator(),
		deleteButton,
	)
}

// createMultipleMessagesContextMenu creates context menu for multiple message selection
func (mw *MainWindow) createMultipleMessagesContextMenu(count int) *fyne.Container {
	// Header showing selection count
	headerLabel := widget.NewLabel(fmt.Sprintf("%d messages selected", count))
	headerLabel.TextStyle = fyne.TextStyle{Bold: true}

	moveToFolderButton := mw.createContextMenuButton("Move to Folder...", func() {
		mw.currentContextPopup.Hide()
		mw.showMoveToFolderDialogMultiple()
	})

	deleteButton := mw.createContextMenuButton(fmt.Sprintf("Delete %d Messages", count), func() {
		mw.currentContextPopup.Hide()
		mw.moveToTrashMultiple()
	})

	clearSelectionButton := mw.createContextMenuButton("Clear Selection", func() {
		mw.currentContextPopup.Hide()
		mw.clearSelection()
	})

	return container.NewVBox(
		headerLabel,
		widget.NewSeparator(),
		moveToFolderButton,
		widget.NewSeparator(),
		deleteButton,
		widget.NewSeparator(),
		clearSelectionButton,
	)
}

// createContextMenuButton creates a button styled like a context menu item
func (mw *MainWindow) createContextMenuButton(text string, onTapped func()) *widget.Button {
	button := widget.NewButton(text, onTapped)

	// Style the button to look like a context menu item
	button.Alignment = widget.ButtonAlignLeading // Left-align the text
	button.Importance = widget.LowImportance     // Remove button styling/background

	return button
}
