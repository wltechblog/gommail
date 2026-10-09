package ui

// Code in this file was mechanically split from main_window.go.

import (
	"context"
	"encoding/json"
	"fmt"
	_ "image/gif"  // Register GIF format
	_ "image/jpeg" // Register JPEG format
	_ "image/png"  // Register PNG format
	"strings"
	"time"

	"fyne.io/fyne/v2"

	"github.com/wltechblog/gommail/internal/config"
	"github.com/wltechblog/gommail/internal/email"
	"github.com/wltechblog/gommail/pkg/imap"
	"github.com/wltechblog/gommail/pkg/notification"
	"github.com/wltechblog/gommail/pkg/smtp"
)

// selectUnifiedInbox handles unified inbox selection by showing cached data first
// while reconnecting and refreshing in the background.
func (mw *MainWindow) selectUnifiedInbox() {
	mw.selectUnifiedInboxWithOptions(false)
}

// selectUnifiedInboxWithOptions handles unified inbox selection and can optionally
// force a fresh refresh for explicit reload paths.
func (mw *MainWindow) selectUnifiedInboxWithOptions(forceRefresh bool) {
	mw.logger.Info("Selecting unified inbox")

	// Set unified inbox state
	mw.accountController.SetUnifiedInbox(true)
	mw.accountController.ClearCurrentAccount()
	mw.folderController.SelectFolder("") // Clear current folder
	if forceRefresh {
		mw.statusBar.SetText("Refreshing unified inbox...")
	} else {
		mw.statusBar.SetText("Loading unified inbox...")
	}

	// Clear current selection and message viewer (but keep messages until new ones load)
	mw.selectedMessage = nil
	mw.updateMessageViewer("")
	if folderList := mw.folderController.GetFolderList(); folderList != nil {
		folderList.UnselectAll()
	}

	// Cancel any active read timer
	mw.cancelReadTimer()

	// Stop any existing monitoring
	mw.stopFolderMonitoring()
	mw.stopUnifiedInboxMonitoring()

	// Clear folder list when unified inbox is selected
	mw.folderController.SetFolders([]email.Folder{})
	if folderList := mw.folderController.GetFolderList(); folderList != nil {
		folderList.Refresh()
	}

	// Load unified messages (don't clear messages array until new ones are ready)
	go mw.loadUnifiedInboxMessages(forceRefresh)
}

func (mw *MainWindow) startUnifiedInboxMonitoringFromConnectedClients() {
	connectedClientCount := 0
	mw.accountController.ForEachClient(func(accountName string, client *imap.ClientWrapper) {
		if client == nil || !client.IsConnected() {
			return
		}

		client.MarkInitialSyncComplete("INBOX")
		connectedClientCount++
	})

	if connectedClientCount > 0 {
		mw.logger.Info("Starting unified inbox monitoring from %d already-connected account clients", connectedClientCount)
		mw.startUnifiedInboxMonitoring()
	}
}

// loadUnifiedInboxMessages loads messages from all accounts' INBOX folders.
// It always works toward a live connected unified inbox, but displays cached data
// immediately when available so the UI is responsive.
func (mw *MainWindow) loadUnifiedInboxMessages(forceRefresh bool) {
	mw.logger.Info("Loading unified inbox messages from all accounts (forceRefresh=%v)", forceRefresh)

	// Try cached unified inbox messages first for immediate display.
	cachedMessages, cachedMessagesFound := mw.loadUnifiedInboxFromCache()
	if cachedMessagesFound {
		fyne.Do(func() {
			if !mw.accountController.IsUnifiedInbox() {
				mw.logger.Debug("User switched away from unified inbox before cached load completed")
				return
			}

			mw.messages = cachedMessages
			mw.sortMessages()
			mw.clearMessageSelection()
			mw.refreshMessageList()
			mw.statusBar.SetText(fmt.Sprintf("Loaded %d cached messages from unified inbox (checking for updates...)", len(mw.messages)))
		})
	} else {
		// Show loading state immediately when cache is unavailable.
		fyne.Do(func() {
			mw.messages = []email.MessageIndexItem{}
			mw.clearMessageSelection()
			mw.refreshMessageList()
			mw.statusBar.SetText("Loading messages from all accounts...")
		})
	}

	// Start monitoring immediately for any accounts that are already connected,
	// then continue the full background refresh which will connect remaining
	// accounts and refresh the unified inbox contents.
	if !forceRefresh {
		mw.startUnifiedInboxMonitoringFromConnectedClients()
	}

	// Fetch fresh messages in background and wait for ALL accounts to complete
	// before replacing the unified mailbox contents.
	mw.backgroundWg.Add(1)
	go func() {
		defer mw.backgroundWg.Done()
		mw.fetchFreshUnifiedInboxMessagesInBackground()
	}()
}

