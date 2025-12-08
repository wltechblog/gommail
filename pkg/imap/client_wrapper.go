package imap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wltechblog/gommail/internal/email"
	"github.com/wltechblog/gommail/internal/logging"
	"github.com/wltechblog/gommail/pkg/cache"
)

// ClientWrapper wraps the IMAPWorker to provide API compatibility with the existing imap.Client
// This allows seamless integration with existing code while using the new worker-based architecture
type ClientWrapper struct {
	// Core components
	worker     *IMAPWorker
	config     email.ServerConfig
	cache      *cache.Cache
	accountKey string
	logger     *logging.Logger

	// State management
	connected int32 // atomic: 0=false, 1=true

	// Callback protection (callbacks can be set/called concurrently)
	callbackMu sync.RWMutex

	// Monitoring callbacks (for compatibility)
	onUpdate     func(string)
	onError      func(error)
	onNewMessage func(string, []email.Message)

	// Command timeout
	defaultTimeout time.Duration
}

// NewClientWrapper creates a new client wrapper that implements the email.IMAPClient interface
func NewClientWrapper(config email.ServerConfig) *ClientWrapper {
	return NewClientWrapperWithCache(config, nil, "")
}

// NewClientWrapperWithCache creates a new client wrapper with caching support
func NewClientWrapperWithCache(config email.ServerConfig, cache *cache.Cache, accountKey string) *ClientWrapper {
	return NewClientWrapperWithCacheAndTracer(config, cache, accountKey, nil)
}

// NewClientWrapperWithCacheAndTracer creates a new client wrapper with caching and tracing support
func NewClientWrapperWithCacheAndTracer(config email.ServerConfig, cache *cache.Cache, accountKey string, tracer io.Writer) *ClientWrapper {
	wrapper := &ClientWrapper{
		config:         config,
		cache:          cache,
		accountKey:     accountKey,
		logger:         logging.NewComponent(fmt.Sprintf("imap-wrapper-%s", config.Username)),
		defaultTimeout: 30 * time.Second,
	}

	// Create the worker with tracer
	wrapper.worker = NewIMAPWorkerWithTracer(&config, config.Username, accountKey, cache, tracer)

	// Set up callbacks
	wrapper.worker.SetConnectionStateCallback(wrapper.handleConnectionStateChange)
	wrapper.worker.SetNewMessageCallback(wrapper.handleNewMessage)

	return wrapper
}

// Start starts the underlying worker
func (w *ClientWrapper) Start() error {
	return w.worker.Start()
}

// Stop stops the underlying worker
func (w *ClientWrapper) Stop() {
	if err := w.worker.Stop(); err != nil {
		w.logger.Warn("Error stopping worker: %v", err)
	}
}

// StopFast stops the underlying worker without graceful cleanup
func (w *ClientWrapper) StopFast() error {
	return w.worker.StopFast()
}

// Connect establishes a connection to the IMAP server
func (w *ClientWrapper) Connect() error {
	w.logger.Debug("Connecting to IMAP server")

	cmd := NewCommand(CmdConnect, nil)
	response, err := w.sendCommandWithTimeout(cmd, w.defaultTimeout)
	if err != nil {
		return wrapCommandError("connect", err)
	}

	if err := wrapResponseError("connect", response); err != nil {
		return err
	}

	atomic.StoreInt32(&w.connected, 1)

	w.logger.Debug("Successfully connected to IMAP server")
	return nil
}

// Disconnect closes the connection to the IMAP server
func (w *ClientWrapper) Disconnect() error {
	w.logger.Debug("Disconnecting from IMAP server")

	cmd := NewCommand(CmdDisconnect, nil)
	response, err := w.sendCommandWithTimeout(cmd, w.defaultTimeout)
	if err != nil {
		return wrapCommandError("disconnect", err)
	}

	if !response.Success {
		return fmt.Errorf("disconnect failed: %w", response.Error)
	}

	atomic.StoreInt32(&w.connected, 0)

	w.logger.Debug("Successfully disconnected from IMAP server")
	return nil
}

