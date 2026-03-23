package controllers

import (
	"context"
	"fmt"
	"sync"

	"github.com/wltechblog/gommail/internal/config"
	"github.com/wltechblog/gommail/internal/email"
	"github.com/wltechblog/gommail/internal/logging"
	"github.com/wltechblog/gommail/pkg/cache"
	"github.com/wltechblog/gommail/pkg/imap"
)

// AccountControllerImpl implements AccountController
type AccountControllerImpl struct {
	currentAccount *config.Account
	isUnifiedInbox bool
	stateMu        sync.RWMutex // protects currentAccount and isUnifiedInbox

	// IMAP client management
	accountClients map[string]*imap.ClientWrapper
	clientsMutex   sync.RWMutex

	// Unified inbox monitoring
	unifiedInboxCtx    context.Context
	unifiedInboxCancel context.CancelFunc
	monitoringMutex    sync.RWMutex

	// Dependencies
	config config.ConfigManager
	cache  *cache.Cache
	logger *logging.Logger
}

// NewAccountController creates a new AccountController
func NewAccountController(config config.ConfigManager, cache *cache.Cache) *AccountControllerImpl {
	return &AccountControllerImpl{
		accountClients: make(map[string]*imap.ClientWrapper),
		config:         config,
		cache:          cache,
		logger:         logging.NewComponent("account-controller"),
	}
}

// GetCurrentAccount returns the currently selected account
func (ac *AccountControllerImpl) GetCurrentAccount() *config.Account {
	ac.stateMu.RLock()
	defer ac.stateMu.RUnlock()
	return ac.currentAccount
}

// SetCurrentAccount sets the currently selected account
func (ac *AccountControllerImpl) SetCurrentAccount(account *config.Account) {
	ac.stateMu.Lock()
	defer ac.stateMu.Unlock()
	ac.currentAccount = account
	ac.logger.Debug("Current account set to: %s", account.Name)
}

// ClearCurrentAccount clears the currently selected account
func (ac *AccountControllerImpl) ClearCurrentAccount() {
	ac.stateMu.Lock()
	defer ac.stateMu.Unlock()
	ac.currentAccount = nil
	ac.logger.Debug("Current account cleared")
}

// IsUnifiedInbox returns whether unified inbox mode is enabled
func (ac *AccountControllerImpl) IsUnifiedInbox() bool {
	ac.stateMu.RLock()
	defer ac.stateMu.RUnlock()
	return ac.isUnifiedInbox
}

// SetUnifiedInbox sets the unified inbox mode
func (ac *AccountControllerImpl) SetUnifiedInbox(enabled bool) {
	ac.stateMu.Lock()
	defer ac.stateMu.Unlock()
	ac.isUnifiedInbox = enabled
	ac.logger.Debug("Unified inbox mode set to: %v", enabled)
}

// GetOrCreateIMAPClient gets or creates an IMAP client for the specified account
// using a default factory that creates a basic (non-traced, unconnected) client.
func (ac *AccountControllerImpl) GetOrCreateIMAPClient(account *config.Account) (*imap.ClientWrapper, error) {
	return ac.GetOrCreateIMAPClientWithFactory(account, func(acc *config.Account) (*imap.ClientWrapper, error) {
		serverConfig := email.ServerConfig{
			Host:     acc.IMAP.Host,
			Port:     acc.IMAP.Port,
			Username: acc.IMAP.Username,
			Password: acc.IMAP.Password,
			TLS:      acc.IMAP.TLS,
		}
		accountKey := fmt.Sprintf("account_%s", acc.Name)
		client := imap.NewClientWrapperWithCache(serverConfig, ac.cache, accountKey)
		return client, nil
	})
}

// GetOrCreateIMAPClientWithFactory atomically gets an existing connected client
// or creates a new one using the provided factory function. The factory is called
// under the clients lock to prevent duplicate clients for the same account.
func (ac *AccountControllerImpl) GetOrCreateIMAPClientWithFactory(account *config.Account, factory func(*config.Account) (*imap.ClientWrapper, error)) (*imap.ClientWrapper, error) {
	ac.clientsMutex.Lock()
	defer ac.clientsMutex.Unlock()

	// Check if we already have a connected client for this account
	if client, exists := ac.accountClients[account.Name]; exists {
		if client.IsConnected() {
			ac.logger.Debug("Reusing existing IMAP client for account: %s", account.Name)
			return client, nil
		}
		// Client exists but is disconnected, clean it up
		ac.logger.Debug("Existing IMAP client for account %s is disconnected, creating new one", account.Name)
		if err := client.Disconnect(); err != nil {
			ac.logger.Debug("Error disconnecting stale client for %s: %v", account.Name, err)
		}
		client.Stop()
		delete(ac.accountClients, account.Name)
	}

	// Create new client via factory
	ac.logger.Info("Creating new IMAP client for account: %s", account.Name)
	client, err := factory(account)
	if err != nil {
		return nil, err
	}

	// Store the client
	ac.accountClients[account.Name] = client
	ac.logger.Info("IMAP client created and stored for account: %s", account.Name)
	return client, nil
}

// GetIMAPClientForAccount returns the IMAP client for a specific account
func (ac *AccountControllerImpl) GetIMAPClientForAccount(accountName string) (*imap.ClientWrapper, bool) {
	ac.clientsMutex.RLock()
	defer ac.clientsMutex.RUnlock()

	client, exists := ac.accountClients[accountName]
	return client, exists
}