// fetchFreshUnifiedInboxMessagesInBackground fetches fresh messages without blocking the UI
func (mw *MainWindow) fetchFreshUnifiedInboxMessagesInBackground() {
	mw.logger.Info("Starting background fetch of fresh unified inbox messages")

	// Account clients are managed by AccountController
	accounts := mw.config.GetAccounts()
	mw.logger.Info("Fetching fresh messages from %d accounts for unified inbox (waiting for all to complete)", len(accounts))

	// Use a channel to collect results from parallel account processing
	type accountResult struct {
		accountName string
		messages    []email.MessageIndexItem
		err         error
	}
	resultChan := make(chan accountResult, len(accounts))

	// Track total accounts for status updates
	totalAccounts := int32(len(accounts))

	// Process accounts in parallel
	for _, account := range accounts {
		go func(acc config.Account) {
			// Check if we should continue (user might have switched away from unified inbox)
			if !mw.accountController.IsUnifiedInbox() {
				mw.logger.Debug("No longer in unified inbox mode, skipping account %s", acc.Name)
				resultChan <- accountResult{accountName: acc.Name, err: fmt.Errorf("unified inbox mode exited")}
				return
			}

			mw.logger.Debug("Background fetching fresh INBOX messages for account: %s", acc.Name)

			// Create or get IMAP client for this account
			client, err := mw.getOrCreateIMAPClient(&acc)
			if err != nil {
				mw.logger.Error("Failed to create IMAP client for account %s: %v", acc.Name, err)
				resultChan <- accountResult{accountName: acc.Name, err: err}
				return
			}

			// Fetch fresh messages from INBOX
			messages, err := client.FetchFreshMessages("INBOX", 0)
			if err != nil {
				mw.logger.Error("Failed to fetch fresh INBOX messages for account %s: %v", acc.Name, err)
				resultChan <- accountResult{accountName: acc.Name, err: err}
				return
			}

			mw.logger.Debug("Background fetched %d fresh messages from account %s INBOX", len(messages), acc.Name)

			// Create SMTP client for this account
			smtpClient := smtp.NewClient(email.ServerConfig{
				Host:     acc.SMTP.Host,
				Port:     acc.SMTP.Port,
				Username: acc.SMTP.Username,
				Password: acc.SMTP.Password,
				TLS:      acc.SMTP.TLS,
			})

			// Convert to MessageIndexItem
			var indexItems []email.MessageIndexItem
			for _, msg := range messages {
				indexItem := email.MessageIndexItem{
					Message:      msg,
					AccountName:  acc.Name,
					AccountEmail: acc.Email,
					FolderName:   "INBOX",
					IMAPClient:   client,
					SMTPClient:   smtpClient,
					AccountConfig: &email.AccountConfig{
						Name:        acc.Name,
						Email:       acc.Email,
						DisplayName: acc.DisplayName,
					},
				}
				indexItems = append(indexItems, indexItem)
			}

			// Update the cache for this account in background
			go func(accName string, msgs []email.Message) {
				mw.cacheAccountMessages(accName, msgs)
			}(acc.Name, messages)

			// Send result
			resultChan <- accountResult{accountName: acc.Name, messages: indexItems, err: nil}
		}(account)
	}

	// Collect results from all accounts - wait for ALL to complete before displaying
	go func() {
		var allNewMessages []email.MessageIndexItem
		var completedCount int32

		for i := 0; i < len(accounts); i++ {
			result := <-resultChan

			if result.err != nil {
				mw.logger.Info("Account %s completed with error: %v", result.accountName, result.err)
			} else {
				mw.logger.Info("Account %s completed successfully with %d messages", result.accountName, len(result.messages))
				allNewMessages = append(allNewMessages, result.messages...)
			}

			// Update status to show progress (but don't update UI yet)
			completedCount++
			fyne.Do(func() {
				mw.statusBar.SetText(fmt.Sprintf("Loading %d/%d accounts...", completedCount, totalAccounts))
			})
		}

		// ALL accounts processed - now update UI with all messages at once
		mw.logger.Info("All %d accounts completed, displaying %d total messages", len(accounts), len(allNewMessages))

		fyne.Do(func() {
			if !mw.accountController.IsUnifiedInbox() {
				mw.logger.Warn("User switched away from unified inbox, skipping final update")
				return // User switched away, don't update
			}

			// Set all messages at once
			mw.messagesMu.Lock()
			mw.messages = allNewMessages
			mw.selectedMessage = nil // previous pointer is now invalid
			mw.messagesMu.Unlock()
			mw.logger.Info("Set messages array to %d messages from all accounts", len(mw.messages))

			// Apply user's sort preferences
			mw.sortMessages()

			// Validate message array consistency
			if !mw.validateMessageArrayConsistency() {
				mw.logger.Error("Message array consistency check failed after unified inbox load")
			}

			// Force the list to update by clearing selection and refreshing
			mw.clearMessageSelection()
			mw.refreshMessageList()

			mw.logger.Info("Refreshed message list with %d total messages", len(mw.messages))

			// Auto-select first message if available
			if len(mw.messages) > 0 && !mw.autoSelectionDone {
				mw.messageList.Select(0)
				mw.selectMessage(0)
				mw.autoSelectionDone = true
			}

			// Mark initial sync complete for all account INBOX folders to enable notifications
			mw.accountController.ForEachClient(func(accountName string, client *imap.ClientWrapper) {
				client.MarkInitialSyncComplete("INBOX")
			})

			// Start monitoring for all accounts in unified inbox
			mw.startUnifiedInboxMonitoring()

			// Update status to show sync is complete
			mw.statusBar.SetText(fmt.Sprintf("Loaded %d messages from unified inbox", len(mw.messages)))
			mw.logger.Info("Background unified inbox refresh completed - %d total messages", len(mw.messages))
		})
	}()
}