// ForceReconnect forces a reconnection to the IMAP server
func (w *ClientWrapper) ForceReconnect() error {
	w.logger.Debug("Force reconnecting to IMAP server")

	cmd := NewCommand(CmdReconnect, nil)
	response, err := w.sendCommandWithTimeout(cmd, w.defaultTimeout)
	if err != nil {
		return wrapCommandError("reconnect", err)
	}

	if !response.Success {
		return fmt.Errorf("reconnect failed: %w", response.Error)
	}

	atomic.StoreInt32(&w.connected, 1)

	w.logger.Debug("Successfully reconnected to IMAP server")
	return nil
}

// IsConnected returns whether the client is currently connected
func (w *ClientWrapper) IsConnected() bool {
	return atomic.LoadInt32(&w.connected) == 1
}

// ListFolders returns a list of all folders
func (w *ClientWrapper) ListFolders() ([]email.Folder, error) {
	cmd := NewCommand(CmdListFolders, nil)
	response, err := w.sendCommandWithTimeout(cmd, w.defaultTimeout)
	if err != nil {
		return nil, wrapCommandError("list folders", err)
	}

	if !response.Success {
		return nil, fmt.Errorf("list folders failed: %w", response.Error)
	}

	folders, ok := response.GetFolders(DataFolders)
	if !ok {
		return nil, fmt.Errorf("invalid response data for folders")
	}

	return folders, nil
}

// ListSubscribedFolders returns a list of subscribed folders
func (w *ClientWrapper) ListSubscribedFolders() ([]email.Folder, error) {
	cmd := NewCommand(CmdListSubscribedFolders, nil)
	response, err := w.sendCommandWithTimeout(cmd, w.defaultTimeout)
	if err != nil {
		return nil, wrapCommandError("list subscribed folders", err)
	}

	if !response.Success {
		return nil, fmt.Errorf("list subscribed folders failed: %w", response.Error)
	}

	folders, ok := response.GetFolders(DataFolders)
	if !ok {
		return nil, fmt.Errorf("invalid response data for subscribed folders")
	}

	return folders, nil
}

// ListSubscribedFoldersFresh returns a fresh list of subscribed folders (bypassing cache)
func (w *ClientWrapper) ListSubscribedFoldersFresh() ([]email.Folder, error) {
	// For now, this is the same as ListSubscribedFolders since the worker handles caching
	return w.ListSubscribedFolders()
}

// ForceRefreshSubscribedFolders forces a refresh of subscribed folders
func (w *ClientWrapper) ForceRefreshSubscribedFolders() ([]email.Folder, error) {
	// For now, this is the same as ListSubscribedFolders since the worker handles caching
	return w.ListSubscribedFolders()
}

// ListAllFolders returns a list of all folders (subscribed and unsubscribed)
func (w *ClientWrapper) ListAllFolders() ([]email.Folder, error) {
	return w.ListFolders()
}

// CreateFolder creates a new folder
func (w *ClientWrapper) CreateFolder(name string) error {
	cmd := NewCommand(CmdCreateFolder, map[string]interface{}{
		ParamFolderName: name,
	})
	response, err := w.sendCommandWithTimeout(cmd, w.defaultTimeout)
	if err != nil {
		return wrapCommandError("create folder", err)
	}

	if !response.Success {
		return fmt.Errorf("create folder failed: %w", response.Error)
	}

	return nil
}

// DeleteFolder deletes a folder
func (w *ClientWrapper) DeleteFolder(name string) error {
	cmd := NewCommand(CmdDeleteFolder, map[string]interface{}{
		ParamFolderName: name,
	})
	response, err := w.sendCommandWithTimeout(cmd, w.defaultTimeout)
	if err != nil {
		return wrapCommandError("delete folder", err)
	}

	if !response.Success {
		return fmt.Errorf("delete folder failed: %w", response.Error)
	}

	return nil
}

// SubscribeFolder subscribes to a folder
func (w *ClientWrapper) SubscribeFolder(name string) error {
	cmd := NewCommand(CmdSubscribeFolder, map[string]interface{}{
		ParamFolderName: name,
	})
	response, err := w.sendCommandWithTimeout(cmd, w.defaultTimeout)
	if err != nil {
		return wrapCommandError("subscribe folder", err)
	}

	if !response.Success {
		return fmt.Errorf("subscribe folder failed: %w", response.Error)
	}

	return nil
}