// StoreIMAPClient stores an IMAP client for a specific account
func (ac *AccountControllerImpl) StoreIMAPClient(accountName string, client *imap.ClientWrapper) {
	ac.clientsMutex.Lock()
	defer ac.clientsMutex.Unlock()

	ac.accountClients[accountName] = client
	ac.logger.Debug("Stored IMAP client for account: %s", accountName)
}

// ForEachClient executes a function for each IMAP client.
// A snapshot of the clients map is taken before iterating so the callback
// can safely call methods that acquire clientsMutex without deadlocking.
func (ac *AccountControllerImpl) ForEachClient(fn func(accountName string, client *imap.ClientWrapper)) {
	// Take a snapshot under the read lock
	ac.clientsMutex.RLock()
	snapshot := make(map[string]*imap.ClientWrapper, len(ac.accountClients))
	for name, client := range ac.accountClients {
		if client != nil {
			snapshot[name] = client
		}
	}
	ac.clientsMutex.RUnlock()

	// Iterate without holding the lock
	for accountName, client := range snapshot {
		fn(accountName, client)
	}
}

// CloseAllClients closes all IMAP clients
func (ac *AccountControllerImpl) CloseAllClients() {
	ac.clientsMutex.Lock()
	defer ac.clientsMutex.Unlock()

	ac.logger.Info("Closing all IMAP clients (%d total)", len(ac.accountClients))

	for accountName, client := range ac.accountClients {
		ac.logger.Debug("Closing IMAP client for account: %s", accountName)
		if err := client.Disconnect(); err != nil {
			ac.logger.Warn("Error disconnecting IMAP client for account %s: %v", accountName, err)
		}
		client.Stop()
	}

	// Clear the map
	ac.accountClients = make(map[string]*imap.ClientWrapper)
	ac.logger.Info("All IMAP clients closed")
}

// CloseClientForAccount closes the IMAP client for a specific account
func (ac *AccountControllerImpl) CloseClientForAccount(accountName string) {
	ac.clientsMutex.Lock()
	defer ac.clientsMutex.Unlock()

	if client, exists := ac.accountClients[accountName]; exists {
		ac.logger.Debug("Closing IMAP client for account: %s", accountName)
		if err := client.Disconnect(); err != nil {
			ac.logger.Warn("Error disconnecting IMAP client for account %s: %v", accountName, err)
		}
		client.Stop()
		delete(ac.accountClients, accountName)
	}
}

// StartUnifiedInboxMonitoring starts monitoring for all accounts in unified inbox mode
func (ac *AccountControllerImpl) StartUnifiedInboxMonitoring(ctx context.Context) {
	ac.monitoringMutex.Lock()
	defer ac.monitoringMutex.Unlock()

	// Stop any existing monitoring
	if ac.unifiedInboxCancel != nil {
		ac.logger.Debug("Stopping existing unified inbox monitoring before starting new one")
		ac.unifiedInboxCancel()
	}

	// Create new context for monitoring
	ac.unifiedInboxCtx, ac.unifiedInboxCancel = context.WithCancel(ctx)
	ac.logger.Info("Started unified inbox monitoring")
}

// StopUnifiedInboxMonitoring stops monitoring for unified inbox
func (ac *AccountControllerImpl) StopUnifiedInboxMonitoring() {
	ac.monitoringMutex.Lock()
	defer ac.monitoringMutex.Unlock()

	if ac.unifiedInboxCancel != nil {
		ac.logger.Info("Stopping unified inbox monitoring")
		ac.unifiedInboxCancel()
		ac.unifiedInboxCancel = nil
		ac.unifiedInboxCtx = nil
	}
}

// IsMonitoringUnifiedInbox returns whether unified inbox monitoring is active
func (ac *AccountControllerImpl) IsMonitoringUnifiedInbox() bool {
	ac.monitoringMutex.RLock()
	defer ac.monitoringMutex.RUnlock()

	return ac.unifiedInboxCtx != nil && ac.unifiedInboxCancel != nil
}

// InvalidateUnifiedInboxCache clears cached data for all accounts to force fresh loading
func (ac *AccountControllerImpl) InvalidateUnifiedInboxCache() {
	if ac.cache == nil {
		return
	}

	ac.logger.Debug("Invalidating unified inbox cache for all accounts")

	accounts := ac.config.GetAccounts()
	for _, account := range accounts {
		// Invalidate message cache for each account's INBOX
		cacheKey := fmt.Sprintf("account_%s:messages:INBOX", account.Name)
		if err := ac.cache.Delete(cacheKey); err != nil {
			ac.logger.Warn("Failed to invalidate cache for account %s: %v", account.Name, err)
		}

		// Invalidate folder cache
		folderCacheKey := fmt.Sprintf("account_%s:folders", account.Name)
		if err := ac.cache.Delete(folderCacheKey); err != nil {
			ac.logger.Warn("Failed to invalidate folder cache for account %s: %v", account.Name, err)
		}
	}

	ac.logger.Info("Unified inbox cache invalidated for all accounts")
}

// CacheAccountMessages caches messages for a specific account
func (ac *AccountControllerImpl) CacheAccountMessages(accountName string, messages []email.Message) {
	if ac.cache == nil {
		return
	}

	// Implementation will be added when integrating with MainWindow
	ac.logger.Debug("Caching %d messages for account: %s", len(messages), accountName)
}