// refreshSingleAccountInUnifiedInbox fetches fresh messages from a single account and merges them into unified inbox
func (mw *MainWindow) refreshSingleAccountInUnifiedInbox(accountName string) {
	mw.logger.Info("Refreshing single account %s in unified inbox", accountName)

	// Find the account config
	targetAccount := mw.getAccountByName(accountName)
	if targetAccount == nil {
		mw.logger.Error("Account %s not found in config", accountName)
		return
	}

	// Get or create IMAP client for this account
	client, err := mw.getOrCreateIMAPClient(targetAccount)
	if err != nil {
		mw.logger.Error("Failed to get IMAP client for account %s: %v", accountName, err)
		return
	}

	// Fetch fresh messages from INBOX
	messages, err := client.FetchFreshMessages("INBOX", 0)
	if err != nil {
		mw.logger.Error("Failed to fetch fresh INBOX messages for account %s: %v", accountName, err)
		return
	}

	mw.logger.Info("Fetched %d fresh messages from account %s", len(messages), accountName)

	// Create SMTP client for this account
	smtpClient := smtp.NewClient(email.ServerConfig{
		Host:     targetAccount.SMTP.Host,
		Port:     targetAccount.SMTP.Port,
		Username: targetAccount.SMTP.Username,
		Password: targetAccount.SMTP.Password,
		TLS:      targetAccount.SMTP.TLS,
	})

	// Convert to MessageIndexItem
	var indexItems []email.MessageIndexItem
	for _, msg := range messages {
		indexItem := email.MessageIndexItem{
			Message:      msg,
			AccountName:  targetAccount.Name,
			AccountEmail: targetAccount.Email,
			FolderName:   "INBOX",
			IMAPClient:   client,
			SMTPClient:   smtpClient,
			AccountConfig: &email.AccountConfig{
				Name:        targetAccount.Name,
				Email:       targetAccount.Email,
				DisplayName: targetAccount.DisplayName,
			},
		}
		indexItems = append(indexItems, indexItem)
	}

	// Update UI with merged messages
	fyne.Do(func() {
		if !mw.accountController.IsUnifiedInbox() {
			mw.logger.Debug("User switched away from unified inbox, skipping update")
			return
		}

		// Merge new messages with existing ones, avoiding duplicates
		stats := mw.mergeNewMessagesIntoUnifiedInbox(indexItems)
		afterCount := len(mw.messages)
		changedCount := stats.Added + stats.Replaced

		if changedCount > 0 {
			mw.logger.Info("Added %d new messages from account %s (total now: %d)", changedCount, accountName, afterCount)

			// Apply user's sort preferences
			mw.sortMessages()

			// Force the list to update
			mw.clearMessageSelection()
			mw.refreshMessageList()

			mw.statusBar.SetText(fmt.Sprintf("Added %d new messages from %s", changedCount, accountName))
		} else {
			mw.logger.Debug("No new messages to add from account %s (all were duplicates)", accountName)
			mw.statusBar.SetText(fmt.Sprintf("No new messages from %s", accountName))
		}
	})
}