// UnsubscribeFolder unsubscribes from a folder
func (w *ClientWrapper) UnsubscribeFolder(name string) error {
	cmd := NewCommand(CmdUnsubscribeFolder, map[string]interface{}{
		ParamFolderName: name,
	})
	response, err := w.sendCommandWithTimeout(cmd, w.defaultTimeout)
	if err != nil {
		return wrapCommandError("unsubscribe folder", err)
	}

	if !response.Success {
		return fmt.Errorf("unsubscribe folder failed: %w", response.Error)
	}

	return nil
}

// sendCommandWithTimeout sends a command to the worker with a timeout
func (w *ClientWrapper) sendCommandWithTimeout(cmd *IMAPCommand, timeout time.Duration) (*IMAPResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd.Context = ctx
	cmd.ResponseCh = make(chan *IMAPResponse, 1)

	return w.worker.SendCommand(cmd)
}

// handleConnectionStateChange handles connection state changes from the worker
func (w *ClientWrapper) handleConnectionStateChange(event ConnectionEvent) {
	if event.State == ConnectionStateConnected {
		atomic.StoreInt32(&w.connected, 1)
	} else {
		atomic.StoreInt32(&w.connected, 0)
	}

	w.logger.Debug("Connection state changed: %s", event.State.String())
}

// handleNewMessage handles new message events from the worker
func (w *ClientWrapper) handleNewMessage(event NewMessageEvent) {
	w.callbackMu.RLock()
	callback := w.onNewMessage
	w.callbackMu.RUnlock()

	if callback != nil {
		callback(event.Folder, event.Messages)
	}
}

// SubscribeToDefaultFolders subscribes to default folders (INBOX, Sent, Drafts, Trash)
func (w *ClientWrapper) SubscribeToDefaultFolders(sentFolder, trashFolder string) error {
	// Subscribe to INBOX (always exists)
	if err := w.SubscribeFolder("INBOX"); err != nil {
		w.logger.Warn("Failed to subscribe to INBOX: %v", err)
	}

	// Subscribe to sent folder if specified
	if sentFolder != "" {
		if err := w.SubscribeFolder(sentFolder); err != nil {
			w.logger.Warn("Failed to subscribe to sent folder %s: %v", sentFolder, err)
		}
	}

	// Subscribe to trash folder if specified
	if trashFolder != "" {
		if err := w.SubscribeFolder(trashFolder); err != nil {
			w.logger.Warn("Failed to subscribe to trash folder %s: %v", trashFolder, err)
		}
	}

	// Try to subscribe to common folder names
	commonFolders := []string{"Drafts", "Draft", "Sent", "Sent Items", "Trash", "Deleted Items"}
	for _, folder := range commonFolders {
		if err := w.SubscribeFolder(folder); err != nil {
			w.logger.Debug("Could not subscribe to folder %s: %v", folder, err)
		}
	}

	return nil
}

// StoreSentMessage stores a sent message in the specified folder
func (w *ClientWrapper) StoreSentMessage(sentFolder string, messageContent []byte) error {
	cmd := NewCommand(CmdStoreSentMessage, map[string]interface{}{
		ParamFolderName:     sentFolder,
		ParamMessageContent: messageContent,
	})
	response, err := w.sendCommandWithTimeout(cmd, w.defaultTimeout)
	if err != nil {
		return wrapCommandError("store sent message", err)
	}

	if !response.Success {
		return fmt.Errorf("store sent message failed: %w", response.Error)
	}

	return nil
}

// SelectFolder selects a folder for operations
func (w *ClientWrapper) SelectFolder(name string) error {
	cmd := NewCommand(CmdSelectFolder, map[string]interface{}{
		ParamFolderName: name,
	})
	response, err := w.sendCommandWithTimeout(cmd, w.defaultTimeout)
	if err != nil {
		return wrapCommandError("select folder", err)
	}

	if !response.Success {
		return fmt.Errorf("select folder failed: %w", response.Error)
	}

	return nil
}

