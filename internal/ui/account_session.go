package ui

// Code in this file was mechanically split from main_window.go.

import (
	"fmt"
	_ "image/gif"  // Register GIF format
	_ "image/jpeg" // Register JPEG format
	_ "image/png"  // Register PNG format
	"io"
	"strings"
	"time"

	"fyne.io/fyne/v2"

	"github.com/wltechblog/gommail/internal/config"
	"github.com/wltechblog/gommail/internal/email"
	"github.com/wltechblog/gommail/internal/trace"
	"github.com/wltechblog/gommail/pkg/imap"
	"github.com/wltechblog/gommail/pkg/smtp"
)

// clearAccountState clears the current account state when switching accounts
func (mw *MainWindow) clearAccountState() {
	// Clear current message list and folder selection when switching accounts
	// This ensures the UI immediately reflects the account switch
	mw.messagesMu.Lock()
	mw.messages = []email.MessageIndexItem{}
	mw.selectedMessage = nil
	mw.messagesMu.Unlock()

	// Clear folder list and current folder to prevent showing folders from previous account
	mw.folderController.SetFolders([]email.Folder{})
	mw.folderController.SelectFolder("") // Clear current folder

	// Clear unified inbox state
	mw.accountController.SetUnifiedInbox(false)
}

// selectAccount handles account selection and connects to the email server
func (mw *MainWindow) selectAccount(account *config.Account) {
	mw.logger.Info("Selecting account: %s (%s)", account.Name, account.Email)
	oldClient := mw.imapClient

	mw.accountController.SetCurrentAccount(account)
	mw.statusBar.SetText(fmt.Sprintf("Connecting to %s...", account.Name))

	// Stop monitoring when switching accounts
	mw.stopFolderMonitoring()
	mw.stopUnifiedInboxMonitoring()

	// Clear account state
	mw.clearAccountState()

	// Refresh UI components to show the cleared state
	mw.clearMessageSelection()
	mw.refreshMessageList()
	mw.updateMessageViewer("")
	if folderList := mw.folderController.GetFolderList(); folderList != nil {
		folderList.UnselectAll()
	}

	// Cancel any active read timer when switching accounts
	mw.cancelReadTimer()

	newClient, needsConnect, err := mw.prepareIMAPClientForAccountSelection(account)
	if err != nil {
		mw.logger.Error("Failed to prepare IMAP client for account %s: %v", account.Name, err)
		mw.statusBar.SetText(fmt.Sprintf("Failed to start IMAP worker for %s", account.Name))
		return
	}

	// Clean up the previous single-account client if it differs from the selected account client.
	// This avoids leaking extra workers while still allowing reuse of an already-managed client.
	if oldClient != nil && oldClient != newClient {
		mw.logger.Debug("Cleaning up previous IMAP client before switching to account: %s", account.Name)
		if err := oldClient.Disconnect(); err != nil {
			mw.logger.Warn("Failed to disconnect previous IMAP client while switching accounts: %v", err)
		}
		oldClient.Stop()
		if mw.imapClient == oldClient {
			mw.imapClient = nil
		}
	}

	mw.imapClient = newClient

	// Create SMTP client
	mw.logger.Debug("Creating SMTP client for %s (host: %s:%d, TLS: %v)",
		account.Name, account.SMTP.Host, account.SMTP.Port, account.SMTP.TLS)
	smtpConfig := email.ServerConfig{
		Host:     account.SMTP.Host,
		Port:     account.SMTP.Port,
		Username: account.SMTP.Username,
		Password: account.SMTP.Password,
		TLS:      account.SMTP.TLS,
	}
	mw.smtpClient = smtp.NewClient(smtpConfig)

	// IMMEDIATELY load cached folders for instant UI responsiveness
	// Access cache directly to avoid waiting for worker connection
	cachedFolders, cachedFoldersFound := mw.loadCachedFoldersDirectly(account.Name)
	if cachedFoldersFound {
		sortedFolders := mw.folderController.SortFolders(cachedFolders)
		mw.folderController.SetFolders(sortedFolders)
		mw.statusBar.SetText(fmt.Sprintf("Loaded %d folders from cache - connecting...", len(cachedFolders)))
		mw.logger.Debug("Immediately loaded %d cached folders for account %s", len(cachedFolders), account.Name)

		// Auto-select INBOX if available and auto-selection hasn't been done
		if !mw.autoSelectionDone {
			if mw.folderController.AutoSelectInbox() {
				mw.autoSelectionDone = true
			} else if mw.folderController.AutoSelectFirstFolder() {
				mw.autoSelectionDone = true
			}
		}
	} else {
		mw.logger.Debug("No cached folders available for account %s, will load from server", account.Name)
		mw.statusBar.SetText(fmt.Sprintf("Connecting to %s...", account.Name))
	}

	// If a connected managed client already exists for this account, reuse it instead of
	// spawning a duplicate worker-backed client.
	if !needsConnect {
		mw.logger.Info("Reusing existing connected IMAP client for account: %s", account.Name)
		newClient.SetConnectionStateCallback(func(event email.ConnectionEvent) {
			mw.handleConnectionStateChange(account.Name, event)
		})

		if cachedFoldersFound {
			mw.statusBar.SetText(fmt.Sprintf("Connected to %s - using cached folders", account.Name))
		} else {
			mw.statusBar.SetText(fmt.Sprintf("Connected to %s - syncing folder subscriptions", account.Name))
			mw.syncFolderSubscriptionsInBackground(account.Name, newClient)
		}
		return
	}

	mw.setCurrentAccountConnectInProgress(true)

	// Now connect and optionally sync folder subscriptions in background
	go func(client *imap.ClientWrapper, selectedAccount *config.Account, usedCachedFolders bool) {
		defer mw.setCurrentAccountConnectInProgress(false)

		// Set up connection state callback before connecting
		client.SetConnectionStateCallback(func(event email.ConnectionEvent) {
			mw.handleConnectionStateChange(selectedAccount.Name, event)
		})

		// Now connect to server and refresh data in background
		err := client.Connect()
		if err != nil {
			fyne.Do(func() {
				mw.statusBar.SetText(fmt.Sprintf("Failed to connect to %s: %v", selectedAccount.Name, err))
			})
			return
		}

		// Clean up cached data for unsubscribed folders on startup
		// This ensures the cache stays clean even if previous cleanup operations were missed
		mw.logger.Debug("Performing startup cleanup of unsubscribed folder cache for account: %s", selectedAccount.Name)
		if cleanupErr := client.CleanupUnsubscribedFolderCache(); cleanupErr != nil {
			mw.logger.Warn("Failed to cleanup unsubscribed folder cache for %s: %v", selectedAccount.Name, cleanupErr)
			// Don't fail the connection for cleanup errors, just log them
		} else {
			mw.logger.Debug("Successfully cleaned up unsubscribed folder cache for account: %s", selectedAccount.Name)
		}

		// Subscribe to default folders (INBOX, Sent, Drafts, Trash) for new accounts
		// This is done after connection but before folder refresh to ensure subscriptions are in place
		mw.logger.Debug("Attempting to subscribe to default folders for account: %s", selectedAccount.Name)
		if err := client.SubscribeToDefaultFolders(selectedAccount.SentFolder, selectedAccount.TrashFolder); err != nil {
			mw.logger.Warn("Failed to subscribe to some default folders for %s: %v", selectedAccount.Name, err)
			// Don't fail the connection for subscription errors, just log them
		} else {
			mw.logger.Info("Successfully subscribed to default folders for account: %s", selectedAccount.Name)
		}

		if usedCachedFolders {
			fyne.Do(func() {
				mw.statusBar.SetText(fmt.Sprintf("Connected to %s - using cached folders", selectedAccount.Name))
			})
			return
		}

		// Sync folder subscriptions in background and update UI if changes are detected
		mw.syncFolderSubscriptionsInBackground(selectedAccount.Name, client)

		fyne.Do(func() {
			mw.statusBar.SetText(fmt.Sprintf("Connected to %s - syncing folder subscriptions", selectedAccount.Name))
		})
	}(newClient, account, cachedFoldersFound)
}