// injectNewMessagesIntoUnifiedInboxForAccount injects already-fetched new messages for a single
// account into the unified inbox without performing a full mailbox re-fetch.
// This is the fast path called by the IDLE new-message callback.
func (mw *MainWindow) injectNewMessagesIntoUnifiedInboxForAccount(accountName, folder string, messages []email.Message) {
	mw.logger.Info("Injecting %d new messages for account %s (folder %s) into unified inbox", len(messages), accountName, folder)

	// Find the account config
	targetAccount := mw.getAccountByName(accountName)
	if targetAccount == nil {
		mw.logger.Error("injectNewMessagesIntoUnifiedInboxForAccount: account %s not found in config", accountName)
		return
	}

	client, exists := mw.accountController.GetIMAPClientForAccount(accountName)
	if !exists || client == nil {
		mw.logger.Error("injectNewMessagesIntoUnifiedInboxForAccount: no IMAP client for account %s", accountName)
		return
	}

	// Create SMTP client for this account
	smtpClient := smtp.NewClient(email.ServerConfig{
		Host:     targetAccount.SMTP.Host,
		Port:     targetAccount.SMTP.Port,
		Username: targetAccount.SMTP.Username,
		Password: targetAccount.SMTP.Password,
		TLS:      targetAccount.SMTP.TLS,
	})

	// Convert to MessageIndexItem
	indexItems := make([]email.MessageIndexItem, 0, len(messages))
	for _, msg := range messages {
		indexItems = append(indexItems, email.MessageIndexItem{
			Message:      msg,
			AccountName:  targetAccount.Name,
			AccountEmail: targetAccount.Email,
			FolderName:   folder,
			IMAPClient:   client,
			SMTPClient:   smtpClient,
			AccountConfig: &email.AccountConfig{
				Name:        targetAccount.Name,
				Email:       targetAccount.Email,
				DisplayName: targetAccount.DisplayName,
			},
		})
	}

	// Merge into the unified inbox on the UI goroutine
	fyne.Do(func() {
		if !mw.accountController.IsUnifiedInbox() {
			mw.logger.Debug("User switched away from unified inbox, skipping inject")
			return
		}

		stats := mw.mergeNewMessagesIntoUnifiedInbox(indexItems)
		afterCount := len(mw.messages)
		changedCount := stats.Added + stats.Replaced

		if changedCount > 0 {
			mw.logger.Info("Injected %d new messages from account %s (total now: %d)", changedCount, accountName, afterCount)
			mw.sortMessages()
			mw.clearMessageSelection()
			mw.refreshMessageList()
			mw.statusBar.SetText(fmt.Sprintf("New messages from %s (%d)", accountName, changedCount))
		} else {
			mw.logger.Debug("No new messages to inject from account %s (all were duplicates)", accountName)
		}
	})
}

// mergeNewMessagesIntoUnifiedInbox merges new messages into the existing unified inbox, avoiding duplicates.
// Returns statistics describing what the merge changed; callers should treat
// any Added or Replaced entry as a list-changing event (refresh, sort).
func (mw *MainWindow) mergeNewMessagesIntoUnifiedInbox(newMessages []email.MessageIndexItem) mergeStats {
	if len(newMessages) == 0 {
		mw.logger.Debug("mergeNewMessagesIntoUnifiedInbox: No new messages to merge")
		return mergeStats{}
	}

	mw.logger.Info("mergeNewMessagesIntoUnifiedInbox: Starting merge of %d new messages into %d existing messages",
		len(newMessages), len(mw.messages))

	merged, stats := mergeIntoUnifiedInbox(mw.messages, newMessages)
	mw.messages = merged

	mw.logger.Info("mergeNewMessagesIntoUnifiedInbox: Merged %d new messages (added %d, replaced %d stale, skipped %d duplicates), total now: %d",
		stats.Added+stats.Replaced, stats.Added, stats.Replaced, stats.Duplicate, len(mw.messages))

	return stats
}