// FetchMessages fetches messages from a folder with optional limit
func (w *ClientWrapper) FetchMessages(folder string, limit int) ([]email.Message, error) {
	// Try cache first
	if cachedMessages, found, err := w.GetCachedMessages(folder); err == nil && found {
		w.logger.Debug("Retrieved %d messages from cache for folder %s", len(cachedMessages), folder)
		// Apply limit to cached messages if specified
		if limit > 0 && len(cachedMessages) > limit {
			// Return the most recent messages (assuming they're already sorted by date)
			return cachedMessages[len(cachedMessages)-limit:], nil
		}
		return cachedMessages, nil
	}

	// Fetch from server
	params := map[string]interface{}{
		ParamFolderName: folder,
	}
	if limit > 0 {
		params[ParamLimit] = limit
	}

	cmd := NewCommand(CmdFetchMessages, params)
	response, err := w.sendCommandWithTimeout(cmd, w.defaultTimeout)
	if err != nil {
		return nil, wrapCommandError("fetch messages", err)
	}

	if !response.Success {
		return nil, fmt.Errorf("fetch messages failed: %w", response.Error)
	}

	messages, ok := response.GetMessages(DataMessages)
	if !ok {
		return nil, fmt.Errorf("invalid response data for messages")
	}

	// Cache the fetched messages
	w.cacheMessages(folder, messages)

	return messages, nil
}

// FetchMessage fetches a single message by UID
func (w *ClientWrapper) FetchMessage(folder string, uid uint32) (*email.Message, error) {
	cmd := NewCommand(CmdFetchMessage, map[string]interface{}{
		ParamFolderName: folder,
		ParamUID:        uid,
	})
	// Use longer timeout for message fetching to handle large messages
	fetchTimeout := 2 * time.Minute
	response, err := w.sendCommandWithTimeout(cmd, fetchTimeout)
	if err != nil {
		return nil, wrapCommandError("fetch message", err)
	}

	if !response.Success {
		return nil, fmt.Errorf("fetch message failed: %w", response.Error)
	}

	message, ok := response.GetMessage(DataMessage)
	if !ok {
		return nil, fmt.Errorf("invalid response data for message")
	}

	return &message, nil
}

// MarkAsRead marks a message as read
func (w *ClientWrapper) MarkAsRead(folder string, uid uint32) error {
	cmd := NewCommand(CmdMarkAsRead, map[string]interface{}{
		ParamFolderName: folder,
		ParamUID:        uid,
	})
	response, err := w.sendCommandWithTimeout(cmd, w.defaultTimeout)
	if err != nil {
		return wrapCommandError("mark as read", err)
	}

	if !response.Success {
		return fmt.Errorf("mark as read failed: %w", response.Error)
	}

	return nil
}

// MarkAsUnread marks a message as unread
func (w *ClientWrapper) MarkAsUnread(folder string, uid uint32) error {
	cmd := NewCommand(CmdMarkAsUnread, map[string]interface{}{
		ParamFolderName: folder,
		ParamUID:        uid,
	})
	response, err := w.sendCommandWithTimeout(cmd, w.defaultTimeout)
	if err != nil {
		return wrapCommandError("mark as unread", err)
	}

	if !response.Success {
		return fmt.Errorf("mark as unread failed: %w", response.Error)
	}

	return nil
}

// DeleteMessage deletes a message
func (w *ClientWrapper) DeleteMessage(folder string, uid uint32) error {
	cmd := NewCommand(CmdDeleteMessage, map[string]interface{}{
		ParamFolderName: folder,
		ParamUID:        uid,
	})
	response, err := w.sendCommandWithTimeout(cmd, w.defaultTimeout)
	if err != nil {
		return wrapCommandError("delete message", err)
	}

	if !response.Success {
		return fmt.Errorf("delete message failed: %w", response.Error)
	}

	return nil
}

// MoveMessage moves a message to another folder
func (w *ClientWrapper) MoveMessage(folder string, uid uint32, targetFolder string) error {
	cmd := NewCommand(CmdMoveMessage, map[string]interface{}{
		ParamFolderName:   folder,
		ParamUID:          uid,
		ParamTargetFolder: targetFolder,
	})
	response, err := w.sendCommandWithTimeout(cmd, w.defaultTimeout)
	if err != nil {
		return wrapCommandError("move message", err)
	}

	if !response.Success {
		return fmt.Errorf("move message failed: %w", response.Error)
	}

	return nil
}