func (mw *MainWindow) prepareIMAPClientForAccountSelection(account *config.Account) (*imap.ClientWrapper, bool, error) {
	if client, exists := mw.accountController.GetIMAPClientForAccount(account.Name); exists && client != nil {
		if client.IsConnected() {
			mw.logger.Debug("Reusing existing managed IMAP client for selected account: %s", account.Name)
			return client, false, nil
		}

		mw.logger.Debug("Managed IMAP client for account %s is disconnected, recreating it", account.Name)
		mw.accountController.CloseClientForAccount(account.Name)
	}

	mw.logger.Debug("Creating worker-based IMAP client for %s (host: %s:%d, TLS: %v)",
		account.Name, account.IMAP.Host, account.IMAP.Port, account.IMAP.TLS)
	imapConfig := email.ServerConfig{
		Host:     account.IMAP.Host,
		Port:     account.IMAP.Port,
		Username: account.IMAP.Username,
		Password: account.IMAP.Password,
		TLS:      account.IMAP.TLS,
	}

	var tracer io.Writer
	tracingConfig := mw.config.GetTracing()
	if tracingConfig.IMAP.Enabled {
		imapTracer := trace.NewIMAPTracer(true, tracingConfig.IMAP.Directory)
		tracer = imapTracer.GetAccountTracer(account.Name)
	}

	client := imap.NewClientWrapperWithCacheAndTracer(imapConfig, mw.cache, accountCacheKey(account.Name), tracer)
	if err := client.Start(); err != nil {
		return nil, false, fmt.Errorf("failed to start IMAP worker for %s: %w", account.Name, err)
	}

	mw.accountController.StoreIMAPClient(account.Name, client)
	return client, true, nil
}