// loadUnifiedInboxFromCache loads unified inbox messages from cache
func (mw *MainWindow) loadUnifiedInboxFromCache() ([]email.MessageIndexItem, bool) {
	if mw.cache == nil || mw.config == nil {
		return nil, false
	}

	var allMessages []email.MessageIndexItem
	cacheFound := false

	accounts := mw.config.GetAccounts()
	for _, account := range accounts {
		messages, found := mw.loadCachedMessagesDirectly(account.Name, "INBOX")
		if !found {
			continue
		}

		cacheFound = true

		// Use an already-connected client if one exists; otherwise leave IMAPClient nil
		// so selecting a cached message can connect lazily on demand.
		var imapClient *imap.ClientWrapper
		if existingClient, exists := mw.accountController.GetIMAPClientForAccount(account.Name); exists && existingClient != nil && existingClient.IsConnected() {
			imapClient = existingClient
		}

		// Create SMTP client for this account (lightweight, no connection)
		smtpClient := smtp.NewClient(email.ServerConfig{
			Host:     account.SMTP.Host,
			Port:     account.SMTP.Port,
			Username: account.SMTP.Username,
			Password: account.SMTP.Password,
			TLS:      account.SMTP.TLS,
		})

		for _, msg := range messages {
			indexItem := email.MessageIndexItem{
				Message:      msg,
				AccountName:  account.Name,
				AccountEmail: account.Email,
				FolderName:   "INBOX",
				IMAPClient:   imapClient,
				SMTPClient:   smtpClient,
				AccountConfig: &email.AccountConfig{
					Name:        account.Name,
					Email:       account.Email,
					DisplayName: account.DisplayName,
					IMAP: email.ServerConfig{
						Host:     account.IMAP.Host,
						Port:     account.IMAP.Port,
						Username: account.IMAP.Username,
						Password: account.IMAP.Password,
						TLS:      account.IMAP.TLS,
					},
					SMTP: email.ServerConfig{
						Host:     account.SMTP.Host,
						Port:     account.SMTP.Port,
						Username: account.SMTP.Username,
						Password: account.SMTP.Password,
						TLS:      account.SMTP.TLS,
					},
				},
			}
			allMessages = append(allMessages, indexItem)
		}

		mw.logger.Debug("Loaded %d cached messages from account %s INBOX for unified inbox", len(messages), account.Name)
	}

	// Don't sort here - let the UI apply user's sort preferences
	return allMessages, cacheFound
}

// cacheAccountMessages caches messages for a specific account
func (mw *MainWindow) cacheAccountMessages(accountName string, messages []email.Message) {
	cacheKey := fmt.Sprintf("account_%s:messages:INBOX", accountName)
	if data, err := json.Marshal(messages); err == nil {
		// Cache for 24 hours
		if err := mw.cache.Set(cacheKey, data, 24*time.Hour); err != nil {
			mw.logger.Warn("Failed to cache messages for account %s: %v", accountName, err)
		} else {
			mw.logger.Debug("Cached %d messages for account %s", len(messages), accountName)
		}
	}
}

// invalidateUnifiedInboxCache clears cached data for all accounts to force fresh loading
func (mw *MainWindow) invalidateUnifiedInboxCache() {
	mw.logger.Debug("Invalidating unified inbox cache for all accounts")

	accounts := mw.config.GetAccounts()
	for _, account := range accounts {
		// Invalidate message cache for each account's INBOX
		if client, exists := mw.accountController.GetIMAPClientForAccount(account.Name); exists && client != nil {
			client.InvalidateMessageCache("INBOX")
		}
	}
}