// SearchMessages searches for messages across all folders using the given criteria
func (w *ClientWrapper) SearchMessages(criteria email.SearchCriteria) ([]email.Message, error) {
	cmd := NewCommand(CmdSearchMessages, map[string]interface{}{
		ParamCriteria: &criteria,
	})
	response, err := w.sendCommandWithTimeout(cmd, w.getSearchTimeout(criteria, false))
	if err != nil {
		return nil, wrapCommandError("search messages", err)
	}

	if !response.Success {
		return nil, fmt.Errorf("search messages failed: %w", response.Error)
	}

	messages, ok := response.GetMessages(DataMessages)
	if !ok {
		return nil, fmt.Errorf("invalid response data for search results")
	}

	return messages, nil
}

// SearchMessagesInFolder searches for messages in a specific folder using the given criteria
func (w *ClientWrapper) SearchMessagesInFolder(folder string, criteria email.SearchCriteria) ([]email.Message, error) {
	cmd := NewCommand(CmdSearchMessages, map[string]interface{}{
		ParamFolderName: folder,
		ParamCriteria:   &criteria,
	})
	response, err := w.sendCommandWithTimeout(cmd, w.getSearchTimeout(criteria, true))
	if err != nil {
		return nil, wrapCommandError("search messages in folder", err)
	}

	if !response.Success {
		return nil, fmt.Errorf("search messages in folder failed: %w", response.Error)
	}

	messages, ok := response.GetMessages(DataMessages)
	if !ok {
		return nil, fmt.Errorf("invalid response data for search results")
	}

	return messages, nil
}

func (w *ClientWrapper) getSearchTimeout(criteria email.SearchCriteria, folderSpecified bool) time.Duration {
	timeout := w.defaultTimeout

	if criteria.SearchServer || !folderSpecified {
		if timeout < 2*time.Minute {
			timeout = 2 * time.Minute
		}
	}

	if criteria.MaxResults == 0 || criteria.MaxResults > 200 {
		if timeout < 3*time.Minute {
			timeout = 3 * time.Minute
		}
	}

	return timeout
}

// SearchCachedMessages searches for messages in the cache using the given criteria
func (w *ClientWrapper) SearchCachedMessages(criteria email.SearchCriteria) ([]email.Message, error) {
	// For now, delegate to regular search since the worker handles caching
	return w.SearchMessages(criteria)
}

// FetchFreshMessages retrieves messages directly from the server, bypassing cache
func (w *ClientWrapper) FetchFreshMessages(folder string, limit int) ([]email.Message, error) {
	params := map[string]interface{}{
		ParamFolderName:  folder,
		ParamBypassCache: true, // Force bypass cache
	}
	if limit > 0 {
		params[ParamLimit] = limit
	}

	cmd := NewCommand(CmdFetchMessages, params)
	response, err := w.sendCommandWithTimeout(cmd, w.defaultTimeout)
	if err != nil {
		return nil, wrapCommandError("fetch fresh messages", err)
	}

	if !response.Success {
		return nil, fmt.Errorf("fetch fresh messages failed: %w", response.Error)
	}

	messages, ok := response.GetMessages(DataMessages)
	if !ok {
		return nil, fmt.Errorf("invalid response data for fresh messages")
	}

	return messages, nil
}

// FetchMessageWithFullHeaders retrieves a specific message by UID from server with all headers, bypassing cache
func (w *ClientWrapper) FetchMessageWithFullHeaders(folder string, uid uint32) (*email.Message, error) {
	cmd := NewCommand(CmdFetchMessage, map[string]interface{}{
		ParamFolderName:  folder,
		ParamUID:         uid,
		ParamBypassCache: true, // Force bypass cache
		ParamFullHeaders: true, // Request full headers
	})
	// Use longer timeout for message fetching with full headers
	fetchTimeout := 2 * time.Minute
	response, err := w.sendCommandWithTimeout(cmd, fetchTimeout)
	if err != nil {
		return nil, wrapCommandError("fetch message with full headers", err)
	}

	if !response.Success {
		return nil, fmt.Errorf("fetch message with full headers failed: %w", response.Error)
	}

	message, ok := response.GetMessage(DataMessage)
	if !ok {
		return nil, fmt.Errorf("invalid response data for message with full headers")
	}

	return &message, nil
}