// syncFolderSubscriptionsInBackground syncs folder subscriptions with server and updates UI if changes detected
func (mw *MainWindow) syncFolderSubscriptionsInBackground(accountName string, client *imap.ClientWrapper) {
	go func() {
		mw.logger.Debug("Starting background folder subscription sync for account: %s", accountName)

		// Get current cached folders for comparison
		currentFolders := mw.folderController.GetFolders()
		currentFoldersCopy := make([]email.Folder, len(currentFolders))
		copy(currentFoldersCopy, currentFolders)

		// Start background refresh of folders (this will update cache with subscribed folders)
		client.RefreshFoldersInBackground()

		// Wait a moment for background refresh to complete
		time.Sleep(2 * time.Second)

		// Get updated subscribed folders from cache
		updatedFolders, err := client.ListSubscribedFolders()
		if err != nil {
			mw.logger.Warn("Failed to get updated folders after sync: %v", err)
			return
		}

		// Check if folder list has changed
		if mw.folderController.FoldersChanged(currentFoldersCopy, updatedFolders) {
			mw.logger.Debug("Folder subscriptions changed, updating UI")
			fyne.Do(func() {
				sortedFolders := mw.folderController.SortFolders(updatedFolders)
				mw.folderController.SetFolders(sortedFolders)
				mw.statusBar.SetText(fmt.Sprintf("Connected to %s - %d folders synced", accountName, len(updatedFolders)))
			})
		} else {
			mw.logger.Debug("No folder subscription changes detected")
			fyne.Do(func() {
				mw.statusBar.SetText(fmt.Sprintf("Connected to %s - %d folders", accountName, len(updatedFolders)))
			})
		}
	}()
}

// getOrCreateIMAPClient gets or creates an IMAP client for the specified account.
// The check-and-create is routed through AccountController.GetOrCreateIMAPClientWithFactory
// to ensure atomicity (no duplicate clients for the same account).
func (mw *MainWindow) getOrCreateIMAPClient(account *config.Account) (*imap.ClientWrapper, error) {
	return mw.accountController.GetOrCreateIMAPClientWithFactory(account, func(acc *config.Account) (*imap.ClientWrapper, error) {
		mw.logger.Debug("Creating new IMAP client for account: %s (%s:%d)", acc.Name, acc.IMAP.Host, acc.IMAP.Port)

		serverConfig := email.ServerConfig{
			Host:     acc.IMAP.Host,
			Port:     acc.IMAP.Port,
			Username: acc.IMAP.Username,
			Password: acc.IMAP.Password,
			TLS:      acc.IMAP.TLS,
		}

		// Get tracing configuration and create tracer if enabled
		var tracer io.Writer
		tracingConfig := mw.config.GetTracing()
		if tracingConfig.IMAP.Enabled {
			imapTracer := trace.NewIMAPTracer(true, tracingConfig.IMAP.Directory)
			tracer = imapTracer.GetAccountTracer(acc.Name)
		}

		client := imap.NewClientWrapperWithCacheAndTracer(serverConfig, mw.cache, accountCacheKey(acc.Name), tracer)

		if err := client.Start(); err != nil {
			return nil, fmt.Errorf("failed to start IMAP worker for %s: %w", acc.Name, err)
		}

		// Set up connection state callback (types are aliases, no conversion needed)
		client.SetConnectionStateCallback(func(event email.ConnectionEvent) {
			mw.handleConnectionStateChange(acc.Name, event)
		})

		if err := client.Connect(); err != nil {
			client.Stop()
			return nil, fmt.Errorf("failed to connect to IMAP server for %s: %w", acc.Name, err)
		}

		mw.logger.Debug("Successfully connected IMAP client for account: %s", acc.Name)
		return client, nil
	})
}