// startUnifiedInboxMonitoring starts monitoring for all accounts in unified inbox mode
func (mw *MainWindow) startUnifiedInboxMonitoring() {
	if !mw.accountController.IsUnifiedInbox() {
		mw.logger.Debug("Not in unified inbox mode, skipping unified monitoring")
		return
	}

	mw.logger.Info("Starting unified inbox monitoring for all accounts")

	// Check if monitoring is already active AND healthy for all accounts before stopping
	allAccountsHealthyMonitoring := true
	activeMonitoringCount := 0
	healthyMonitoringCount := 0
	connectedClientCount := 0

	mw.accountController.ForEachClient(func(accountName string, client *imap.ClientWrapper) {
		if client == nil || !client.IsConnected() {
			allAccountsHealthyMonitoring = false
			return
		}

		connectedClientCount++
		isMonitoring := client.IsMonitoring()
		if isMonitoring {
			activeMonitoringCount++

			// Check if the connection is actually healthy
			isHealthy := mw.isClientConnectionHealthy(client, accountName)
			if isHealthy {
				healthyMonitoringCount++
			} else {
				allAccountsHealthyMonitoring = false
				mw.logger.Warn("Account %s is monitoring but connection is unhealthy", accountName)
			}
		} else {
			allAccountsHealthyMonitoring = false
		}
	})
	// Unlock handled by AccountController

	if connectedClientCount == 0 {
		mw.logger.Debug("No connected account clients available for unified inbox monitoring")
		return
	}

	// Only skip restart if all accounts are monitoring AND all connections are healthy
	if allAccountsHealthyMonitoring && healthyMonitoringCount > 0 {
		mw.logger.Info("All accounts (%d) are monitoring INBOX with healthy connections, skipping restart", healthyMonitoringCount)
		return
	}

	// Log the reason for restart
	if activeMonitoringCount > 0 {
		mw.logger.Info("Restarting monitoring: %d accounts monitoring, %d with healthy connections", activeMonitoringCount, healthyMonitoringCount)
	}

	// Only stop existing monitoring if we need to start new monitoring
	if activeMonitoringCount > 0 {
		mw.logger.Info("Stopping existing monitoring for %d accounts before restart (some accounts not monitoring)", activeMonitoringCount)
		mw.stopUnifiedInboxMonitoring()
	} else {
		mw.logger.Info("No existing monitoring detected, starting fresh monitoring for all accounts")
	}

	// Start monitoring for each account's INBOX
	monitoringCount := 0
	mw.accountController.ForEachClient(func(accountName string, client *imap.ClientWrapper) {
		if client == nil || !client.IsConnected() {
			mw.logger.Debug("Skipping unified inbox monitoring for disconnected account %s", accountName)
			return
		}

		// Set up connection state callback for error handling (capture accountName properly)
		func(capturedAccountName string) {
			client.SetConnectionStateCallback(func(event email.ConnectionEvent) {
				// Handle connection errors during monitoring
				if event.Error != nil {
					mw.logger.Error("Connection error for account %s: %v", capturedAccountName, event.Error)
					fyne.Do(func() {
						mw.statusBar.SetText(fmt.Sprintf("Connection error for %s: %v", capturedAccountName, event.Error))
					})
				}
			})
		}(accountName)

		// Set up combined new message callback for both unified inbox refresh AND notifications (capture accountName properly)
		func(capturedAccountName string) {
			client.SetNewMessageCallback(func(updatedFolder string, messages []email.Message) {
				mw.logger.Info("Real-time update detected in %s for account %s (%d messages)", updatedFolder, capturedAccountName, len(messages))

				// PART 1: Handle unified inbox refresh
				// Check if we're in the middle of a unified inbox delete operation for this account
				mw.deleteOperationMutex.RLock()
				deleteInProgress := false
				if mw.unifiedInboxDeleteInProgress != nil {
					deleteInProgress = mw.unifiedInboxDeleteInProgress[capturedAccountName]
				}
				mw.deleteOperationMutex.RUnlock()

				if !deleteInProgress {
					if len(messages) > 0 {
						// Fast path: inject only the newly arrived messages without re-fetching the whole mailbox.
						mw.backgroundWg.Add(1)
						go func() {
							defer mw.backgroundWg.Done()
							mw.injectNewMessagesIntoUnifiedInboxForAccount(capturedAccountName, updatedFolder, messages)
						}()
						// If the delivery was capped, schedule a full refresh to pick up any missed messages.
						if len(messages) >= imap.MaxNewMessageNotifications {
							mw.logger.Info("Notification cap reached for account %s, scheduling follow-up full refresh", capturedAccountName)
							mw.backgroundWg.Add(1)
							go func() {
								defer mw.backgroundWg.Done()
								mw.refreshSingleAccountInUnifiedInbox(capturedAccountName)
							}()
						}
					} else {
						// Fallback: server signalled a change but no messages were available yet; full refresh.
						fyne.Do(func() {
							mw.statusBar.SetText(fmt.Sprintf("New messages detected in %s, refreshing...", capturedAccountName))
						})
						mw.backgroundWg.Add(1)
						go func() {
							defer mw.backgroundWg.Done()
							mw.refreshSingleAccountInUnifiedInbox(capturedAccountName)
						}()
					}
				} else {
					mw.logger.Debug("Skipping unified inbox refresh due to delete operation in progress (account: %s, folder: %s)", capturedAccountName, updatedFolder)
				}

				// PART 2: Handle desktop notifications for new messages
				if mw.notificationMgr != nil && mw.notificationMgr.IsEnabled() {
					// Convert messages to notification format
					var notificationMessages []notification.MessageInfo
					for _, message := range messages {
						// Extract sender name from the From field
						sender := "Unknown Sender"
						if len(message.From) > 0 {
							if message.From[0].Name != "" {
								sender = message.From[0].Name
							} else {
								sender = message.From[0].Email
							}
						}

						// Extract subject
						subject := message.Subject
						if subject == "" {
							subject = "(No Subject)"
						}

						notificationMessages = append(notificationMessages, notification.MessageInfo{
							Sender:  sender,
							Subject: subject,
						})
					}

					// Show batched notification with account name instead of folder name
					// This makes it clear which account received the message
					err := mw.notificationMgr.ShowNewMessages(notificationMessages, capturedAccountName)
					if err != nil {
						mw.logger.Error("Failed to show notification: %v", err)
					}
				}
			})
		}(accountName)

		// Start monitoring INBOX for this account (capture accountName properly)
		func(capturedAccountName string) {
			if err := client.StartMonitoring("INBOX"); err != nil {
				mw.logger.Error("Failed to start monitoring INBOX for account %s: %v", capturedAccountName, err)
			} else {
				mode := client.GetMonitoringMode()
				mw.logger.Info("Started monitoring INBOX for account '%s' using %s mode", capturedAccountName, mode.String())
				monitoringCount++
			}
		}(accountName)
	})

	if monitoringCount > 0 {
		// Show monitoring status in UI
		fyne.Do(func() {
			currentStatus := mw.statusBar.Text
			if !strings.Contains(currentStatus, "monitoring") {
				mw.statusBar.SetText(fmt.Sprintf("%s (monitoring %d accounts)", currentStatus, monitoringCount))
			}
		})

		// Start a fresh unified inbox health monitor. Repeated calls to
		// startUnifiedInboxMonitoring() should not leave multiple monitors running.
		mw.restartUnifiedInboxHealthMonitoring()
	}
}