// GetCachedMessages retrieves messages from cache (public method)
func (w *ClientWrapper) GetCachedMessages(folder string) ([]email.Message, bool, error) {
	if w.cache == nil {
		return nil, false, nil // No cache available
	}

	key := w.getCacheKey("messages", folder)
	data, found, err := w.cache.Get(key)
	if err != nil || !found {
		return nil, false, err
	}

	var messages []email.Message
	err = json.Unmarshal(data, &messages)
	if err != nil {
		return nil, false, fmt.Errorf("failed to unmarshal cached messages: %w", err)
	}

	w.logger.Debug("Retrieved %d cached messages from folder: %s", len(messages), folder)
	return messages, true, nil
}

// Monitoring methods for compatibility with existing code

// StartMonitoring starts IDLE monitoring for the specified folder
func (w *ClientWrapper) StartMonitoring(folder string) error {
	cmd := NewStartIDLECommand(folder)
	response, err := w.sendCommandWithTimeout(cmd, w.defaultTimeout)
	if err != nil {
		return wrapCommandError("start monitoring", err)
	}

	if !response.Success {
		return fmt.Errorf("start monitoring failed: %w", response.Error)
	}

	return nil
}

// StopMonitoring stops IDLE monitoring
func (w *ClientWrapper) StopMonitoring() {
	cmd := NewStopIDLECommand()
	_, err := w.sendCommandWithTimeout(cmd, w.defaultTimeout)
	if err != nil {
		w.logger.Warn("Failed to stop monitoring: %v", err)
	}
}

// StopMonitoringFast stops IDLE monitoring without graceful cleanup
func (w *ClientWrapper) StopMonitoringFast() {
	w.StopMonitoring() // Same as regular stop for now
}

// IsMonitoring returns whether IDLE monitoring is currently active
func (w *ClientWrapper) IsMonitoring() bool {
	return w.worker.IsIDLEActive()
}

// IsMonitoringPaused returns whether IDLE monitoring is currently paused
func (w *ClientWrapper) IsMonitoringPaused() bool {
	return w.worker.IsIDLEPaused()
}

// GetMonitoredFolder returns the folder currently being monitored
func (w *ClientWrapper) GetMonitoredFolder() string {
	return w.worker.GetIDLEFolder()
}

// GetMonitoringMode returns the current monitoring mode (always IDLE for worker-based implementation)
func (w *ClientWrapper) GetMonitoringMode() email.MonitorMode {
	// Worker-based implementation always uses IDLE when available
	return email.MonitorModeIdle
}

// CleanupUnsubscribedFolderCache cleans up cache data for unsubscribed folders
func (w *ClientWrapper) CleanupUnsubscribedFolderCache() error {
	// Delegate to worker
	cmd := NewCommand(CmdCleanupUnsubscribedFolderCache, nil)
	response, err := w.sendCommandWithTimeout(cmd, w.defaultTimeout)
	if err != nil {
		return err
	}
	return response.Error
}

// InvalidateFolderCache invalidates the folder cache
func (w *ClientWrapper) InvalidateFolderCache() {
	// Send command to worker to invalidate folder cache
	cmd := NewCommand(CmdInvalidateFolderCache, nil)
	w.sendCommandWithTimeout(cmd, w.defaultTimeout) // Fire and forget
}

// InvalidateSubscribedFolderCache invalidates the subscribed folder cache
func (w *ClientWrapper) InvalidateSubscribedFolderCache() {
	// Send command to worker to invalidate subscribed folder cache
	cmd := NewCommand(CmdInvalidateSubscribedFolderCache, nil)
	w.sendCommandWithTimeout(cmd, w.defaultTimeout) // Fire and forget
}

// InvalidateMessageCache invalidates the message cache for a specific folder
func (w *ClientWrapper) InvalidateMessageCache(folder string) {
	// Send command to worker to invalidate message cache
	cmd := NewCommand(CmdInvalidateMessageCache, map[string]interface{}{
		ParamFolderName: folder,
	})
	w.sendCommandWithTimeout(cmd, w.defaultTimeout) // Fire and forget
}