// handleConnectionStateChange handles IMAP connection state changes
func (mw *MainWindow) handleConnectionStateChange(accountName string, event imap.ConnectionEvent) {
	mw.logger.Debug("Connection state changed for account %s: %s", accountName, event.State.String())

	// Update status bar based on connection state
	fyne.Do(func() {
		switch event.State {
		case imap.ConnectionStateConnecting:
			mw.statusBar.SetText(fmt.Sprintf("Connecting to %s...", accountName))
		case imap.ConnectionStateConnected:
			mw.statusBar.SetText(fmt.Sprintf("Connected to %s", accountName))
		case imap.ConnectionStateReconnecting:
			if event.Attempt > 1 {
				mw.statusBar.SetText(fmt.Sprintf("Reconnecting to %s (attempt %d)...", accountName, event.Attempt))
			} else {
				mw.statusBar.SetText(fmt.Sprintf("Reconnecting to %s...", accountName))
			}
		case imap.ConnectionStateDisconnected:
			mw.statusBar.SetText(fmt.Sprintf("Disconnected from %s", accountName))
		case imap.ConnectionStateFailed:
			if event.Error != nil {
				mw.statusBar.SetText(fmt.Sprintf("Connection failed for %s: %v", accountName, event.Error))
			} else {
				mw.statusBar.SetText(fmt.Sprintf("Connection failed for %s", accountName))
			}
		}
	})

	// Log connection errors for debugging
	if event.Error != nil {
		mw.logger.Warn("Connection error for account %s: %v", accountName, event.Error)

		// Check if this is a connection error that should trigger reconnection
		if mw.isConnectionError(event.Error) {
			mw.logger.Info("Connection error detected for account %s, monitoring will handle reconnection", accountName)
		}
	}

	// Handle successful reconnection
	if event.State == imap.ConnectionStateConnected && event.Attempt > 1 {
		mw.logger.Info("Successfully reconnected to account %s after %d attempts", accountName, event.Attempt)

		// Refresh data after reconnection
		go func() {
			// Give the connection a moment to stabilize
			time.Sleep(500 * time.Millisecond)

			// Refresh folder list and current folder if we're viewing this account
			if mw.accountController.GetCurrentAccount() != nil && mw.accountController.GetCurrentAccount().Name == accountName {
				mw.logger.Debug("Refreshing data after reconnection for account %s", accountName)

				// Refresh folders in background
				if mw.imapClient != nil {
					mw.imapClient.RefreshFoldersInBackground()
				}

				// Refresh current folder if one is selected
				if currentFolder := mw.folderController.GetCurrentFolder(); currentFolder != "" {
					fyne.Do(func() {
						mw.selectFolderWithOptions(currentFolder, true)
					})
				}
			}
		}()
	}
}

// isConnectionError checks if an error indicates a connection problem
func (mw *MainWindow) isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "use of closed network connection") ||
		strings.Contains(errStr, "connection reset by peer") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "EOF") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "network is unreachable") ||
		strings.Contains(errStr, "i/o timeout") ||
		strings.Contains(errStr, "connection timed out")
}

// getAccountForMessage returns the account and SMTP client for a specific message (used by timer)
func (mw *MainWindow) getAccountForMessage(msg *email.Message) (*config.Account, *smtp.Client, error) {
	if !mw.accountController.IsUnifiedInbox() {
		// Regular mode - use current account
		if mw.accountController.GetCurrentAccount() == nil {
			return nil, nil, fmt.Errorf("no account selected")
		}
		return mw.accountController.GetCurrentAccount(), mw.smtpClient, nil
	}

	// Unified inbox mode - find the account for the specific message.
	// Called from the read-timer goroutine, so the message list must be
	// read under its mutex.
	var accountName string
	mw.messagesMu.RLock()
	for _, indexItem := range mw.messages {
		if indexItem.Message.UID == msg.UID && indexItem.Message.Subject == msg.Subject {
			accountName = indexItem.AccountName
			break
		}
	}
	mw.messagesMu.RUnlock()

	if accountName == "" {
		return nil, nil, fmt.Errorf("could not find account for message")
	}

	// Find the account by name
	account := mw.getAccountByName(accountName)
	if account != nil {
		return account, mw.smtpClientForAccount(account), nil
	}

	return nil, nil, fmt.Errorf("account %s not found", accountName)
}

// imapClientForAccount returns a connected IMAP client for the given account,
// or nil if none is available. It performs no network I/O.
func (mw *MainWindow) imapClientForAccount(account *config.Account) email.IMAPClient {
	if account == nil {
		return nil
	}
	if client, exists := mw.accountController.GetIMAPClientForAccount(account.Name); exists && client != nil && client.IsConnected() {
		return client
	}
	return nil
}