// monitorConnectionHealth periodically checks connection health for all accounts
func (mw *MainWindow) monitorConnectionHealth(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second) // Check every 30 seconds
	defer ticker.Stop()

	mw.logger.Debug("Starting connection health monitoring")

	for {
		select {
		case <-ctx.Done():
			mw.logger.Debug("Connection health monitoring stopped")
			return
		case <-ticker.C:
			mw.performHealthChecks()
		}
	}
}

// startGlobalHealthMonitoring starts health monitoring that works in both unified and single account modes
func (mw *MainWindow) startGlobalHealthMonitoring() {
	// Stop any existing global health monitoring
	mw.stopGlobalHealthMonitoring()

	// Create a new context for global health monitoring
	mw.globalHealthCtx, mw.globalHealthCancel = context.WithCancel(context.Background())

	// Start the global health monitoring goroutine
	go mw.monitorConnectionHealth(mw.globalHealthCtx)

	mw.logger.Debug("Started global connection health monitoring")
}

// stopGlobalHealthMonitoring stops the global health monitoring
func (mw *MainWindow) stopGlobalHealthMonitoring() {
	if mw.globalHealthCancel != nil {
		mw.globalHealthCancel()
		mw.globalHealthCancel = nil
		mw.globalHealthCtx = nil
		mw.logger.Debug("Stopped global connection health monitoring")
	}
}

func (mw *MainWindow) restartUnifiedInboxHealthMonitoring() {
	mw.unifiedInboxHealthMu.Lock()
	defer mw.unifiedInboxHealthMu.Unlock()

	if mw.unifiedInboxCancel != nil {
		mw.unifiedInboxCancel()
	}

	ctx, cancel := context.WithCancel(context.Background())
	mw.unifiedInboxCtx = ctx
	mw.unifiedInboxCancel = cancel

	go mw.monitorConnectionHealth(ctx)
}

func (mw *MainWindow) stopUnifiedInboxHealthMonitoring() {
	mw.unifiedInboxHealthMu.Lock()
	defer mw.unifiedInboxHealthMu.Unlock()

	if mw.unifiedInboxCancel != nil {
		mw.unifiedInboxCancel()
		mw.unifiedInboxCancel = nil
		mw.unifiedInboxCtx = nil
		mw.logger.Debug("Stopped unified inbox connection health monitoring")
	}
}