// ForceDisconnect forces disconnection without graceful cleanup
func (w *ClientWrapper) ForceDisconnect() {
	// Send command to worker to force disconnect
	cmd := NewCommand(CmdForceDisconnect, nil)
	w.sendCommandWithTimeout(cmd, w.defaultTimeout) // Fire and forget
}

// RefreshFoldersInBackground starts background folder refresh
func (w *ClientWrapper) RefreshFoldersInBackground() {
	// Send command to worker to refresh folders in background
	cmd := NewCommand(CmdRefreshFoldersInBackground, nil)
	w.sendCommandWithTimeout(cmd, w.defaultTimeout) // Fire and forget
}

// PauseMonitoring temporarily pauses IDLE monitoring
func (w *ClientWrapper) PauseMonitoring() {
	cmd := NewPauseIDLECommand()
	_, err := w.sendCommandWithTimeout(cmd, w.defaultTimeout)
	if err != nil {
		w.logger.Warn("Failed to pause monitoring: %v", err)
	}
}

// ResumeMonitoring resumes IDLE monitoring after a pause
func (w *ClientWrapper) ResumeMonitoring() {
	cmd := NewResumeIDLECommand()
	_, err := w.sendCommandWithTimeout(cmd, w.defaultTimeout)
	if err != nil {
		w.logger.Warn("Failed to resume monitoring: %v", err)
	}
}

// SetMonitorCallbacks sets the monitoring callbacks for compatibility
func (w *ClientWrapper) SetMonitorCallbacks(onUpdate func(string), onError func(error)) {
	w.callbackMu.Lock()
	defer w.callbackMu.Unlock()
	w.onUpdate = onUpdate
	w.onError = onError
}

// SetNewMessageCallback sets the new message callback
func (w *ClientWrapper) SetNewMessageCallback(onNewMessage func(string, []email.Message)) {
	w.callbackMu.Lock()
	defer w.callbackMu.Unlock()
	w.onNewMessage = onNewMessage
}

// MarkInitialSyncComplete marks the initial sync as complete for a folder
func (w *ClientWrapper) MarkInitialSyncComplete(folder string) {
	w.worker.MarkInitialSyncComplete(folder)
}

// Additional utility methods for compatibility

// GetConnectionState returns the current connection state
func (w *ClientWrapper) GetConnectionState() ConnectionState {
	return w.worker.GetState()
}

// SetConnectionStateCallback sets the connection state change callback
func (w *ClientWrapper) SetConnectionStateCallback(callback func(email.ConnectionEvent)) {
	// Convert from imap.ConnectionEvent to email.ConnectionEvent
	w.worker.SetConnectionStateCallback(func(event ConnectionEvent) {
		emailEvent := email.ConnectionEvent{
			State:   email.ConnectionState(event.State),
			Error:   event.Error,
			Attempt: event.Attempt,
		}
		callback(emailEvent)
	})
}

// GetHealthStatus returns detailed health status information
func (w *ClientWrapper) GetHealthStatus() map[string]interface{} {
	return w.worker.GetHealthStatus()
}

// SetHealthCheckInterval configures the health check interval
func (w *ClientWrapper) SetHealthCheckInterval(interval time.Duration) {
	w.worker.SetHealthCheckInterval(interval)
}

// SetReconnectConfig configures reconnection parameters
func (w *ClientWrapper) SetReconnectConfig(delay, maxDelay time.Duration, maxAttempts int) {
	w.worker.SetReconnectConfig(delay, maxDelay, maxAttempts)
}

// getCacheKey generates a cache key for the given type and identifier
// Deprecated: Use cache.GenerateAccountKey instead
func (w *ClientWrapper) getCacheKey(keyType, identifier string) string {
	return cache.GenerateAccountKey(w.accountKey, keyType, identifier)
}

// cacheMessages stores messages in cache
func (w *ClientWrapper) cacheMessages(folder string, messages []email.Message) error {
	if w.cache == nil {
		return nil // No cache available
	}

	data, err := json.Marshal(messages)
	if err != nil {
		return fmt.Errorf("failed to marshal messages: %w", err)
	}

	key := w.getCacheKey("messages", folder)
	return w.cache.Set(key, data, 24*time.Hour) // Cache messages for 24 hours
}