// getAccountByName returns the account with the given name.
// Returns nil if the account is not found.
func (mw *MainWindow) getAccountByName(accountName string) *config.Account {
	accounts := mw.config.GetAccounts()
	for i, acc := range accounts {
		if acc.Name == accountName {
			return &accounts[i]
		}
	}
	return nil
}

// smtpClientForAccount builds an SMTP client for the account. ClientWrapper
// items carry their own client, but a typed-nil *smtp.Client inside the
// SMTPClient interface (e.g. items built by search) survives an unchecked
// assertion and panics later; building fresh from the account config is
// always safe - the client is stateless apart from its config.
func (mw *MainWindow) smtpClientForAccount(account *config.Account) *smtp.Client {
	if account == nil {
		return nil
	}
	return smtp.NewClient(email.ServerConfig{
		Host:     account.SMTP.Host,
		Port:     account.SMTP.Port,
		Username: account.SMTP.Username,
		Password: account.SMTP.Password,
		TLS:      account.SMTP.TLS,
	})
}

func (mw *MainWindow) setCurrentAccountConnectInProgress(inProgress bool) {
	mw.connectionLifecycleMu.Lock()
	defer mw.connectionLifecycleMu.Unlock()
	mw.currentAccountConnectInProgress = inProgress
}

func (mw *MainWindow) isCurrentAccountConnectInProgress() bool {
	mw.connectionLifecycleMu.Lock()
	defer mw.connectionLifecycleMu.Unlock()
	return mw.currentAccountConnectInProgress
}

func (mw *MainWindow) beginCurrentAccountReconnect(accountName string) bool {
	mw.connectionLifecycleMu.Lock()
	defer mw.connectionLifecycleMu.Unlock()

	if mw.currentAccountConnectInProgress {
		mw.logger.Debug("Skipping reconnect for current account %s because account connection is already in progress", accountName)
		return false
	}

	if mw.reconnectInProgress {
		mw.logger.Debug("Reconnect already in progress for current account %s, skipping duplicate attempt", accountName)
		return false
	}

	mw.reconnectInProgress = true
	return true
}

func (mw *MainWindow) endCurrentAccountReconnect() {
	mw.connectionLifecycleMu.Lock()
	defer mw.connectionLifecycleMu.Unlock()
	mw.reconnectInProgress = false
}

// reconnectCurrentAccount attempts to reconnect the current account
func (mw *MainWindow) reconnectCurrentAccount() {
	account := mw.accountController.GetCurrentAccount()
	if account == nil {
		return
	}

	accountName := account.Name
	if !mw.beginCurrentAccountReconnect(accountName) {
		return
	}
	defer mw.endCurrentAccountReconnect()

	mw.logger.Info("Reconnecting current account: %s", accountName)

	// Update status bar
	fyne.Do(func() {
		mw.statusBar.SetText(fmt.Sprintf("Reconnecting to %s...", accountName))
	})

	// Force the cached per-account client to be recreated so we don't accidentally
	// reuse a stale connection and then disconnect the same client we just selected.
	mw.accountController.CloseClientForAccount(accountName)

	// Create new IMAP client
	newClient, err := mw.getOrCreateIMAPClient(account)
	if err != nil {
		mw.logger.Error("Failed to reconnect account %s: %v", accountName, err)
		fyne.Do(func() {
			mw.statusBar.SetText(fmt.Sprintf("Failed to reconnect to %s: %v", accountName, err))
		})
		return
	}

	// Replace the old client
	oldClient := mw.imapClient
	mw.imapClient = newClient

	// Clean up old client
	if oldClient != nil && oldClient != newClient {
		if err := oldClient.Disconnect(); err != nil {
			mw.logger.Warn("Failed to disconnect old IMAP client during reconnect for %s: %v", accountName, err)
		}
		oldClient.Stop()
	}

	// Update status bar
	fyne.Do(func() {
		mw.statusBar.SetText(fmt.Sprintf("Reconnected to %s", accountName))
	})

	// Refresh the current folder
	currentFolder := mw.folderController.GetCurrentFolder()
	if currentFolder != "" {
		mw.logger.Info("Refreshing folder %s after reconnection", currentFolder)
		fyne.Do(func() {
			mw.selectFolderWithOptions(currentFolder, true)
		})
	}

	mw.logger.Info("Successfully reconnected current account: %s", accountName)
}