// performHealthChecks checks the health of all account connections.
// This is observation-only: the worker owns reconnection via its own health
// ticker and attemptAutomaticReconnection. The UI monitor only updates the
// status bar to keep the user informed.
func (mw *MainWindow) performHealthChecks() {
	unhealthyAccounts := 0
	totalAccounts := 0

	// In unified inbox mode, check all accounts
	if mw.accountController.IsUnifiedInbox() {
		mw.accountController.ForEachClient(func(accountName string, client *imap.ClientWrapper) {
			totalAccounts++

			// Check if connection is healthy (observation only)
			isHealthy := mw.isClientConnectionHealthy(client, accountName)

			if !isHealthy {
				unhealthyAccounts++
				mw.logger.Warn("Health check: account %s connection unhealthy (worker will handle reconnection)", accountName)
			}
		})
	} else {
		// In single account mode, check the current account
		if mw.accountController.GetCurrentAccount() != nil && mw.imapClient != nil {
			if mw.isCurrentAccountConnectInProgress() {
				accountName := mw.accountController.GetCurrentAccount().Name
				mw.logger.Debug("Skipping health check for current account %s while account connection is in progress", accountName)
				return
			}

			totalAccounts = 1
			accountName := mw.accountController.GetCurrentAccount().Name

			// Check if connection is healthy (observation only)
			isHealthy := mw.isClientConnectionHealthy(mw.imapClient, accountName)

			if !isHealthy {
				unhealthyAccounts++
				mw.logger.Warn("Health check: current account %s connection unhealthy (worker will handle reconnection)", accountName)
			}
		}
	}

	if unhealthyAccounts > 0 {
		mw.logger.Warn("Health check completed: %d/%d accounts have unhealthy connections", unhealthyAccounts, totalAccounts)
		fyne.Do(func() {
			mw.statusBar.SetText(fmt.Sprintf("Connection issues detected on %d account(s) — reconnecting...", unhealthyAccounts))
		})
	} else if totalAccounts > 0 {
		mw.logger.Debug("Health check completed: all %d accounts have healthy connections", totalAccounts)
	}
}

// restartAccountMonitoring restarts monitoring for a specific account
func (mw *MainWindow) restartAccountMonitoring(accountName string, client *imap.ClientWrapper) {
	// Stop monitoring first
	client.StopMonitoring()

	// Wait a moment for cleanup
	time.Sleep(1 * time.Second)

	// Restart monitoring
	if err := client.StartMonitoring("INBOX"); err != nil {
		mw.logger.Error("Failed to restart monitoring for account %s: %v", accountName, err)
	} else {
		mw.logger.Info("Successfully restarted monitoring for account %s", accountName)
	}
}

// isClientConnectionHealthy checks if an IMAP client connection is healthy
func (mw *MainWindow) isClientConnectionHealthy(client *imap.ClientWrapper, accountName string) bool {
	if client == nil {
		mw.logger.Debug("Account %s: client is nil", accountName)
		return false
	}

	// Check basic connection status first
	if !client.IsConnected() {
		mw.logger.Debug("Account %s: not connected", accountName)
		return false
	}

	// Perform actual health check by sending a command to the worker
	healthStatus := client.GetHealthStatus()
	if healthStatus == nil {
		mw.logger.Debug("Account %s: health status unavailable", accountName)
		return false
	}

	// Check the connection state
	state, ok := healthStatus["state"].(string)
	if !ok {
		mw.logger.Debug("Account %s: invalid health status format", accountName)
		return false
	}

	// Check if the connection is actually healthy (not just connected)
	connectionHealthy, hasHealthCheck := healthStatus["connection_healthy"].(bool)
	if hasHealthCheck && !connectionHealthy {
		mw.logger.Debug("Account %s: connection health check failed (state: %s)", accountName, state)
		return false
	}

	// Consider connection healthy if it's connected and not in a failed state
	isHealthy := (state == "Connected" || state == "Connecting") && (!hasHealthCheck || connectionHealthy)

	if !isHealthy {
		mw.logger.Debug("Account %s: connection state is %s, healthy=%v (unhealthy)", accountName, state, connectionHealthy)
		return false
	}

	mw.logger.Debug("Account %s: connection is healthy (state: %s, health_check: %v)", accountName, state, connectionHealthy)
	return true
}

// stopUnifiedInboxMonitoring stops monitoring for all accounts in unified inbox mode
func (mw *MainWindow) stopUnifiedInboxMonitoring() {
	mw.logger.Info("Stopping unified inbox monitoring for all accounts")
	mw.stopUnifiedInboxHealthMonitoring()

	mw.accountController.ForEachClient(func(accountName string, client *imap.ClientWrapper) {
		if client.IsMonitoring() {
			folder := client.GetMonitoredFolder()
			mw.logger.Info("Stopping monitoring for account %s, folder: %s", accountName, folder)
			client.StopMonitoring()
		}
	})
}

// stopUnifiedInboxMonitoringFast stops monitoring for all accounts without graceful cleanup
func (mw *MainWindow) stopUnifiedInboxMonitoringFast() {
	mw.logger.Debug("Force stopping unified inbox monitoring for all accounts")
	mw.stopUnifiedInboxHealthMonitoring()

	mw.accountController.ForEachClient(func(accountName string, client *imap.ClientWrapper) {
		if client.IsMonitoring() {
			folder := client.GetMonitoredFolder()
			mw.logger.Debug("Stopping monitoring for account %s, folder: %s", accountName, folder)
			client.StopMonitoring()
		}
	})
}
