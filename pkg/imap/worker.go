package imap

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/wltechblog/gommail/internal/email"
	"github.com/wltechblog/gommail/internal/logging"
	"github.com/wltechblog/gommail/pkg/cache"
)

// IMAPWorker represents a dedicated goroutine that handles all IMAP operations for a single account
// This eliminates mutex contention by ensuring only one goroutine accesses the IMAP connection
type IMAPWorker struct {
	// Configuration
	config      *email.ServerConfig
	accountName string
	cacheKey    string
	cache       *cache.Cache // Cache instance for storing/retrieving data

	// IMAP connections (only accessed by worker goroutine)
	commandClient *imapclient.Client // Dedicated to regular commands
	idleClient    *imapclient.Client // Dedicated to IDLE monitoring

	// Communication channels
	commandCh chan *IMAPCommand
	stopCh    chan struct{}

	// Worker state (protected by mutex for external access)
	mu             sync.RWMutex
	state          ConnectionState
	selectedFolder string
	idleActive     bool
	idlePaused     bool
	idleFolder     string
	idleTimeout    time.Duration
	idleCmd        *imapclient.IdleCommand
	idleCancel     context.CancelFunc
	lastActivity   time.Time
	commandsQueued int
	reconnectCount int
	errorCount     int

	// IDLE monitoring state
	lastMessageCount map[string]uint32
	lastHighestUID   map[string]uint32
	initialSyncDone  map[string]bool
	lastIDLEActivity time.Time    // Track last time IDLE loop was confirmed running
	idleUpdateCh     chan struct{} // Signalled by UnilateralDataHandler when server pushes a mailbox update

	// Reconnection tracking to prevent notification spam
	lastReconnectTime    time.Time
	reconnectGracePeriod time.Duration

	// Health monitoring
	healthCheckInterval  time.Duration
	lastHealthCheck      time.Time
	reconnectDelay       time.Duration
	maxReconnectDelay    time.Duration
	maxReconnectAttempts int
	reconnectAttempt     int
	reconnecting         bool // Flag to prevent concurrent reconnection attempts
	healthTicker         *time.Ticker

	// Event callbacks
	onConnectionStateChange func(ConnectionEvent)
	onNewMessage            func(NewMessageEvent)

	// Tracing
	tracer io.Writer // IMAP protocol tracer (nil if tracing disabled)

	// Worker lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Logging
	logger *logging.Logger
}

// Worker configuration defaults
const (
	// DefaultCommandChBuffer is the buffer size for the command channel.
	DefaultCommandChBuffer = 100
	// DefaultIDLETimeout is the IDLE timeout (RFC 2177 recommends < 30 min).
	DefaultIDLETimeout = 29 * time.Minute
	// DefaultReconnectGracePeriod is how long to wait after reconnection before
	// considering the connection unhealthy again.
	DefaultReconnectGracePeriod = 30 * time.Second
	// DefaultHealthCheckInterval is the interval between health check ticks.
	DefaultHealthCheckInterval = 30 * time.Second
	// DefaultReconnectDelay is the initial delay before a reconnection attempt.
	DefaultReconnectDelay = 1 * time.Second
	// MaxReconnectDelay caps the exponential back-off for reconnection.
	MaxReconnectDelay = 30 * time.Second
	// DefaultMaxReconnectAttempts is kept for compatibility but not enforced.
	DefaultMaxReconnectAttempts = 10
	// MaxNewMessageNotifications caps the number of notifications per check.
	MaxNewMessageNotifications = 10
	// ReconnectionTimeout is the per-attempt timeout for automatic reconnection.
	ReconnectionTimeout = 30 * time.Second
)

// NewIMAPWorker creates a new IMAP worker for the specified account
func NewIMAPWorker(config *email.ServerConfig, accountName, cacheKey string, cache *cache.Cache) *IMAPWorker {
	worker := &IMAPWorker{
		config:      config,
		accountName: accountName,
		cacheKey:    cacheKey,
		cache:       cache,
		commandCh:   make(chan *IMAPCommand, DefaultCommandChBuffer),
		idleUpdateCh: make(chan struct{}, 1),
		// stopCh and ctx will be created in Start()
		state:                ConnectionStateDisconnected,
		idleTimeout:          DefaultIDLETimeout,
		lastMessageCount:     make(map[string]uint32),
		lastHighestUID:       make(map[string]uint32),
		initialSyncDone:      make(map[string]bool),
		reconnectGracePeriod: DefaultReconnectGracePeriod,
		healthCheckInterval:  DefaultHealthCheckInterval,
		reconnectDelay:       DefaultReconnectDelay,
		maxReconnectDelay:    MaxReconnectDelay,
		maxReconnectAttempts: DefaultMaxReconnectAttempts,
		logger:               logging.NewComponent(fmt.Sprintf("imap-worker-%s", accountName)),
		tracer:               nil, // Will be set by SetTracer if tracing is enabled
	}

	// Load persistent tracking state from cache
	worker.loadTrackingStateFromCache()

	return worker
}

// NewIMAPWorkerWithTracer creates a new IMAP worker with tracing support
func NewIMAPWorkerWithTracer(config *email.ServerConfig, accountName, cacheKey string, cache *cache.Cache, tracer io.Writer) *IMAPWorker {
	worker := NewIMAPWorker(config, accountName, cacheKey, cache)
	worker.tracer = tracer
	if tracer != nil {
		worker.logger.Info("IMAP protocol tracing enabled for account: %s", accountName)
	}
	return worker
}

// SetTracer sets the IMAP protocol tracer for this worker
func (w *IMAPWorker) SetTracer(tracer io.Writer) {
	w.tracer = tracer
	if tracer != nil {
		w.logger.Info("IMAP protocol tracing enabled for account: %s", w.accountName)
	} else {
		w.logger.Info("IMAP protocol tracing disabled for account: %s", w.accountName)
	}
}

// Start begins the worker goroutine and event loop
func (w *IMAPWorker) Start() error {
	// Check if already started by testing if stopCh is nil (not yet created)
	// or if context is already cancelled (already stopped)
	if w.stopCh != nil {
		select {
		case <-w.stopCh:
			// stopCh is closed, worker was already started and stopped
			return fmt.Errorf("worker already stopped, create a new worker instance")
		default:
			// stopCh exists and is not closed, worker is already running
			return fmt.Errorf("worker already running")
		}
	}

	w.logger.Debug("Starting IMAP worker for account: %s", w.accountName)

	// Initialize channels and context
	w.stopCh = make(chan struct{})
	w.ctx, w.cancel = context.WithCancel(context.Background())
	w.lastActivity = time.Now()

	// Start health check ticker
	w.mu.Lock()
	w.healthTicker = time.NewTicker(w.healthCheckInterval)
	w.mu.Unlock()

	// Start the main worker goroutine
	w.wg.Add(1)
	go w.eventLoop()

	return nil
}

// Stop gracefully stops the worker
func (w *IMAPWorker) Stop() error {
	// Check if already stopped by testing if stopCh is nil or already closed
	if w.stopCh == nil {
		return nil // Never started
	}

	select {
	case <-w.stopCh:
		return nil // Already stopped
	default:
		// Still running, proceed with stop
	}

	w.logger.Info("Stopping IMAP worker for account: %s", w.accountName)

	// Signal stop and wait for goroutine to finish
	close(w.stopCh)
	w.cancel()
	w.wg.Wait()

	// Close command channel
	close(w.commandCh)

	w.logger.Info("IMAP worker stopped for account: %s", w.accountName)
	return nil
}

// StopFast forcefully stops the worker without graceful cleanup
func (w *IMAPWorker) StopFast() error {
	// Check if already stopped by testing if stopCh is nil or already closed
	if w.stopCh == nil {
		return nil // Never started
	}

	select {
	case <-w.stopCh:
		return nil // Already stopped
	default:
		// Still running, proceed with fast stop
	}

	w.logger.Debug("Force stopping IMAP worker for account: %s", w.accountName)

	// Cancel context and close channels immediately
	w.cancel()
	close(w.stopCh)
	close(w.commandCh)

	// Don't wait for goroutine to finish in fast stop
	w.logger.Debug("IMAP worker force stopped for account: %s", w.accountName)
	return nil
}

// IsRunning returns whether the worker is currently running
func (w *IMAPWorker) IsRunning() bool {
	// Worker is running if stopCh exists and is not closed
	if w.stopCh == nil {
		return false // Never started
	}

	select {
	case <-w.stopCh:
		return false // Stopped
	default:
		return true // Still running
	}
}

// SendCommand sends a command to the worker and returns the response
func (w *IMAPWorker) SendCommand(cmd *IMAPCommand) (*IMAPResponse, error) {
	if !w.IsRunning() {
		return nil, fmt.Errorf("worker not running")
	}

	// Set a default timeout if context doesn't have one
	if cmd.Context == nil {
		// Use longer timeout for message operations, shorter for others
		timeout := 30 * time.Second
		if cmd.Type == CmdFetchMessage || cmd.Type == CmdFetchMessages {
			timeout = 2 * time.Minute
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		cmd.Context = ctx
	}

	// Send command to worker
	select {
	case w.commandCh <- cmd:
		// Command queued successfully
	case <-cmd.Context.Done():
		return nil, fmt.Errorf("command timeout while queuing")
	case <-w.ctx.Done():
		return nil, fmt.Errorf("worker shutting down")
	}

	// Wait for response
	select {
	case response := <-cmd.ResponseCh:
		return response, nil
	case <-cmd.Context.Done():
		return nil, fmt.Errorf("command timeout while waiting for response")
	case <-w.ctx.Done():
		return nil, fmt.Errorf("worker shutting down")
	}
}

// SendCommandAsync sends a command to the worker without waiting for response
func (w *IMAPWorker) SendCommandAsync(cmd *IMAPCommand) error {
	if !w.IsRunning() {
		return fmt.Errorf("worker not running")
	}

	select {
	case w.commandCh <- cmd:
		return nil
	case <-w.ctx.Done():
		return fmt.Errorf("worker shutting down")
	default:
		return fmt.Errorf("command queue full")
	}
}

// GetStatus returns the current worker status
func (w *IMAPWorker) GetStatus() WorkerStatus {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return WorkerStatus{
		State:          w.state,
		SelectedFolder: w.selectedFolder,
		IDLEActive:     w.idleActive,
		LastActivity:   w.lastActivity,
		CommandsQueued: len(w.commandCh),
		ReconnectCount: w.reconnectCount,
		ErrorCount:     w.errorCount,
	}
}

// SetConnectionStateCallback sets the callback for connection state changes
func (w *IMAPWorker) SetConnectionStateCallback(callback func(ConnectionEvent)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onConnectionStateChange = callback
}

// SetNewMessageCallback sets the callback for new message notifications
func (w *IMAPWorker) SetNewMessageCallback(callback func(NewMessageEvent)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onNewMessage = callback
}

// eventLoop is the main worker goroutine that processes commands
func (w *IMAPWorker) eventLoop() {
	defer w.wg.Done()
	w.logger.Debug("Worker event loop started for account: %s", w.accountName)

	// Main event loop
	for {
		select {
		case cmd := <-w.commandCh:
			if cmd == nil {
				// Channel closed
				w.logger.Debug("Command channel closed, exiting event loop")
				return
			}
			w.processCommand(cmd)

		case <-w.getHealthTicker():
			// Queue health check as a command to maintain serialization
			healthCmd := NewCommand(CmdHealthCheck, map[string]interface{}{})
			healthCmd.ResponseCh = make(chan *IMAPResponse, 1) // Create response channel
			select {
			case w.commandCh <- healthCmd:
				// Health check queued successfully
			default:
				// Command channel full, skip this health check
				w.logger.Debug("Skipping health check - command channel full")
			}

		case <-w.stopCh:
			w.logger.Debug("Stop signal received, exiting event loop")
			w.cleanup()
			return

		case <-w.ctx.Done():
			w.logger.Debug("Context cancelled, exiting event loop")
			w.cleanup()
			return
		}
	}
}

// processCommand processes a single command
func (w *IMAPWorker) processCommand(cmd *IMAPCommand) {
	w.logger.Debug("Processing command: %s (ID: %s)", cmd.Type, cmd.ID)

	// Update activity timestamp
	w.mu.Lock()
	w.lastActivity = time.Now()
	w.mu.Unlock()

	// Check if command context is cancelled
	select {
	case <-cmd.Context.Done():
		response := NewResponse(cmd.ID, false, nil, fmt.Errorf("command cancelled"))
		w.sendResponse(cmd, response)
		return
	default:
	}

	// Check connection state for commands that require a connection
	if w.requiresConnection(cmd.Type) {
		w.mu.RLock()
		state := w.state
		w.mu.RUnlock()

		if state == ConnectionStateReconnecting {
			response := NewResponse(cmd.ID, false, nil, fmt.Errorf("connection is reconnecting, please retry"))
			w.sendResponse(cmd, response)
			return
		}

		if state != ConnectionStateConnected || w.commandClient == nil {
			response := NewResponse(cmd.ID, false, nil, fmt.Errorf("not connected to server"))
			w.sendResponse(cmd, response)
			return
		}
	}

	// No need to pause IDLE - we have separate connections now

	// Process the command based on its type
	var response *IMAPResponse
	switch cmd.Type {
	case CmdConnect:
		response = w.handleConnect(cmd)
	case CmdDisconnect:
		response = w.handleDisconnect(cmd)
	case CmdReconnect:
		response = w.handleReconnect(cmd)
	case CmdSelectFolder:
		response = w.handleSelectFolder(cmd)
	case CmdListFolders:
		response = w.handleListFolders(cmd)
	case CmdListSubscribedFolders:
		response = w.handleListSubscribedFolders(cmd)
	case CmdCreateFolder:
		response = w.handleCreateFolder(cmd)
	case CmdDeleteFolder:
		response = w.handleDeleteFolder(cmd)
	case CmdSubscribeFolder:
		response = w.handleSubscribeFolder(cmd)
	case CmdUnsubscribeFolder:
		response = w.handleUnsubscribeFolder(cmd)
	case CmdFetchMessages:
		response = w.handleFetchMessages(cmd)
	case CmdFetchMessage:
		response = w.handleFetchMessage(cmd)
	case CmdGetCachedMessages:
		response = w.handleGetCachedMessages(cmd)
	case CmdMarkAsRead:
		response = w.handleMarkAsRead(cmd)
	case CmdMarkAsUnread:
		response = w.handleMarkAsUnread(cmd)
	case CmdDeleteMessage:
		response = w.handleDeleteMessage(cmd)
	case CmdMoveMessage:
		response = w.handleMoveMessage(cmd)
	case CmdSearchMessages:
		response = w.handleSearchMessages(cmd)
	case CmdStoreSentMessage:
		response = w.handleStoreSentMessage(cmd)
	case CmdStartIDLE:
		response = w.handleStartIDLE(cmd)
	case CmdStopIDLE:
		response = w.handleStopIDLE(cmd)
	case CmdPauseIDLE:
		response = w.handlePauseIDLE(cmd)
	case CmdResumeIDLE:
		response = w.handleResumeIDLE(cmd)
	case CmdHealthCheck:
		response = w.handleHealthCheck(cmd)
	case CmdGetStatus:
		response = w.handleGetStatus(cmd)
	case CmdRefreshFoldersInBackground:
		response = w.handleRefreshFoldersInBackground(cmd)
	case CmdCheckNewMessages:
		response = w.handleCheckNewMessages(cmd)
	default:
		// For now, return "not implemented" for other commands
		// These will be implemented in subsequent tasks
		response = NewResponse(cmd.ID, false, nil, fmt.Errorf("command %s not yet implemented", cmd.Type))
	}

	// Send response back
	w.sendResponse(cmd, response)
}

// requiresConnection checks if a command type requires an active connection
func (w *IMAPWorker) requiresConnection(cmdType CommandType) bool {
	switch cmdType {
	case CmdConnect, CmdDisconnect, CmdHealthCheck, CmdReconnect:
		return false // These commands can be processed without a connection
	default:
		return true // All other commands require a connection
	}
}

// isConnectionError checks if an error indicates a connection problem
func (w *IMAPWorker) isConnectionError(err error) bool {
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
		strings.Contains(errStr, "timed out") // Matches both "connection timed out" and "NOOP command timed out"
}

// performComprehensiveHealthCheck performs a thorough health check of the IMAP connection
func (w *IMAPWorker) performComprehensiveHealthCheck() (bool, error) {
	// commandClient can be set to nil concurrently (e.g. force disconnect / reconnect).
	// Capture a stable reference for this health check to avoid nil dereference.
	w.mu.RLock()
	client := w.commandClient
	w.mu.RUnlock()
	if client == nil {
		return false, fmt.Errorf("command client not available")
	}

	// Test 1: Basic NOOP command with timeout
	w.logger.Debug("Performing NOOP command for health check")

	// Create a context with timeout for the health check
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Channel to receive the result
	done := make(chan error, 1)

	// Perform NOOP in a goroutine with timeout
	go func(c *imapclient.Client) {
		// c may be closed, but should never be nil here. If it is, fail gracefully.
		if c == nil {
			done <- fmt.Errorf("command client not available")
			return
		}
		noopCmd := c.Noop()
		if noopCmd == nil {
			done <- fmt.Errorf("NOOP command returned nil")
			return
		}
		done <- noopCmd.Wait()
	}(client)

	// Wait for completion or timeout
	select {
	case err := <-done:
		if err != nil {
			w.logger.Debug("NOOP command failed: %v", err)
			return false, wrapCommandError("NOOP", err)
		}
		w.logger.Debug("NOOP command succeeded")
		// NOOP succeeded, now check IDLE health
		return w.checkIDLEHealth()

	case <-ctx.Done():
		w.logger.Debug("NOOP command timed out")
		return false, fmt.Errorf("NOOP command timed out after 10 seconds")
	}
}

// checkIDLEHealth verifies that IDLE monitoring is working correctly
func (w *IMAPWorker) checkIDLEHealth() (bool, error) {
	w.mu.RLock()
	idleActive := w.idleActive
	idlePaused := w.idlePaused
	idleFolder := w.idleFolder
	lastActivity := w.lastIDLEActivity
	w.mu.RUnlock()

	// If IDLE is supposed to be active and not paused, verify it's actually running
	if idleActive && !idlePaused {
		// Check if IDLE activity timestamp is too old (more than 2x the IDLE timeout)
		// This indicates the IDLE loop may have died without restarting
		maxIdleAge := w.idleTimeout * 2
		idleAge := time.Since(lastActivity)

		if idleAge > maxIdleAge {
			w.logger.Warn("IDLE monitoring appears stuck for folder %s: last activity was %v ago (max expected: %v)",
				idleFolder, idleAge, maxIdleAge)
			return false, fmt.Errorf("IDLE monitoring stuck: last activity %v ago", idleAge)
		}

		w.logger.Debug("IDLE health check passed: folder=%s, last activity=%v ago", idleFolder, idleAge)
	}

	return true, nil
}

// attemptAutomaticReconnection attempts to automatically reconnect when connection issues are detected
func (w *IMAPWorker) attemptAutomaticReconnection() {
	w.mu.Lock()

	// Check if a reconnection is already in progress
	if w.reconnecting {
		w.logger.Debug("Reconnection already in progress, skipping duplicate attempt")
		w.mu.Unlock()
		return
	}

	currentAttempt := w.reconnectAttempt
	delay := w.reconnectDelay
	maxDelay := w.maxReconnectDelay

	// Mark reconnection as in progress BEFORE releasing lock to prevent race condition
	w.reconnecting = true

	// Save IDLE state NOW before any other reconnection attempt can clear it
	// This must be done while holding the lock to prevent race conditions
	wasIDLEActive := w.idleActive
	idleFolder := w.idleFolder

	// Increment attempt counter
	w.reconnectAttempt++
	currentDelay := delay
	// Calculate exponential backoff delay
	for i := 0; i < w.reconnectAttempt-1; i++ {
		currentDelay *= 2
		if currentDelay > maxDelay {
			currentDelay = maxDelay
			break
		}
	}

	w.mu.Unlock()

	// Ensure we clear the reconnecting flag when done
	defer func() {
		w.mu.Lock()
		w.reconnecting = false
		w.mu.Unlock()
	}()

	w.logger.Info("Starting automatic reconnection attempt %d (will retry indefinitely)", currentAttempt+1)
	// Use INFO level so we can see this in production logs
	w.logger.Info("Saved IDLE state before reconnection: active=%v, folder=%s", wasIDLEActive, idleFolder)

	// Wait before attempting reconnection (context-aware so shutdown is prompt)
	w.logger.Debug("Waiting %v before reconnection attempt", currentDelay)
	select {
	case <-time.After(currentDelay):
	case <-w.ctx.Done():
		return
	}

	// Send reconnect command through the command channel so it runs on the
	// event loop goroutine, preserving the single-goroutine invariant.
	reconnectCmd := NewCommand(CmdReconnect, nil)
	reconnectCmd.ResponseCh = make(chan *IMAPResponse, 1)

	ctx, cancel := context.WithTimeout(context.Background(), ReconnectionTimeout)
	defer cancel()
	reconnectCmd.Context = ctx

	// Send through channel and wait for the event loop to process it
	var response *IMAPResponse
	select {
	case w.commandCh <- reconnectCmd:
		select {
		case response = <-reconnectCmd.ResponseCh:
			// got response
		case <-ctx.Done():
			w.logger.Error("Timeout waiting for reconnection response")
			return
		case <-w.ctx.Done():
			return
		}
	case <-w.ctx.Done():
		return
	}

	if response.Success {
		w.logger.Info("Automatic reconnection successful after %d attempts", w.reconnectAttempt)
		// Reset reconnect attempt counter on success
		w.mu.Lock()
		w.reconnectAttempt = 0
		w.mu.Unlock()

		// Determine which folder to start IDLE monitoring for
		var folderToMonitor string
		if wasIDLEActive && idleFolder != "" {
			// IDLE was active before reconnection - restart it for the same folder
			folderToMonitor = idleFolder
			w.logger.Info("Restarting IDLE monitoring for folder %s after reconnection (was active before)", idleFolder)
		} else {
			// IDLE was not active before reconnection (e.g., initial connection failed)
			// Start IDLE for INBOX by default to ensure we receive new message notifications
			folderToMonitor = "INBOX"
			w.logger.Info("Starting IDLE monitoring for INBOX after reconnection (was not active before, active=%v, folder=%s)", wasIDLEActive, idleFolder)
		}

		// Send IDLE start command through the command channel to avoid race conditions
		startIDLECmd := NewStartIDLECommand(folderToMonitor)
		startIDLECmd.ResponseCh = make(chan *IMAPResponse, 1)

		// Set a timeout for the IDLE start command
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		startIDLECmd.Context = ctx

		// Send command through channel
		select {
		case w.commandCh <- startIDLECmd:
			// Wait for response
			select {
			case response := <-startIDLECmd.ResponseCh:
				if !response.Success {
					w.logger.Error("Failed to start IDLE for %s after reconnection: %v", folderToMonitor, response.Error)
				} else {
					w.logger.Info("Successfully started IDLE monitoring for %s after reconnection", folderToMonitor)

					// Check for messages that arrived while disconnected
					// IDLE only notifies about changes that happen AFTER it starts monitoring
					w.logger.Info("Checking for messages that arrived while disconnected for folder: %s", folderToMonitor)
					w.sendCheckNewMessagesCommand(folderToMonitor)
				}
			case <-ctx.Done():
				w.logger.Error("Timeout waiting for IDLE start for %s after reconnection", folderToMonitor)
			}
		case <-time.After(5 * time.Second):
			w.logger.Error("Failed to queue IDLE start command for %s after reconnection (channel full)", folderToMonitor)
		}
	} else {
		w.logger.Warn("Automatic reconnection attempt %d failed: %v", w.reconnectAttempt, response.Error)

		// Always schedule another attempt - never give up on an account
		w.logger.Info("Scheduling next automatic reconnection attempt in %v", currentDelay*2)
		time.AfterFunc(currentDelay*2, func() {
			w.attemptAutomaticReconnection()
		})
	}
}

// sendResponse sends a response back to the command sender
func (w *IMAPWorker) sendResponse(cmd *IMAPCommand, response *IMAPResponse) {
	// Check if the response contains a connection error and trigger reconnection
	if !response.Success && response.Error != nil && w.isConnectionError(response.Error) {
		w.logger.Warn("Command %s failed with connection error: %v", cmd.Type, response.Error)

		// Don't trigger reconnection for certain commands to avoid loops or duplicates:
		// - connect/disconnect/reconnect: avoid reconnection loops
		// - health_check: the health check handler already triggers reconnection
		if cmd.Type != CmdConnect && cmd.Type != CmdDisconnect && cmd.Type != CmdReconnect && cmd.Type != CmdHealthCheck {
			w.logger.Info("Connection error detected in command response, attempting automatic reconnection")
			go w.attemptAutomaticReconnection()
		}
	}

	select {
	case cmd.ResponseCh <- response:
		// Response sent successfully
	case <-cmd.Context.Done():
		// Command context cancelled, don't send response
		w.logger.Debug("Command context cancelled, not sending response for %s", cmd.ID)
	case <-w.ctx.Done():
		// Worker shutting down
		w.logger.Debug("Worker shutting down, not sending response for %s", cmd.ID)
	}
}

// cleanup performs cleanup when the worker is stopping
func (w *IMAPWorker) cleanup() {
	w.logger.Debug("Performing worker cleanup for account: %s", w.accountName)

	// Stop IDLE monitoring if active (before closing clients)
	w.stopIDLEInternal()

	// Disconnect IMAP clients if connected
	if w.commandClient != nil {
		w.logger.Debug("Disconnecting command client during cleanup")
		if err := w.commandClient.Close(); err != nil {
			w.logger.Debug("Error closing command client during cleanup: %v", err)
		}
		w.commandClient = nil
	}
	if w.idleClient != nil {
		w.logger.Debug("Disconnecting IDLE client during cleanup")
		if err := w.idleClient.Close(); err != nil {
			w.logger.Debug("Error closing IDLE client during cleanup: %v", err)
		}
		w.idleClient = nil
	}

	// Stop health check ticker
	w.mu.Lock()
	if w.healthTicker != nil {
		w.healthTicker.Stop()
		w.healthTicker = nil
	}
	w.mu.Unlock()

	// Update state
	w.setState(ConnectionStateDisconnected, nil)
}

// setState updates the connection state and triggers callbacks
func (w *IMAPWorker) setState(newState ConnectionState, err error) {
	w.mu.Lock()
	oldState := w.state
	w.state = newState
	callback := w.onConnectionStateChange
	w.mu.Unlock()

	if oldState != newState {
		w.logger.Debug("Connection state changed: %s -> %s", oldState, newState)

		if callback != nil {
			// Call callback in a separate goroutine to avoid blocking
			go func() {
				event := NewConnectionEvent(newState, err)
				callback(*event)
			}()
		}
	}
}

// incrementErrorCount increments the error counter
func (w *IMAPWorker) incrementErrorCount() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.errorCount++
}

// incrementReconnectCount increments the reconnect counter
func (w *IMAPWorker) incrementReconnectCount() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.reconnectCount++
}

// Command handlers

// handleConnect handles the connect command
func (w *IMAPWorker) handleConnect(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling connect command")

	// Check if already connected
	if w.commandClient != nil && w.idleClient != nil {
		w.logger.Debug("Already connected, returning success")
		return NewResponse(cmd.ID, true, map[string]interface{}{
			DataConnectionState: w.state,
		}, nil)
	}

	// Get server config from command parameters
	config, ok := cmd.GetServerConfig(ParamConfig)
	if !ok {
		// Use worker's default config
		config = w.config
	}

	if config == nil {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("no server configuration provided"))
	}

	// Update state to connecting
	w.setState(ConnectionStateConnecting, nil)

	// Create both IMAP clients (command and IDLE)
	w.logger.Debug("Connecting to IMAP server: %s:%d (TLS: %v)", config.Host, config.Port, config.TLS)

	// Create TLS configuration
	tlsConfig := &tls.Config{
		ServerName: config.Host,
	}

	// Helper function to create a client connection
	createClient := func(purpose string) (*imapclient.Client, error) {
		w.logger.Debug("Creating %s client connection", purpose)
		var client *imapclient.Client
		var err error

		// Create options with tracing if enabled
		options := &imapclient.Options{
			TLSConfig: tlsConfig,
		}

		// Add debug writer for tracing if enabled
		if w.tracer != nil {
			options.DebugWriter = w.tracer
			w.logger.Debug("IMAP tracing enabled for %s client", purpose)
		}

		if config.TLS {
			// Direct TLS connection
			client, err = imapclient.DialTLS(fmt.Sprintf("%s:%d", config.Host, config.Port), options)
		} else {
			// Plain connection with STARTTLS
			client, err = imapclient.DialStartTLS(fmt.Sprintf("%s:%d", config.Host, config.Port), options)
		}

		if err != nil {
			return nil, fmt.Errorf("failed to connect %s client: %w", purpose, err)
		}

		// Authenticate
		if config.Username != "" && config.Password != "" {
			w.logger.Debug("Authenticating %s client with username: %s", purpose, config.Username)
			if err := client.Login(config.Username, config.Password).Wait(); err != nil {
				client.Close()
				return nil, fmt.Errorf("authentication failed for %s client: %w", purpose, err)
			}
		}

		return client, nil
	}

	// Create command client
	commandClient, err := createClient("command")
	if err != nil {
		w.logger.Error("Failed to create command client: %v", err)
		w.setState(ConnectionStateFailed, err)
		w.incrementErrorCount()
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("command client creation failed: %w", err))
	}

	// Create IDLE client with a UnilateralDataHandler so the server's unsolicited
	// EXISTS/EXPUNGE responses during IDLE are delivered immediately rather than
	// only being noticed when the 29-minute timeout fires.
	w.logger.Debug("Creating IDLE client connection")
	idleOptions := &imapclient.Options{
		TLSConfig: tlsConfig,
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{
			Mailbox: func(data *imapclient.UnilateralDataMailbox) {
				// Server pushed a mailbox update (e.g. * N EXISTS) while we are IDLing.
				// NumMessages being non-nil means the message count changed, which is
				// the primary indicator that new mail has arrived.
				if data.NumMessages != nil {
					w.logger.Debug("IDLE: server pushed mailbox update (NumMessages=%d), signalling check",
						*data.NumMessages)
					// Non-blocking send: if a signal is already pending we don't need
					// another one – the monitor loop will check for new messages anyway.
					select {
					case w.idleUpdateCh <- struct{}{}:
					default:
					}
				}
			},
		},
	}
	if w.tracer != nil {
		idleOptions.DebugWriter = w.tracer
		w.logger.Debug("IMAP tracing enabled for IDLE client")
	}

	var idleClient *imapclient.Client
	if config.TLS {
		idleClient, err = imapclient.DialTLS(fmt.Sprintf("%s:%d", config.Host, config.Port), idleOptions)
	} else {
		idleClient, err = imapclient.DialStartTLS(fmt.Sprintf("%s:%d", config.Host, config.Port), idleOptions)
	}
	if err != nil {
		commandClient.Close()
		w.logger.Error("Failed to connect IDLE client: %v", err)
		w.setState(ConnectionStateFailed, err)
		w.incrementErrorCount()
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("IDLE client creation failed: %w", err))
	}
	if config.Username != "" && config.Password != "" {
		if err := idleClient.Login(config.Username, config.Password).Wait(); err != nil {
			idleClient.Close()
			commandClient.Close()
			w.logger.Error("Failed to authenticate IDLE client: %v", err)
			w.setState(ConnectionStateFailed, err)
			w.incrementErrorCount()
			return NewResponse(cmd.ID, false, nil, fmt.Errorf("IDLE client authentication failed: %w", err))
		}
	}

	// Store clients and update state
	w.commandClient = commandClient
	w.idleClient = idleClient
	w.setState(ConnectionStateConnected, nil)

	// If this is a reconnection (not initial connection), set the reconnection time
	w.mu.Lock()
	if w.reconnectCount > 0 {
		w.lastReconnectTime = time.Now()
		w.logger.Debug("Set reconnection grace period for account %s (duration: %v)", w.accountName, w.reconnectGracePeriod)
	}
	w.mu.Unlock()

	w.logger.Debug("Successfully connected to IMAP server for account: %s", w.accountName)
	return NewResponse(cmd.ID, true, map[string]interface{}{
		DataConnectionState: w.state,
	}, nil)
}

// handleDisconnect handles the disconnect command
func (w *IMAPWorker) handleDisconnect(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling disconnect command")

	if w.commandClient == nil && w.idleClient == nil {
		w.logger.Debug("Already disconnected")
		return NewResponse(cmd.ID, true, map[string]interface{}{
			DataConnectionState: w.state,
		}, nil)
	}

	// Stop IDLE monitoring before disconnecting
	w.stopIDLEInternal()

	// Close both connections
	if w.commandClient != nil {
		w.logger.Debug("Closing command client connection")
		if err := w.commandClient.Close(); err != nil {
			w.logger.Warn("Error closing command client connection: %v", err)
		}
		w.commandClient = nil
	}

	if w.idleClient != nil {
		w.logger.Debug("Closing IDLE client connection")
		if err := w.idleClient.Close(); err != nil {
			w.logger.Warn("Error closing IDLE client connection: %v", err)
		}
		w.idleClient = nil
	}
	w.setState(ConnectionStateDisconnected, nil)

	// Clear selected folder and IDLE state
	w.mu.Lock()
	w.selectedFolder = ""
	w.idleActive = false
	w.mu.Unlock()

	w.logger.Debug("Disconnected from IMAP server for account: %s", w.accountName)
	return NewResponse(cmd.ID, true, map[string]interface{}{
		DataConnectionState: w.state,
	}, nil)
}

// handleReconnect handles the reconnect command
func (w *IMAPWorker) handleReconnect(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling reconnect command")

	w.incrementReconnectCount()
	w.setState(ConnectionStateReconnecting, nil)

	// Disconnect first if connected
	if w.commandClient != nil {
		w.logger.Debug("Disconnecting command client before reconnect")
		w.commandClient.Close()
		w.commandClient = nil
	}
	if w.idleClient != nil {
		w.logger.Debug("Disconnecting IDLE client before reconnect")
		w.idleClient.Close()
		w.idleClient = nil
	}

	// Clear state
	w.mu.Lock()
	w.selectedFolder = ""
	w.idleActive = false
	w.mu.Unlock()

	// Attempt to reconnect
	connectCmd := NewCommandWithContext(cmd.Context, CmdConnect, cmd.Parameters)
	return w.handleConnect(connectCmd)
}

// handleHealthCheck handles the health check command
func (w *IMAPWorker) handleHealthCheck(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling health check command")

	if w.commandClient == nil {
		return NewResponse(cmd.ID, false, map[string]interface{}{
			DataHealthStatus: w.GetHealthStatus(),
		}, fmt.Errorf("not connected"))
	}

	// Perform comprehensive health check
	healthy, err := w.performComprehensiveHealthCheck()

	if !healthy {
		w.logger.Warn("Connection health check failed: %v", err)
		w.incrementErrorCount()

		// Check if this is an IDLE monitoring issue
		if err != nil && strings.Contains(err.Error(), "IDLE monitoring stuck") {
			w.logger.Warn("IDLE monitoring stuck detected, attempting to restart IDLE")
			go w.recoverStuckIDLE()
		} else if w.isConnectionError(err) {
			// If health check fails with connection error, attempt automatic reconnection
			w.logger.Info("Connection error detected during health check, attempting automatic reconnection")
			go w.attemptAutomaticReconnection()
		}
	}

	// Get detailed health status
	healthStatus := w.GetHealthStatus()
	healthStatus["connection_healthy"] = healthy
	healthStatus["last_health_check"] = time.Now().Format(time.RFC3339)

	if !healthy {
		return NewResponse(cmd.ID, false, map[string]interface{}{
			DataHealthStatus: healthStatus,
		}, fmt.Errorf("connection health check failed: %w", err))
	}

	return NewResponse(cmd.ID, true, map[string]interface{}{
		DataHealthStatus: healthStatus,
	}, nil)
}

// handleGetStatus handles the get status command
func (w *IMAPWorker) handleGetStatus(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling get status command")

	status := w.GetStatus()
	return NewResponse(cmd.ID, true, map[string]interface{}{
		DataWorkerStatus: status,
	}, nil)
}

// handleRefreshFoldersInBackground handles the refresh folders in background command
func (w *IMAPWorker) handleRefreshFoldersInBackground(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling refresh folders in background command")

	// Capture a stable client reference for the background goroutine.
	w.mu.RLock()
	client := w.commandClient
	w.mu.RUnlock()

	// This is a fire-and-forget operation, so we return success immediately
	// and do the actual work in a goroutine
	go func(c *imapclient.Client) {
		// Only proceed if we're connected
		if c == nil {
			w.logger.Debug("Not connected, skipping background folder refresh")
			return
		}

		w.logger.Debug("Starting background folder refresh")

		// Refresh subscribed folders from server and update cache
		listCmd := c.List("", "*", &imap.ListOptions{
			SelectSubscribed: true,
		})
		mailboxes, err := listCmd.Collect()
		if err != nil {
			w.logger.Warn("Background folder refresh failed: %v", err)
			return
		}

		// Convert to email.Folder format
		folders := make([]email.Folder, len(mailboxes))
		for i, mb := range mailboxes {
			folders[i] = email.Folder{
				Name:       mb.Mailbox,
				Path:       mb.Mailbox,
				Delimiter:  "/", // Default delimiter
				Attributes: w.convertAttributes(mb.Attrs),
				Subscribed: true,
			}
		}

		// Update cache with fresh folder data
		if err := w.cacheSubscribedFolders(folders); err != nil {
			w.logger.Warn("Failed to cache refreshed folders: %v", err)
		} else {
			w.logger.Debug("Successfully refreshed and cached %d folders in background", len(folders))
		}
	}(client)

	// Return success immediately since this is fire-and-forget
	return NewResponse(cmd.ID, true, map[string]interface{}{
		"status": "background_refresh_started",
	}, nil)
}

// handleSelectFolder handles the select folder command
func (w *IMAPWorker) handleSelectFolder(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling select folder command")

	if w.commandClient == nil {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("not connected"))
	}

	folderName, ok := cmd.GetString(ParamFolderName)
	if !ok {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("folder name not provided"))
	}

	w.logger.Debug("Selecting folder: %s", folderName)

	// Select the folder
	selectData, err := w.commandClient.Select(folderName, nil).Wait()
	if err != nil {
		w.logger.Error("Failed to select folder %s: %v", folderName, err)
		w.incrementErrorCount()
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to select folder: %w", err))
	}

	// Update selected folder
	w.mu.Lock()
	w.selectedFolder = folderName
	w.mu.Unlock()

	w.logger.Debug("Successfully selected folder: %s", folderName)
	return NewResponse(cmd.ID, true, map[string]interface{}{
		DataFolder: map[string]interface{}{
			"name":          folderName,
			"message_count": selectData.NumMessages,
			"recent_count":  selectData.NumRecent,
			// Note: Unseen count is not available in SelectData, use Status command separately if needed
		},
	}, nil)
}

// handleListFolders handles the list folders command
func (w *IMAPWorker) handleListFolders(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling list folders command")

	// Try to get folders from cache first
	if cachedFolders, found, err := w.getCachedFolders(); err == nil && found {
		w.logger.Debug("Retrieved %d folders from cache", len(cachedFolders))
		return NewResponse(cmd.ID, true, map[string]interface{}{
			DataFolders: cachedFolders,
		}, nil)
	}

	// If not connected, return empty list (cache miss and no connection)
	if w.commandClient == nil {
		w.logger.Debug("Not connected and no cached folders available")
		return NewResponse(cmd.ID, true, map[string]interface{}{
			DataFolders: []email.Folder{},
		}, nil)
	}

	w.logger.Debug("Listing all folders from server")

	// List all folders from server
	listCmd := w.commandClient.List("", "*", nil)
	mailboxes, err := listCmd.Collect()
	if err != nil {
		w.logger.Error("Failed to list folders: %v", err)
		w.incrementErrorCount()
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to list folders: %w", err))
	}

	// Convert to email.Folder format
	folders := make([]email.Folder, len(mailboxes))
	for i, mb := range mailboxes {
		folders[i] = email.Folder{
			Name:       mb.Mailbox,
			Path:       mb.Mailbox,
			Delimiter:  "/", // Default delimiter, will be updated when we get proper delimiter info
			Attributes: w.convertAttributes(mb.Attrs),
			Subscribed: false, // Will be determined separately
		}
	}

	// Cache the folders
	if err := w.cacheFolders(folders); err != nil {
		w.logger.Warn("Failed to cache folders: %v", err)
	}

	w.logger.Debug("Listed %d folders from server", len(folders))
	return NewResponse(cmd.ID, true, map[string]interface{}{
		DataFolders: folders,
	}, nil)
}

// handleListSubscribedFolders handles the list subscribed folders command
func (w *IMAPWorker) handleListSubscribedFolders(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling list subscribed folders command")

	// Try to get subscribed folders from cache first
	if cachedFolders, found, err := w.getCachedSubscribedFolders(); err == nil && found {
		w.logger.Debug("Retrieved %d subscribed folders from cache", len(cachedFolders))
		return NewResponse(cmd.ID, true, map[string]interface{}{
			DataFolders: cachedFolders,
		}, nil)
	}

	// If not connected, return empty list (cache miss and no connection)
	if w.commandClient == nil {
		w.logger.Debug("Not connected and no cached subscribed folders available")
		return NewResponse(cmd.ID, true, map[string]interface{}{
			DataFolders: []email.Folder{},
		}, nil)
	}

	w.logger.Debug("Listing subscribed folders from server")

	// List subscribed folders from server
	listCmd := w.commandClient.List("", "*", &imap.ListOptions{
		SelectSubscribed: true,
	})
	mailboxes, err := listCmd.Collect()
	if err != nil {
		w.logger.Error("Failed to list subscribed folders: %v", err)
		w.incrementErrorCount()
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to list subscribed folders: %w", err))
	}

	// Convert to email.Folder format
	folders := make([]email.Folder, len(mailboxes))
	for i, mb := range mailboxes {
		folders[i] = email.Folder{
			Name:       mb.Mailbox,
			Path:       mb.Mailbox,
			Delimiter:  "/", // Default delimiter, will be updated when we get proper delimiter info
			Attributes: w.convertAttributes(mb.Attrs),
			Subscribed: true,
		}
	}

	// Cache the subscribed folders
	if err := w.cacheSubscribedFolders(folders); err != nil {
		w.logger.Warn("Failed to cache subscribed folders: %v", err)
	}

	w.logger.Debug("Listed %d subscribed folders from server", len(folders))
	return NewResponse(cmd.ID, true, map[string]interface{}{
		DataFolders: folders,
	}, nil)
}

// handleCreateFolder handles the create folder command
func (w *IMAPWorker) handleCreateFolder(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling create folder command")

	if w.commandClient == nil {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("not connected"))
	}

	folderName, ok := cmd.GetString(ParamFolderName)
	if !ok {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("folder name not provided"))
	}

	w.logger.Debug("Creating folder: %s", folderName)

	// Create the folder
	if err := w.commandClient.Create(folderName, nil).Wait(); err != nil {
		w.logger.Error("Failed to create folder %s: %v", folderName, err)
		w.incrementErrorCount()
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to create folder: %w", err))
	}

	w.logger.Debug("Successfully created folder: %s", folderName)
	return NewResponse(cmd.ID, true, map[string]interface{}{
		DataFolderCreated: folderName,
	}, nil)
}

// handleDeleteFolder handles the delete folder command
func (w *IMAPWorker) handleDeleteFolder(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling delete folder command")

	if w.commandClient == nil {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("not connected"))
	}

	folderName, ok := cmd.GetString(ParamFolderName)
	if !ok {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("folder name not provided"))
	}

	w.logger.Debug("Deleting folder: %s", folderName)

	// Delete the folder
	if err := w.commandClient.Delete(folderName).Wait(); err != nil {
		w.logger.Error("Failed to delete folder %s: %v", folderName, err)
		w.incrementErrorCount()
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to delete folder: %w", err))
	}

	w.logger.Debug("Successfully deleted folder: %s", folderName)
	return NewResponse(cmd.ID, true, nil, nil)
}

// handleSubscribeFolder handles the subscribe folder command
func (w *IMAPWorker) handleSubscribeFolder(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling subscribe folder command")

	if w.commandClient == nil {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("not connected"))
	}

	folderName, ok := cmd.GetString(ParamFolderName)
	if !ok {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("folder name not provided"))
	}

	w.logger.Debug("Subscribing to folder: %s", folderName)

	// Subscribe to the folder
	if err := w.commandClient.Subscribe(folderName).Wait(); err != nil {
		w.logger.Error("Failed to subscribe to folder %s: %v", folderName, err)
		w.incrementErrorCount()
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to subscribe to folder: %w", err))
	}

	w.logger.Debug("Successfully subscribed to folder: %s", folderName)
	return NewResponse(cmd.ID, true, nil, nil)
}

// handleUnsubscribeFolder handles the unsubscribe folder command
func (w *IMAPWorker) handleUnsubscribeFolder(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling unsubscribe folder command")

	if w.commandClient == nil {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("not connected"))
	}

	folderName, ok := cmd.GetString(ParamFolderName)
	if !ok {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("folder name not provided"))
	}

	w.logger.Debug("Unsubscribing from folder: %s", folderName)

	// Unsubscribe from the folder
	if err := w.commandClient.Unsubscribe(folderName).Wait(); err != nil {
		w.logger.Error("Failed to unsubscribe from folder %s: %v", folderName, err)
		w.incrementErrorCount()
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to unsubscribe from folder: %w", err))
	}

	w.logger.Debug("Successfully unsubscribed from folder: %s", folderName)
	return NewResponse(cmd.ID, true, nil, nil)
}

// handleFetchMessages handles the fetch messages command
func (w *IMAPWorker) handleFetchMessages(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling fetch messages command")

	if w.commandClient == nil {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("not connected"))
	}

	folderName, ok := cmd.GetString(ParamFolderName)
	if !ok {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("folder name not provided"))
	}

	limit, _ := cmd.GetInt(ParamLimit) // Optional parameter

	w.logger.Debug("Fetching messages from folder: %s (limit: %d)", folderName, limit)

	// Select folder if not already selected
	if w.selectedFolder != folderName {
		_, err := w.commandClient.Select(folderName, nil).Wait()
		if err != nil {
			w.logger.Error("Failed to select folder %s: %v", folderName, err)
			w.incrementErrorCount()
			return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to select folder: %w", err))
		}
		w.mu.Lock()
		w.selectedFolder = folderName
		w.mu.Unlock()
	}

	// Use UID SEARCH so we get stable UIDs, not volatile sequence numbers.
	// Sequence numbers shift whenever a message is expunged: a concurrent
	// expunge between SEARCH and FETCH can make the sequence-number set
	// invalid, causing servers like Dovecot to return
	// "BAD Error in IMAP command FETCH: Invalid messageset".
	// UIDs are permanent identifiers; a UID FETCH for a missing UID simply
	// returns no data instead of an error.
	searchCriteria := &imap.SearchCriteria{}

	searchCmd := w.commandClient.UIDSearch(searchCriteria, nil)
	searchData, err := searchCmd.Wait()
	if err != nil {
		w.logger.Error("Failed to UID SEARCH messages in folder %s: %v", folderName, err)
		w.incrementErrorCount()
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to search messages: %w", err))
	}

	uids := searchData.AllUIDs()
	if len(uids) == 0 {
		w.logger.Debug("No messages found in folder: %s", folderName)
		return NewResponse(cmd.ID, true, map[string]interface{}{
			DataMessages: []email.Message{},
		}, nil)
	}

	// Apply limit if specified (get most recent messages; UIDs are ascending)
	if limit > 0 && len(uids) > limit {
		uids = uids[len(uids)-limit:]
	}

	// Build a compact UID range for the fetch command.
	//
	// Expressing the request as a single "minUID:maxUID" range keeps the IMAP
	// command argument short regardless of how many messages are in the folder
	// or how fragmented the UID space is.  Listing every UID individually via
	// AddNum can produce a multi-kilobyte argument string for large or heavily-
	// pruned mailboxes, causing servers like Dovecot to reject the command with
	// "BAD Too long argument".
	//
	// UID FETCH silently returns no data for UIDs that no longer exist (e.g.
	// expunged messages), so any gaps within the range are completely harmless.
	// UIDs returned by UIDSearch are already sorted ascending, so uids[0] is
	// the smallest and uids[len-1] is the largest.
	uidSet := imap.UIDSet{}
	if len(uids) > 0 {
		uidSet.AddRange(uids[0], uids[len(uids)-1])
	}

	// Fetch message envelopes and flags via UID FETCH
	fetchCmd := w.commandClient.Fetch(uidSet, &imap.FetchOptions{
		Envelope:     true,
		Flags:        true,
		UID:          true,
		RFC822Size:   true,
		InternalDate: true, // Fetch actual arrival date from server
	})

	messages := []email.Message{}
	for {
		msg := fetchCmd.Next()
		if msg == nil {
			break
		}

		emailMsg := w.convertFetchDataToMessage(msg)
		messages = append(messages, emailMsg)
	}

	if err := fetchCmd.Close(); err != nil {
		w.logger.Warn("Error closing fetch command: %v", err)
	}

	// Cache the fetched messages
	if err := w.cacheMessages(folderName, messages); err != nil {
		w.logger.Warn("Failed to cache messages for folder %s: %v", folderName, err)
	}

	w.logger.Debug("Successfully fetched %d messages from folder: %s", len(messages), folderName)
	return NewResponse(cmd.ID, true, map[string]interface{}{
		DataMessages: messages,
	}, nil)
}

// convertFetchDataToMessage converts IMAP fetch data to email.Message format (exact copy of working client.go code)
func (w *IMAPWorker) convertFetchDataToMessage(msg *imapclient.FetchMessageData) email.Message {
	emailMsg := email.Message{
		ID: fmt.Sprintf("%d", msg.SeqNum), // Use sequence number as ID for now
	}

	// Process all fetch item data
	for {
		item := msg.Next()
		if item == nil {
			break
		}

		switch data := item.(type) {
		case imapclient.FetchItemDataEnvelope:
			if data.Envelope != nil {
				w.populateFromEnvelope(&emailMsg, data.Envelope)
			}
		case imapclient.FetchItemDataFlags:
			emailMsg.Flags = w.convertFlags(data.Flags)
		case imapclient.FetchItemDataUID:
			emailMsg.UID = uint32(data.UID)
			emailMsg.ID = fmt.Sprintf("%d", data.UID) // Use UID as ID instead of sequence number
		case imapclient.FetchItemDataRFC822Size:
			emailMsg.Size = int64(data.Size)
		case imapclient.FetchItemDataInternalDate:
			emailMsg.InternalDate = data.Time // Set the actual arrival date from server
		case imapclient.FetchItemDataBodySection:
			// Handle body section data
			if data.Literal != nil {
				bodyBytes, err := io.ReadAll(data.Literal)
				if err != nil {
					continue
				}

				// Check if this is a header-only section
				if data.Section != nil && data.Section.Specifier == imap.PartSpecifierHeader {
					// This is just the headers section - parse headers directly without body parsing
					// Don't use ParseRawMessage() as it will fail on multipart messages without body
					reader := bytes.NewReader(bodyBytes)
					msg, err := mail.ReadMessage(reader)
					if err != nil {
						w.logger.Warn("Failed to read header section (UID %d, size %d bytes): %v", emailMsg.UID, len(bodyBytes), err)
					} else {
						// Extract headers directly
						if emailMsg.Headers == nil {
							emailMsg.Headers = make(map[string]string)
						}
						for key := range msg.Header {
							emailMsg.Headers[key] = msg.Header.Get(key)
						}
						w.logger.Debug("Parsed %d headers from header section", len(emailMsg.Headers))
					}
				} else {
					// This is the full message body - parse everything
					processor := email.NewMessageProcessor()
					parsedMsg, err := processor.ParseRawMessage(bodyBytes)
					if err != nil {
						w.logger.Warn("Failed to parse message body (UID %d, size %d bytes): %v", emailMsg.UID, len(bodyBytes), err)
						w.logger.Warn("Raw message saved to /tmp/failed-message-*.eml for analysis")
						emailMsg.Body = email.MessageBody{
							Text: fmt.Sprintf("Error parsing message body: %v", err),
						}
					} else {
						emailMsg.Body = parsedMsg.Body
						emailMsg.Attachments = parsedMsg.Attachments
						// Also merge headers from the full message if we don't have them yet
						if emailMsg.Headers == nil {
							emailMsg.Headers = make(map[string]string)
						}
						for key, value := range parsedMsg.Headers {
							emailMsg.Headers[key] = value
						}
					}
				}
			}
		}
	}
	return emailMsg
}

// populateFromEnvelope populates message fields from IMAP envelope
func (w *IMAPWorker) populateFromEnvelope(msg *email.Message, env *imap.Envelope) {
	msg.Subject = env.Subject
	msg.Date = env.Date
	msg.From = w.convertAddresses(env.From)
	msg.To = w.convertAddresses(env.To)
	msg.CC = w.convertAddresses(env.Cc)
	msg.BCC = w.convertAddresses(env.Bcc)
	msg.ReplyTo = w.convertAddresses(env.ReplyTo)

	// Use Message-ID if available
	if env.MessageID != "" {
		msg.ID = env.MessageID
	}
}

// convertAddresses converts IMAP addresses to email.Address format
func (w *IMAPWorker) convertAddresses(imapAddrs []imap.Address) []email.Address {
	if len(imapAddrs) == 0 {
		return nil
	}

	addrs := make([]email.Address, len(imapAddrs))
	for i, addr := range imapAddrs {
		addrs[i] = email.Address{
			Name:  addr.Name,
			Email: addr.Mailbox + "@" + addr.Host,
		}
	}
	return addrs
}

// convertFlags converts IMAP flags to string slice
func (w *IMAPWorker) convertFlags(imapFlags []imap.Flag) []string {
	if len(imapFlags) == 0 {
		return nil
	}

	flags := make([]string, len(imapFlags))
	for i, flag := range imapFlags {
		flags[i] = string(flag)
	}
	return flags
}

// convertAttributes converts IMAP folder attributes to string slice
func (w *IMAPWorker) convertAttributes(attrs []imap.MailboxAttr) []string {
	var result []string
	for _, attr := range attrs {
		result = append(result, string(attr))
	}
	return result
}

// initializeFolderTracking initializes tracking data for a folder
func (w *IMAPWorker) initializeFolderTracking(folder string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, exists := w.lastMessageCount[folder]; !exists {
		w.lastMessageCount[folder] = 0
	}
	if _, exists := w.lastHighestUID[folder]; !exists {
		w.lastHighestUID[folder] = 0
	}
	if _, exists := w.initialSyncDone[folder]; !exists {
		w.initialSyncDone[folder] = false
	}
}

// startIDLEInternal starts IDLE monitoring for the specified folder
func (w *IMAPWorker) startIDLEInternal(folder string) error {
	w.logger.Debug("Starting IDLE monitoring for folder: %s", folder)

	// Check if IDLE client is available
	if w.idleClient == nil {
		return fmt.Errorf("IDLE client not available (connection may be down)")
	}

	// Start IDLE command
	idleCmd, err := w.idleClient.Idle()
	if err != nil {
		return fmt.Errorf("failed to start IDLE command: %w", err)
	}

	// Create context for IDLE timeout
	ctx, cancel := context.WithTimeout(w.ctx, w.idleTimeout)

	w.mu.Lock()
	w.idleActive = true
	w.idlePaused = false
	w.idleFolder = folder
	w.idleCmd = idleCmd
	w.idleCancel = cancel
	w.mu.Unlock()

	// Start IDLE monitoring goroutine
	go w.idleMonitorLoop(ctx, idleCmd, folder)

	return nil
}

// stopIDLEInternal stops IDLE monitoring
func (w *IMAPWorker) stopIDLEInternal() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.idleActive {
		return
	}

	w.logger.Debug("Stopping IDLE monitoring for folder: %s", w.idleFolder)

	// Cancel the IDLE context
	if w.idleCancel != nil {
		w.idleCancel()
	}

	// Close the IDLE command. idleCancel() above may have already caused the
	// idleMonitorLoop goroutine to call idleCmd.Close(); the library's atomic
	// guard returns "already closed" in that race, which is expected and harmless.
	if w.idleCmd != nil {
		if err := w.idleCmd.Close(); err != nil {
			w.logger.Debug("IDLE close in stopIDLEInternal (may be harmless if goroutine closed first): %v", err)
		}
	}

	// Reset IDLE state
	w.idleActive = false
	w.idlePaused = false
	w.idleFolder = ""
	w.idleCmd = nil
	w.idleCancel = nil
}

// pauseIDLEInternal pauses IDLE monitoring
func (w *IMAPWorker) pauseIDLEInternal() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.idleActive || w.idlePaused {
		return
	}

	w.logger.Debug("Pausing IDLE monitoring for folder: %s", w.idleFolder)

	// Cancel the current IDLE context to interrupt it
	if w.idleCancel != nil {
		w.idleCancel()
	}

	w.idlePaused = true
}

// resumeIDLEInternal resumes IDLE monitoring
func (w *IMAPWorker) resumeIDLEInternal() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.idleActive || !w.idlePaused {
		return nil
	}

	w.logger.Debug("Resuming IDLE monitoring for folder: %s", w.idleFolder)

	// Check if IDLE client is available
	if w.idleClient == nil {
		return fmt.Errorf("IDLE client not available (connection may be down)")
	}

	// Start a new IDLE command
	idleCmd, err := w.idleClient.Idle()
	if err != nil {
		return fmt.Errorf("failed to restart IDLE command: %w", err)
	}

	// Create new context for IDLE timeout
	ctx, cancel := context.WithTimeout(w.ctx, w.idleTimeout)

	// Update IDLE state
	w.idleCmd = idleCmd
	w.idleCancel = cancel
	w.idlePaused = false

	// Start new IDLE monitoring goroutine
	go w.idleMonitorLoop(ctx, idleCmd, w.idleFolder)

	return nil
}

// idleMonitorLoop is the main IDLE monitoring loop
func (w *IMAPWorker) idleMonitorLoop(ctx context.Context, idleCmd *imapclient.IdleCommand, folder string) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("IDLE monitor loop panic: %v", r)
		}
	}()

	w.logger.Debug("IDLE monitor loop started for folder: %s", folder)

	// Update IDLE activity timestamp to indicate loop is running
	w.mu.Lock()
	w.lastIDLEActivity = time.Now()
	w.mu.Unlock()

	// Wait for IDLE to complete or timeout
	done := make(chan error, 1)
	go func() {
		done <- idleCmd.Wait()
	}()

	select {
	case <-w.idleUpdateCh:
		// The server pushed a mailbox update (e.g. * N EXISTS) via the UnilateralDataHandler
		// while we were IDLing. Terminate IDLE immediately, check for new messages, then restart.
		w.logger.Debug("IDLE: received server push notification for folder: %s, terminating IDLE to fetch updates", folder)
		if err := idleCmd.Close(); err != nil {
			// "already closed" can occur if stopIDLEInternal also called Close; treat as non-fatal.
			w.logger.Debug("IDLE close after server push (may be harmless): %v", err)
		}
		<-done // Wait for idleCmd.Wait() (server OK to DONE) to complete before any new command.
		w.checkForNewMessages(folder)

	case <-ctx.Done():
		// Timeout or context cancellation
		contextErr := ctx.Err()
		if contextErr == context.DeadlineExceeded {
			w.logger.Debug("IDLE timeout reached after %v for folder: %s", w.idleTimeout, folder)
		} else {
			w.logger.Debug("IDLE cancelled for folder: %s, reason: %v", folder, contextErr)
		}

		// Close the IDLE command
		if err := idleCmd.Close(); err != nil {
			// Suppress "already closed" - stopIDLEInternal may have beaten us here.
			w.logger.Debug("IDLE close on ctx.Done() (may be harmless): %v", err)
		}
		<-done // Wait for the command to actually finish

		// Check for new messages on timeout (server may have changes)
		if contextErr == context.DeadlineExceeded {
			w.logger.Debug("IDLE timeout reached, checking for new messages in folder: %s", folder)
			w.checkForNewMessages(folder)
		}

	case err := <-done:
		// IDLE completed (server sent updates)
		if err != nil {
			w.logger.Error("IDLE command error for folder %s: %v", folder, err)
			w.incrementErrorCount()

			// Check if this is a connection error that requires reconnection
			if w.isConnectionError(err) {
				w.logger.Info("Connection error detected in IDLE monitoring, attempting automatic reconnection")
				go w.attemptAutomaticReconnection()
				return // Exit the loop, reconnection will restart IDLE if successful
			}
		} else {
			// IDLE completed successfully, meaning server has updates
			w.logger.Debug("IDLE completed, server has updates for folder: %s", folder)
			w.checkForNewMessages(folder)
		}
	}

	// If IDLE is still active and not paused, restart it
	w.mu.RLock()
	shouldRestart := w.idleActive && !w.idlePaused && w.idleFolder == folder
	w.mu.RUnlock()

	if shouldRestart {
		w.logger.Info("Restarting IDLE monitoring for folder: %s after timeout/completion", folder)
		if err := w.startIDLEInternal(folder); err != nil {
			w.logger.Error("Failed to restart IDLE monitoring for folder %s: %v", folder, err)
			w.incrementErrorCount()

			// Check if this is a connection error that requires reconnection
			if w.isConnectionError(err) {
				// DON'T clear idleActive/idleFolder before reconnection!
				// The reconnection logic needs to know IDLE was active so it can restart it
				w.logger.Info("Connection error detected while restarting IDLE, attempting automatic reconnection")
				go w.attemptAutomaticReconnection()
			} else {
				// Non-connection error - mark IDLE as inactive and stop
				w.mu.Lock()
				w.idleActive = false
				w.mu.Unlock()
				// Non-connection error - log it prominently so it's visible
				w.logger.Warn("IDLE restart failed with non-connection error, IDLE monitoring stopped for folder %s", folder)
			}
		} else {
			w.logger.Info("Successfully restarted IDLE monitoring for folder: %s", folder)
		}
	} else {
		w.logger.Debug("IDLE monitoring not restarted (active=%v, paused=%v, folder match=%v)", w.idleActive, w.idlePaused, w.idleFolder == folder)
	}
}

// recoverStuckIDLE attempts to recover from a stuck IDLE monitoring state
func (w *IMAPWorker) recoverStuckIDLE() {
	w.mu.RLock()
	idleFolder := w.idleFolder
	idleActive := w.idleActive
	w.mu.RUnlock()

	if !idleActive {
		w.logger.Debug("IDLE not active, no recovery needed")
		return
	}

	w.logger.Info("Attempting to recover stuck IDLE monitoring for folder: %s", idleFolder)

	// Try to stop and restart IDLE monitoring
	// First, try to cancel the current IDLE context without clearing state
	w.mu.Lock()
	if w.idleCancel != nil {
		w.idleCancel()
	}
	if w.idleCmd != nil {
		if err := w.idleCmd.Close(); err != nil {
			w.logger.Warn("Error closing IDLE command during recovery: %v", err)
		}
	}
	w.mu.Unlock()

	// Wait a moment for cleanup
	select {
	case <-time.After(1 * time.Second):
	case <-w.ctx.Done():
		return
	}

	// Try to restart IDLE monitoring
	if err := w.startIDLEInternal(idleFolder); err != nil {
		w.logger.Error("Failed to recover IDLE monitoring for folder %s: %v", idleFolder, err)

		// If restart fails with a connection error, attempt full reconnection
		// DON'T clear idleActive/idleFolder - let reconnection logic handle IDLE restart
		if w.isConnectionError(err) {
			w.logger.Info("IDLE recovery failed with connection error, attempting full reconnection")
			go w.attemptAutomaticReconnection()
		} else {
			// Non-connection error - clear IDLE state since we can't recover
			w.mu.Lock()
			w.idleActive = false
			w.idlePaused = false
			w.idleFolder = ""
			w.idleCmd = nil
			w.idleCancel = nil
			w.mu.Unlock()
			w.logger.Warn("IDLE recovery failed with non-connection error, IDLE monitoring stopped")
		}
	} else {
		w.logger.Info("Successfully recovered IDLE monitoring for folder: %s", idleFolder)

		// Check for messages that may have been missed while IDLE was stuck
		w.logger.Info("Checking for messages that may have been missed while IDLE was stuck for folder: %s", idleFolder)
		w.sendCheckNewMessagesCommand(idleFolder)
	}
}

// sendCheckNewMessagesCommand sends a CmdCheckNewMessages command through the
// command channel so that the check runs on the event loop goroutine.
// Safe to call from any goroutine (IDLE monitor, reconnection, etc.).
func (w *IMAPWorker) sendCheckNewMessagesCommand(folder string) {
	cmd := NewCommand(CmdCheckNewMessages, map[string]interface{}{
		ParamFolderName: folder,
	})
	select {
	case w.commandCh <- cmd:
		// queued
	case <-w.ctx.Done():
		// worker shutting down
	}
}

// handleCheckNewMessages handles the CmdCheckNewMessages command on the event loop.
func (w *IMAPWorker) handleCheckNewMessages(cmd *IMAPCommand) *IMAPResponse {
	folder, _ := cmd.Parameters[ParamFolderName].(string)
	if folder == "" {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("folder name required"))
	}
	w.checkForNewMessages(folder)
	return NewResponse(cmd.ID, true, nil, nil)
}

// checkForNewMessages compares current folder state with previous state to detect new messages.
// Called from the IDLE monitor goroutine (between IDLE cycles) and from
// CmdCheckNewMessages on the event loop. Access to commandClient is protected
// by capturing the reference under w.mu.
func (w *IMAPWorker) checkForNewMessages(folder string) {
	w.mu.RLock()
	client := w.commandClient
	w.mu.RUnlock()
	if client == nil {
		return
	}

	// Get current folder status
	statusCmd := client.Status(folder, &imap.StatusOptions{
		NumMessages: true,
		UIDNext:     true,
	})
	status, err := statusCmd.Wait()
	if err != nil {
		w.logger.Error("Failed to get folder status for %s: %v", folder, err)
		return
	}

	currentMessageCount := uint32(0)
	currentHighestUID := uint32(0)

	if status.NumMessages != nil {
		currentMessageCount = uint32(*status.NumMessages)
	}
	if status.UIDNext != 0 {
		currentHighestUID = uint32(status.UIDNext) - 1 // UIDNext is the next UID to be assigned
	}

	w.mu.Lock()
	lastCount := w.lastMessageCount[folder]
	lastUID := w.lastHighestUID[folder]
	initialSyncDone := w.initialSyncDone[folder]

	// Always advance the message-count watermark so we don't re-trigger on the
	// same EXISTS count change. We deliberately defer updating lastHighestUID
	// until after a successful fetch: some servers (e.g. Gmail) push EXISTS
	// before the message is actually fetchable. If fetchNewMessages returns 0,
	// leaving lastHighestUID at its old value lets the next IDLE cycle retry.
	w.lastMessageCount[folder] = currentMessageCount
	w.mu.Unlock()

	// Save tracking state to cache when it changes
	go w.saveTrackingStateToCache()

	// Check for new messages (only after initial sync is complete)
	if initialSyncDone && (currentMessageCount > lastCount || currentHighestUID > lastUID) {

		// Detect potential tracking state reset (massive jump in message count from 0)
		// This prevents notification floods when tracking state is lost
		if lastCount == 0 && lastUID == 0 && currentMessageCount > 100 {
			w.logger.Warn("Detected potential tracking state reset for folder %s (count jumped from 0 to %d). Suppressing notifications to prevent flood.",
				folder, currentMessageCount)
			// Advance UID watermark to avoid replaying the flood on reconnect.
			w.mu.Lock()
			w.lastHighestUID[folder] = currentHighestUID
			w.mu.Unlock()
			return
		}

		w.logger.Info("New messages detected in folder %s: count %d->%d, highest UID %d->%d",
			folder, lastCount, currentMessageCount, lastUID, currentHighestUID)

		// Fetch the new messages
		newMessages := w.fetchNewMessages(folder, lastUID, currentHighestUID)
		if len(newMessages) > 0 {
			// Advance the UID watermark now that we have confirmed the messages exist.
			w.mu.Lock()
			w.lastHighestUID[folder] = currentHighestUID
			w.mu.Unlock()
			go w.saveTrackingStateToCache()

			if w.onNewMessage != nil {
				// Additional safety check: limit the number of notifications sent at once
				maxNotifications := MaxNewMessageNotifications
				notificationMessages := newMessages
				if len(newMessages) > maxNotifications {
					w.logger.Warn("Limiting notifications to %d messages (found %d new messages) to prevent spam",
						maxNotifications, len(newMessages))
					notificationMessages = newMessages[:maxNotifications]
				}

				// Create new message event
				event := NewMessageEvent{
					Folder:    folder,
					Messages:  notificationMessages,
					Count:     len(notificationMessages),
					Timestamp: time.Now(),
				}
				w.onNewMessage(event)
			}
		} else if currentHighestUID > lastUID {
			// fetchNewMessages returned nothing despite a UID change. This typically
			// means the server sent EXISTS before the message was fully delivered.
			// Leave lastHighestUID at the old value so the next IDLE cycle retries.
			w.logger.Debug("fetchNewMessages returned 0 results for folder %s (UID %d->%d); will retry on next IDLE cycle",
				folder, lastUID, currentHighestUID)
		}
	} else {
		// No new messages; advance the UID watermark to match current server state.
		w.mu.Lock()
		w.lastHighestUID[folder] = currentHighestUID
		w.mu.Unlock()
	}
}

// fetchNewMessages fetches messages with UIDs higher than the last known UID
func (w *IMAPWorker) fetchNewMessages(folder string, lastUID, currentHighestUID uint32) []email.Message {
	if lastUID >= currentHighestUID {
		return nil
	}

	w.mu.RLock()
	client := w.commandClient
	w.mu.RUnlock()
	if client == nil {
		return nil
	}

	// Select folder if not already selected
	if w.selectedFolder != folder {
		if _, err := client.Select(folder, nil).Wait(); err != nil {
			w.logger.Error("Failed to select folder %s for new message fetch: %v", folder, err)
			return nil
		}
		w.mu.Lock()
		w.selectedFolder = folder
		w.mu.Unlock()
	}

	// Create UID set for new messages
	var uidSet imap.UIDSet
	uidSet = append(uidSet, imap.UIDRange{Start: imap.UID(lastUID + 1), Stop: imap.UID(currentHighestUID)})

	fetchOptions := &imap.FetchOptions{
		Envelope:     true,
		Flags:        true,
		UID:          true,
		RFC822Size:   true,
		InternalDate: true, // Fetch actual arrival date from server
	}

	fetchCmd := client.Fetch(uidSet, fetchOptions)

	var newMessages []email.Message
	for {
		msg := fetchCmd.Next()
		if msg == nil {
			break
		}

		emailMsg := w.convertFetchDataToMessage(msg)
		newMessages = append(newMessages, emailMsg)
	}

	// Single explicit close; no defer to avoid a double-close.
	if err := fetchCmd.Close(); err != nil {
		w.logger.Error("Failed to fetch new messages: %v", err)
		return nil
	}

	w.logger.Debug("Fetched %d new messages from folder %s", len(newMessages), folder)
	return newMessages
}

// TrackingState represents the persistent tracking state for a folder
type TrackingState struct {
	LastMessageCount uint32    `json:"last_message_count"`
	LastHighestUID   uint32    `json:"last_highest_uid"`
	InitialSyncDone  bool      `json:"initial_sync_done"`
	LastUpdated      time.Time `json:"last_updated"`
}

// loadTrackingStateFromCache loads persistent tracking state from cache
func (w *IMAPWorker) loadTrackingStateFromCache() {
	if w.cache == nil {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// Load tracking state for each folder from cache
	cacheKey := w.getCacheKey("tracking_state", "all_folders")
	data, found, err := w.cache.Get(cacheKey)
	if err != nil || !found {
		w.logger.Warn("No cached tracking state found for account %s: found=%t, err=%v. This may cause notification floods if messages exist.",
			w.accountName, found, err)
		return
	}

	var trackingStates map[string]TrackingState
	if err := json.Unmarshal(data, &trackingStates); err != nil {
		w.logger.Warn("Failed to unmarshal tracking state from cache: %v", err)
		return
	}

	// Restore tracking state for each folder
	for folder, state := range trackingStates {
		// Extended expiration time to 7 days to prevent tracking state loss
		// This is more forgiving for users who don't use the app daily
		if time.Since(state.LastUpdated) < 7*24*time.Hour {
			w.lastMessageCount[folder] = state.LastMessageCount
			w.lastHighestUID[folder] = state.LastHighestUID
			w.initialSyncDone[folder] = state.InitialSyncDone
			w.logger.Debug("Restored tracking state for folder %s: count=%d, uid=%d, synced=%t",
				folder, state.LastMessageCount, state.LastHighestUID, state.InitialSyncDone)
		} else {
			w.logger.Warn("Cached tracking state for folder %s is too old (%v), will perform safe initialization",
				folder, time.Since(state.LastUpdated))
			// Don't completely ignore old state - mark as needing safe initialization
			w.initialSyncDone[folder] = false
		}
	}
}

// saveTrackingStateToCache saves persistent tracking state to cache
func (w *IMAPWorker) saveTrackingStateToCache() {
	if w.cache == nil {
		return
	}

	w.mu.RLock()
	trackingStates := make(map[string]TrackingState)
	now := time.Now()

	// Create tracking state for each folder
	for folder := range w.lastMessageCount {
		trackingStates[folder] = TrackingState{
			LastMessageCount: w.lastMessageCount[folder],
			LastHighestUID:   w.lastHighestUID[folder],
			InitialSyncDone:  w.initialSyncDone[folder],
			LastUpdated:      now,
		}
	}
	w.mu.RUnlock()

	// Marshal and save to cache
	data, err := json.Marshal(trackingStates)
	if err != nil {
		w.logger.Warn("Failed to marshal tracking state: %v", err)
		return
	}

	cacheKey := w.getCacheKey("tracking_state", "all_folders")
	// Increase cache expiration to 30 days to make tracking state more persistent
	// This prevents tracking state loss for users who don't use the app regularly
	if err := w.cache.Set(cacheKey, data, 30*24*time.Hour); err != nil {
		w.logger.Warn("Failed to save tracking state to cache: %v", err)
	} else {
		w.logger.Debug("Saved tracking state to cache for %d folders", len(trackingStates))
	}
}

// MarkInitialSyncComplete marks the initial sync as complete for a folder
func (w *IMAPWorker) MarkInitialSyncComplete(folder string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.initialSyncDone[folder] = true
	w.logger.Debug("Marked initial sync complete for folder: %s", folder)

	// Save tracking state to cache when initial sync is complete
	go w.saveTrackingStateToCache()
}

// IsIDLEActive returns whether IDLE monitoring is currently active
func (w *IMAPWorker) IsIDLEActive() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.idleActive
}

// IsIDLEPaused returns whether IDLE monitoring is currently paused
func (w *IMAPWorker) IsIDLEPaused() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.idlePaused
}

// GetIDLEFolder returns the folder currently being monitored by IDLE
func (w *IMAPWorker) GetIDLEFolder() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.idleFolder
}

// GetState returns the current connection state
func (w *IMAPWorker) GetState() ConnectionState {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.state
}

// getHealthTicker returns the health check ticker channel, or nil if not active
func (w *IMAPWorker) getHealthTicker() <-chan time.Time {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.healthTicker != nil {
		return w.healthTicker.C
	}
	return nil
}

// performHealthCheck checks the connection health and initiates reconnection if needed
func (w *IMAPWorker) performHealthCheck() {
	w.mu.Lock()
	w.lastHealthCheck = time.Now()
	w.mu.Unlock()

	// Skip health check if we're already reconnecting or connecting
	if w.state == ConnectionStateReconnecting || w.state == ConnectionStateConnecting {
		return
	}

	// Skip health check if not connected
	if w.commandClient == nil {
		return
	}

	w.logger.Debug("Performing connection health check via command channel")

	// Queue health check command instead of calling client directly
	healthCmd := NewCommand(CmdHealthCheck, map[string]interface{}{})
	healthCmd.ResponseCh = make(chan *IMAPResponse, 1)

	select {
	case w.commandCh <- healthCmd:
		// Wait for response
		// Note: We don't trigger reconnection here because handleHealthCheck() already does it
		// This prevents duplicate reconnection attempts
		select {
		case response := <-healthCmd.ResponseCh:
			if !response.Success {
				w.logger.Debug("Connection health check failed (reconnection will be handled by handleHealthCheck)")
			} else {
				// Connection is healthy
				if w.state != ConnectionStateConnected {
					w.setState(ConnectionStateConnected, nil)
				}
				w.logger.Debug("Connection health check passed")
			}
		case <-time.After(5 * time.Second):
			w.logger.Warn("Health check command timed out (command may still be processing)")
		}
	default:
		w.logger.Debug("Command channel full, skipping health check")
	}
}

// isConnectionHealthy tests if the current IMAP connection is working
func (w *IMAPWorker) isConnectionHealthy() bool {
	if w.commandClient == nil {
		return false
	}

	// Try a simple NOOP command to test the connection
	cmd := w.commandClient.Noop()
	err := cmd.Wait()
	if err != nil {
		w.logger.Debug("Connection health check failed: %v", err)
		w.incrementErrorCount()
		return false
	}

	return true
}

// initiateReconnection starts the reconnection process
func (w *IMAPWorker) initiateReconnection() {
	w.logger.Debug("Initiating reconnection for account: %s", w.accountName)
	w.setState(ConnectionStateReconnecting, nil)
	w.mu.Lock()
	w.reconnectAttempt = 0
	w.mu.Unlock()

	// Start reconnection in a separate goroutine to avoid blocking the event loop
	go w.reconnectionLoop()
}

// reconnectionLoop handles the reconnection attempts with exponential backoff
func (w *IMAPWorker) reconnectionLoop() {
	// Capture IDLE state now, before reconnection clears it, so we can
	// restart monitoring and detect messages that arrived while offline.
	w.mu.RLock()
	wasIDLEActive := w.idleActive
	savedIDLEFolder := w.idleFolder
	w.mu.RUnlock()

	for {
		select {
		case <-w.ctx.Done():
			return
		default:
		}

		w.mu.Lock()
		w.reconnectAttempt++
		currentAttempt := w.reconnectAttempt
		w.mu.Unlock()

		w.logger.Debug("Reconnection attempt %d for account: %s (will retry indefinitely)", currentAttempt, w.accountName)

		// Attempt to reconnect
		if err := w.attemptReconnection(); err != nil {
			w.logger.Error("Reconnection attempt %d failed: %v", currentAttempt, err)

			// Calculate backoff delay with exponential backoff
			delay := w.calculateBackoffDelay(currentAttempt)
			w.logger.Debug("Waiting %v before next reconnection attempt", delay)

			select {
			case <-time.After(delay):
				continue
			case <-w.ctx.Done():
				return
			}
		} else {
			// Reconnection successful
			w.logger.Debug("Successfully reconnected to IMAP server for account: %s", w.accountName)
			w.setState(ConnectionStateConnected, nil)

			// Reset reconnection attempt counter
			w.mu.Lock()
			w.reconnectAttempt = 0
			w.mu.Unlock()

			// Trigger reconnected callback
			if w.onConnectionStateChange != nil {
				event := ConnectionEvent{
					State:   ConnectionStateConnected,
					Error:   nil,
					Attempt: currentAttempt,
				}
				go w.onConnectionStateChange(event)
			}

			// Determine which folder to monitor.
			folderToMonitor := savedIDLEFolder
			if folderToMonitor == "" {
				folderToMonitor = "INBOX"
			}
			w.logger.Info("Restarting IDLE monitoring for folder %s after reconnection (was active before: %v)", folderToMonitor, wasIDLEActive)

			// Restart IDLE via the command channel so it runs on the event loop,
			// then immediately check for messages that arrived while disconnected.
			startIDLECmd := NewStartIDLECommand(folderToMonitor)
			startIDLECmd.ResponseCh = make(chan *IMAPResponse, 1)
			idleCtx, idleCancel := context.WithTimeout(context.Background(), 10*time.Second)
			startIDLECmd.Context = idleCtx

			select {
			case w.commandCh <- startIDLECmd:
				select {
				case resp := <-startIDLECmd.ResponseCh:
					idleCancel()
					if resp.Success {
						w.logger.Info("Successfully restarted IDLE monitoring for %s after reconnection", folderToMonitor)
					} else {
						w.logger.Error("Failed to restart IDLE for %s after reconnection: %v", folderToMonitor, resp.Error)
					}
					// Always check for new messages regardless of IDLE restart outcome –
					// messages may have arrived while the connection was down.
					w.logger.Info("Checking for messages that arrived while disconnected for folder: %s", folderToMonitor)
					w.sendCheckNewMessagesCommand(folderToMonitor)
				case <-idleCtx.Done():
					idleCancel()
					w.logger.Error("Timeout waiting for IDLE restart for %s after reconnection", folderToMonitor)
					w.sendCheckNewMessagesCommand(folderToMonitor)
				case <-w.ctx.Done():
					idleCancel()
				}
			case <-w.ctx.Done():
				idleCancel()
			case <-time.After(5 * time.Second):
				idleCancel()
				w.logger.Error("Failed to queue IDLE start command for %s after reconnection (channel full)", folderToMonitor)
				w.sendCheckNewMessagesCommand(folderToMonitor)
			}

			return
		}
	}
}

// attemptReconnection performs a single reconnection attempt
func (w *IMAPWorker) attemptReconnection() error {
	// Force disconnect first to clean up any stale connections
	w.forceDisconnectInternal()

	// Brief pause for cleanup
	select {
	case <-time.After(100 * time.Millisecond):
	case <-w.ctx.Done():
		return fmt.Errorf("worker stopped during reconnection")
	}

	// Attempt to reconnect using the connect handler logic
	connectCmd := NewCommand(CmdConnect, nil)
	connectCmd.ID = "internal-reconnect"
	connectCmd.ResponseCh = make(chan *IMAPResponse, 1)

	response := w.handleConnect(connectCmd)
	if !response.Success {
		return response.Error
	}

	return nil
}

// calculateBackoffDelay calculates the backoff delay for reconnection attempts
func (w *IMAPWorker) calculateBackoffDelay(attempt int) time.Duration {
	// Exponential backoff: delay = reconnectDelay * 2^(attempt-1)
	delay := w.reconnectDelay * time.Duration(1<<uint(attempt-1))

	// Cap at maxReconnectDelay
	if delay > w.maxReconnectDelay {
		delay = w.maxReconnectDelay
	}

	return delay
}

// forceDisconnectInternal forcibly closes the connection without proper cleanup
func (w *IMAPWorker) forceDisconnectInternal() {
	w.logger.Debug("Force disconnecting from IMAP server")

	// Stop IDLE monitoring if active
	w.stopIDLEInternal()

	w.mu.Lock()
	if w.commandClient != nil {
		w.commandClient.Close()
		w.commandClient = nil
	}
	if w.idleClient != nil {
		w.idleClient.Close()
		w.idleClient = nil
	}
	w.selectedFolder = ""
	w.mu.Unlock()

	w.setState(ConnectionStateDisconnected, nil)
}

// SetHealthCheckInterval configures the health check interval
func (w *IMAPWorker) SetHealthCheckInterval(interval time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.healthCheckInterval = interval

	// Restart the ticker if it's running
	if w.healthTicker != nil {
		w.healthTicker.Stop()
		w.healthTicker = time.NewTicker(interval)
	}
}

// SetReconnectConfig configures reconnection parameters
func (w *IMAPWorker) SetReconnectConfig(delay, maxDelay time.Duration, maxAttempts int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.reconnectDelay = delay
	w.maxReconnectDelay = maxDelay
	w.maxReconnectAttempts = maxAttempts
}

// GetHealthStatus returns the current health status
func (w *IMAPWorker) GetHealthStatus() map[string]interface{} {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return map[string]interface{}{
		"state":                  w.state.String(),
		"last_health_check":      w.lastHealthCheck,
		"reconnect_attempt":      w.reconnectAttempt,
		"max_reconnect_attempts": w.maxReconnectAttempts,
		"health_check_interval":  w.healthCheckInterval,
		"error_count":            w.errorCount,
	}
}

// handleFetchMessage handles the fetch single message command
func (w *IMAPWorker) handleFetchMessage(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling fetch message command")

	// Check if command context is cancelled before starting
	select {
	case <-cmd.Context.Done():
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("command cancelled before processing"))
	default:
	}

	if w.commandClient == nil {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("not connected"))
	}

	folderName, ok := cmd.GetString(ParamFolderName)
	if !ok {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("folder name not provided"))
	}

	uid, ok := cmd.GetUint32(ParamUID)
	if !ok {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("message UID not provided"))
	}

	w.logger.Debug("Fetching message UID %d from folder: %s", uid, folderName)

	// No need to pause IDLE - we have separate connections now

	// Select folder if not already selected
	if w.selectedFolder != folderName {
		w.logger.Debug("Selecting folder %s (currently selected: %s)", folderName, w.selectedFolder)
		selectData, err := w.commandClient.Select(folderName, nil).Wait()
		if err != nil {
			w.logger.Error("Failed to select folder %s: %v", folderName, err)
			w.incrementErrorCount()
			// Check if this is a connection error
			if w.isConnectionError(err) {
				w.logger.Warn("Connection error during folder selection, initiating reconnection")
				w.initiateReconnection()
				return NewResponse(cmd.ID, false, nil, fmt.Errorf("connection error during folder selection: %w", err))
			}
			return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to select folder: %w", err))
		}
		w.logger.Debug("Successfully selected folder %s, messages: %d, recent: %d", folderName, selectData.NumMessages, selectData.NumRecent)
		w.mu.Lock()
		w.selectedFolder = folderName
		w.mu.Unlock()
	}

	// Check context before starting fetch
	select {
	case <-cmd.Context.Done():
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("command cancelled before fetch"))
	default:
	}

	// Validate connection state before fetch
	if w.commandClient == nil {
		w.logger.Error("Command client is nil when trying to fetch message UID %d", uid)
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("connection not available"))
	}

	// Test connection health with a quick NOOP before the expensive fetch
	w.logger.Debug("Testing connection health before fetching message UID %d", uid)
	noopCmd := w.commandClient.Noop()
	if err := noopCmd.Wait(); err != nil {
		w.logger.Error("Connection health check failed before fetch: %v", err)
		if w.isConnectionError(err) {
			w.initiateReconnection()
			return NewResponse(cmd.ID, false, nil, fmt.Errorf("connection error before fetch: %w", err))
		}
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("connection test failed: %w", err))
	}
	w.logger.Debug("Connection health check passed for UID %d", uid)

	// Create UID set for single message
	uidSet := imap.UIDSetNum(imap.UID(uid))
	w.logger.Debug("Created UID set for message %d: %v", uid, uidSet)

	// Fetch message with full content
	w.logger.Debug("Starting IMAP fetch command for UID %d", uid)
	fetchCmd := w.commandClient.Fetch(uidSet, &imap.FetchOptions{
		Envelope:     true,
		Flags:        true,
		UID:          true,
		RFC822Size:   true,
		InternalDate: true, // Fetch actual arrival date from server
		BodySection: []*imap.FetchItemBodySection{
			{},                                    // Fetch the entire message body
			{Specifier: imap.PartSpecifierHeader}, // Explicitly fetch all headers
		},
	})
	w.logger.Debug("IMAP fetch command created successfully for UID %d", uid)

	// Use a goroutine to handle the fetch with context cancellation
	type fetchResult struct {
		emailMsg *email.Message
		err      error
	}

	resultCh := make(chan fetchResult, 1)
	go func() {
		defer func() {
			if err := fetchCmd.Close(); err != nil {
				w.logger.Warn("Error closing fetch command: %v", err)
				// Check if close error indicates connection issues
				if w.isConnectionError(err) {
					w.logger.Debug("Fetch command close error indicates connection problem")
				}
			}
		}()

		w.logger.Debug("Calling fetchCmd.Next() for UID %d", uid)
		msg := fetchCmd.Next()
		if msg == nil {
			w.logger.Debug("fetchCmd.Next() returned nil for UID %d", uid)
			resultCh <- fetchResult{nil, fmt.Errorf("message with UID %d not found", uid)}
			return
		}

		w.logger.Debug("fetchCmd.Next() returned message data for UID %d, SeqNum: %d", uid, msg.SeqNum)

		// Convert IMMEDIATELY inside the goroutine (like working client.go)
		w.logger.Debug("Starting message conversion for UID %d", uid)
		emailMsg := w.convertFetchDataToMessage(msg)
		w.logger.Debug("Message conversion completed for UID %d, result UID: %d", uid, emailMsg.UID)

		// Ensure the UID is preserved even if fetch items don't include it
		if emailMsg.UID == 0 {
			w.logger.Warn("Message UID was 0 after conversion, setting to requested UID %d", uid)
			emailMsg.UID = uid
			emailMsg.ID = fmt.Sprintf("%d", uid)
		}

		resultCh <- fetchResult{&emailMsg, nil}
	}()

	// Wait for fetch result or context cancellation
	select {
	case result := <-resultCh:
		if result.err != nil {
			// Check if this is a connection error that requires reconnection
			if w.isConnectionError(result.err) {
				w.logger.Warn("Connection error detected during fetch: %v", result.err)
				w.initiateReconnection()
				return NewResponse(cmd.ID, false, nil, fmt.Errorf("connection error, reconnection initiated: %w", result.err))
			}
			return NewResponse(cmd.ID, false, nil, result.err)
		}

		// Message was already converted in the goroutine
		emailMsg := *result.emailMsg
		w.logger.Debug("Successfully fetched message UID %d from folder: %s", uid, folderName)
		return NewResponse(cmd.ID, true, map[string]interface{}{
			DataMessage: emailMsg,
		}, nil)

	case <-cmd.Context.Done():
		w.logger.Warn("Fetch command cancelled for message UID %d", uid)
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("command cancelled during fetch"))
	}
}

// handleGetCachedMessages handles the get cached messages command
func (w *IMAPWorker) handleGetCachedMessages(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling get cached messages command")

	folderName, ok := cmd.GetString(ParamFolderName)
	if !ok {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("folder name not provided"))
	}

	w.logger.Debug("Getting cached messages from folder: %s", folderName)

	// Get cached messages using the cache instance
	messages, found, err := w.getCachedMessages(folderName)
	if err != nil {
		w.logger.Error("Failed to get cached messages for folder %s: %v", folderName, err)
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to get cached messages: %w", err))
	}

	if !found {
		w.logger.Debug("No cached messages found for folder: %s", folderName)
		return NewResponse(cmd.ID, false, map[string]interface{}{
			DataMessages: []email.Message{},
			DataFound:    false,
		}, nil)
	}

	w.logger.Debug("Found %d cached messages for folder: %s", len(messages), folderName)
	return NewResponse(cmd.ID, true, map[string]interface{}{
		DataMessages: messages,
		DataFound:    true,
	}, nil)
}

// handleMarkAsRead handles the mark as read command
func (w *IMAPWorker) handleMarkAsRead(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling mark as read command")

	if w.commandClient == nil {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("not connected"))
	}

	folderName, ok := cmd.GetString(ParamFolderName)
	if !ok {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("folder name not provided"))
	}

	uid, ok := cmd.GetUint32(ParamUID)
	if !ok {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("message UID not provided"))
	}

	w.logger.Debug("Marking message UID %d as read in folder: %s", uid, folderName)

	// Select folder if not already selected
	if w.selectedFolder != folderName {
		_, err := w.commandClient.Select(folderName, nil).Wait()
		if err != nil {
			w.logger.Error("Failed to select folder %s: %v", folderName, err)
			w.incrementErrorCount()
			return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to select folder: %w", err))
		}
		w.mu.Lock()
		w.selectedFolder = folderName
		w.mu.Unlock()
	}

	// Create UID set for single message
	uidSet := imap.UIDSetNum(imap.UID(uid))

	// Add \Seen flag
	storeCmd := w.commandClient.Store(uidSet, &imap.StoreFlags{
		Op:    imap.StoreFlagsAdd,
		Flags: []imap.Flag{imap.FlagSeen},
	}, nil)

	// Consume any returned data (we don't need it for flag operations)
	for storeCmd.Next() != nil {
		// Just consume the data
	}

	if err := storeCmd.Close(); err != nil {
		w.logger.Error("Failed to mark message UID %d as read: %v", uid, err)
		w.incrementErrorCount()
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to mark message as read: %w", err))
	}

	w.logger.Debug("Successfully marked message UID %d as read", uid)
	return NewResponse(cmd.ID, true, nil, nil)
}

// handleMarkAsUnread handles the mark as unread command
func (w *IMAPWorker) handleMarkAsUnread(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling mark as unread command")

	if w.commandClient == nil {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("not connected"))
	}

	folderName, ok := cmd.GetString(ParamFolderName)
	if !ok {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("folder name not provided"))
	}

	uid, ok := cmd.GetUint32(ParamUID)
	if !ok {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("message UID not provided"))
	}

	w.logger.Debug("Marking message UID %d as unread in folder: %s", uid, folderName)

	// Select folder if not already selected
	if w.selectedFolder != folderName {
		_, err := w.commandClient.Select(folderName, nil).Wait()
		if err != nil {
			w.logger.Error("Failed to select folder %s: %v", folderName, err)
			w.incrementErrorCount()
			return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to select folder: %w", err))
		}
		w.mu.Lock()
		w.selectedFolder = folderName
		w.mu.Unlock()
	}

	// Create UID set for single message
	uidSet := imap.UIDSetNum(imap.UID(uid))

	// Remove \Seen flag
	storeCmd := w.commandClient.Store(uidSet, &imap.StoreFlags{
		Op:    imap.StoreFlagsDel,
		Flags: []imap.Flag{imap.FlagSeen},
	}, nil)

	// Consume any returned data
	for storeCmd.Next() != nil {
		// Just consume the data
	}

	if err := storeCmd.Close(); err != nil {
		w.logger.Error("Failed to mark message UID %d as unread: %v", uid, err)
		w.incrementErrorCount()
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to mark message as unread: %w", err))
	}

	w.logger.Debug("Successfully marked message UID %d as unread", uid)
	return NewResponse(cmd.ID, true, nil, nil)
}

// handleDeleteMessage handles the delete message command
func (w *IMAPWorker) handleDeleteMessage(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling delete message command")

	if w.commandClient == nil {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("not connected"))
	}

	folderName, ok := cmd.GetString(ParamFolderName)
	if !ok {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("folder name not provided"))
	}

	uid, ok := cmd.GetUint32(ParamUID)
	if !ok {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("message UID not provided"))
	}

	w.logger.Debug("Deleting message UID %d from folder: %s", uid, folderName)

	// Select folder if not already selected
	if w.selectedFolder != folderName {
		_, err := w.commandClient.Select(folderName, nil).Wait()
		if err != nil {
			w.logger.Error("Failed to select folder %s: %v", folderName, err)
			w.incrementErrorCount()
			return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to select folder: %w", err))
		}
		w.mu.Lock()
		w.selectedFolder = folderName
		w.mu.Unlock()
	}

	// Create UID set for single message
	uidSet := imap.UIDSetNum(imap.UID(uid))

	// Add \Deleted flag
	storeCmd := w.commandClient.Store(uidSet, &imap.StoreFlags{
		Op:    imap.StoreFlagsAdd,
		Flags: []imap.Flag{imap.FlagDeleted},
	}, nil)

	// Consume any returned data
	for storeCmd.Next() != nil {
		// Just consume the data
	}

	if err := storeCmd.Close(); err != nil {
		w.logger.Error("Failed to delete message UID %d: %v", uid, err)
		w.incrementErrorCount()
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to delete message: %w", err))
	}

	// Expunge the message to actually delete it
	expungeCmd := w.commandClient.UIDExpunge(uidSet)

	// Consume any returned data
	for expungeCmd.Next() != 0 {
		// Just consume the data
	}

	if err := expungeCmd.Close(); err != nil {
		w.logger.Warn("Failed to expunge after delete: %v", err)
		// Don't fail the operation, just warn
	}

	w.logger.Debug("Successfully deleted message UID %d", uid)
	return NewResponse(cmd.ID, true, nil, nil)
}

// handleMoveMessage handles the move message command
func (w *IMAPWorker) handleMoveMessage(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling move message command")

	if w.commandClient == nil {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("not connected"))
	}

	folderName, ok := cmd.GetString(ParamFolderName)
	if !ok {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("folder name not provided"))
	}

	uid, ok := cmd.GetUint32(ParamUID)
	if !ok {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("message UID not provided"))
	}

	targetFolder, ok := cmd.GetString(ParamTargetFolder)
	if !ok {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("target folder not provided"))
	}

	w.logger.Debug("Moving message UID %d from folder %s to %s", uid, folderName, targetFolder)

	// Select source folder if not already selected
	if w.selectedFolder != folderName {
		_, err := w.commandClient.Select(folderName, nil).Wait()
		if err != nil {
			w.logger.Error("Failed to select folder %s: %v", folderName, err)
			w.incrementErrorCount()
			return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to select folder: %w", err))
		}
		w.mu.Lock()
		w.selectedFolder = folderName
		w.mu.Unlock()
	}

	// Create UID set for single message
	uidSet := imap.UIDSetNum(imap.UID(uid))

	// Move the message
	moveCmd := w.commandClient.Move(uidSet, targetFolder)

	// Wait for move to complete
	_, err := moveCmd.Wait()
	if err != nil {
		w.logger.Error("Failed to move message UID %d: %v", uid, err)
		w.incrementErrorCount()
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to move message: %w", err))
	}

	w.logger.Debug("Successfully moved message UID %d to folder %s", uid, targetFolder)
	return NewResponse(cmd.ID, true, nil, nil)
}

// handleSearchMessages handles the search messages command
func (w *IMAPWorker) handleSearchMessages(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling search messages command")

	if w.commandClient == nil {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("not connected"))
	}

	folderName, hasFolder := cmd.GetString(ParamFolderName)
	criteria, ok := cmd.GetSearchCriteria(ParamCriteria)
	if !ok || criteria == nil {
		searchTerm, _ := cmd.GetString(ParamSearchTerm)
		criteria = &email.SearchCriteria{Content: searchTerm}
	}

	desc := w.describeSearchCriteria(criteria)

	if hasFolder && folderName != "" && !strings.EqualFold(folderName, "All Folders") {
		w.logger.Info("Search request folder=%s criteria=%s max=%d server=%v", folderName, desc, criteria.MaxResults, criteria.SearchServer)

		messages, err := w.searchMessagesInFolder(folderName, criteria, criteria.MaxResults)
		if err != nil {
			w.logger.Error("Failed to search folder %s: %v", folderName, err)
			w.incrementErrorCount()
			return NewResponse(cmd.ID, false, nil, err)
		}

		return NewResponse(cmd.ID, true, map[string]interface{}{
			DataMessages: messages,
		}, nil)
	}

	w.logger.Info("Search request all folders criteria=%s max=%d server=%v", desc, criteria.MaxResults, criteria.SearchServer)

	messages, err := w.searchMessagesAcrossFolders(criteria)
	if err != nil {
		w.logger.Error("Failed to search across folders: %v", err)
		w.incrementErrorCount()
		return NewResponse(cmd.ID, false, nil, err)
	}

	return NewResponse(cmd.ID, true, map[string]interface{}{
		DataMessages: messages,
	}, nil)
}

func (w *IMAPWorker) searchMessagesAcrossFolders(criteria *email.SearchCriteria) ([]email.Message, error) {
	folders, err := w.getSearchableFolderNames()
	if err != nil {
		return nil, err
	}

	if len(folders) == 0 {
		w.logger.Warn("Search aborted because no selectable folders were found")
		return []email.Message{}, nil
	}

	w.logger.Info("Search spanning %d folders: %s", len(folders), strings.Join(folders, ","))

	maxTotal := criteria.MaxResults
	remaining := maxTotal
	var allMessages []email.Message

	for _, folder := range folders {
		if maxTotal > 0 && remaining <= 0 {
			break
		}

		limit := 0
		if maxTotal > 0 {
			limit = remaining
		}

		w.logger.Debug("Searching folder %s with limit %d", folder, limit)

		folderMessages, err := w.searchMessagesInFolder(folder, criteria, limit)
		if err != nil {
			return nil, fmt.Errorf("failed to search folder %s: %w", folder, err)
		}

		w.logger.Info("Folder %s produced %d messages", folder, len(folderMessages))

		allMessages = append(allMessages, folderMessages...)
		if maxTotal > 0 {
			remaining -= len(folderMessages)
			if remaining <= 0 {
				break
			}
		}
	}

	w.logger.Info("Aggregated %d messages across folders", len(allMessages))

	return allMessages, nil
}

func (w *IMAPWorker) searchMessagesInFolder(folderName string, criteria *email.SearchCriteria, maxResults int) ([]email.Message, error) {
	if folderName == "" {
		return nil, fmt.Errorf("folder name not provided")
	}

	if w.selectedFolder != folderName {
		_, err := w.commandClient.Select(folderName, nil).Wait()
		if err != nil {
			return nil, fmt.Errorf("failed to select folder: %w", err)
		}
		w.mu.Lock()
		w.selectedFolder = folderName
		w.mu.Unlock()
		w.logger.Debug("Selected folder %s for search", folderName)
	}

	searchCriteria := w.buildIMAPSearchCriteria(criteria)
	searchCmd := w.commandClient.UIDSearch(searchCriteria, nil)
	searchData, err := searchCmd.Wait()
	if err != nil {
		return nil, fmt.Errorf("failed to search messages: %w", err)
	}

	uids := searchData.AllUIDs()
	w.logger.Debug("Search in folder %s found %d messages", folderName, len(uids))
	if len(uids) == 0 {
		w.logger.Info("Folder %s returned no matching UIDs", folderName)
		return []email.Message{}, nil
	}

	limit := maxResults
	if limit > 0 && len(uids) > limit {
		w.logger.Info("Folder %s limiting results from %d to %d based on MaxResults", folderName, len(uids), limit)
		uids = uids[len(uids)-limit:]
	}

	uidSet := imap.UIDSet{}
	for _, uid := range uids {
		uidSet.AddNum(uid)
	}

	fetchCmd := w.commandClient.Fetch(uidSet, &imap.FetchOptions{
		Envelope:     true,
		Flags:        true,
		UID:          true,
		RFC822Size:   true,
		InternalDate: true,
	})

	messages := make([]email.Message, 0, len(uids))
	for {
		msg := fetchCmd.Next()
		if msg == nil {
			break
		}
		messages = append(messages, w.convertFetchDataToMessage(msg))
	}

	if err := fetchCmd.Close(); err != nil {
		w.logger.Warn("Error closing fetch command: %v", err)
	}

	w.logger.Info("Folder %s returning %d hydrated messages", folderName, len(messages))

	return messages, nil
}

func (w *IMAPWorker) buildIMAPSearchCriteria(criteria *email.SearchCriteria) *imap.SearchCriteria {
	result := &imap.SearchCriteria{}
	if criteria == nil {
		return result
	}

	if criteria.From != "" {
		result.Header = append(result.Header, imap.SearchCriteriaHeaderField{Key: "From", Value: criteria.From})
	}
	if criteria.To != "" {
		result.Header = append(result.Header, imap.SearchCriteriaHeaderField{Key: "To", Value: criteria.To})
	}
	if criteria.Subject != "" {
		result.Header = append(result.Header, imap.SearchCriteriaHeaderField{Key: "Subject", Value: criteria.Subject})
	}
	if criteria.Content != "" {
		result.Body = []string{criteria.Content}
	}
	if criteria.DateFrom != nil {
		result.Since = *criteria.DateFrom
	}
	if criteria.DateTo != nil {
		result.Before = *criteria.DateTo
	}
	if criteria.UnreadOnly {
		result.NotFlag = append(result.NotFlag, imap.FlagSeen)
	}
	if criteria.MessageSize != nil {
		result.Larger = *criteria.MessageSize
	}
	for _, keyword := range criteria.Keywords {
		if keyword != "" {
			result.Flag = append(result.Flag, imap.Flag(keyword))
		}
	}

	return result
}

func (w *IMAPWorker) describeSearchCriteria(criteria *email.SearchCriteria) string {
	if criteria == nil {
		return "none"
	}

	parts := []string{}

	if criteria.From != "" {
		parts = append(parts, fmt.Sprintf("from=%s", criteria.From))
	}
	if criteria.To != "" {
		parts = append(parts, fmt.Sprintf("to=%s", criteria.To))
	}
	if criteria.Subject != "" {
		parts = append(parts, fmt.Sprintf("subject=%s", criteria.Subject))
	}
	if criteria.Content != "" {
		parts = append(parts, fmt.Sprintf("body~=%s", criteria.Content))
	}
	if criteria.DateFrom != nil {
		parts = append(parts, fmt.Sprintf("since=%s", criteria.DateFrom.Format(time.RFC3339)))
	}
	if criteria.DateTo != nil {
		parts = append(parts, fmt.Sprintf("before=%s", criteria.DateTo.Format(time.RFC3339)))
	}
	if criteria.HasAttachments {
		parts = append(parts, "attachments=true")
	}
	if criteria.UnreadOnly {
		parts = append(parts, "unread=true")
	}
	if criteria.MessageSize != nil {
		parts = append(parts, fmt.Sprintf("minSize=%d", *criteria.MessageSize))
	}
	if len(criteria.Keywords) > 0 {
		parts = append(parts, fmt.Sprintf("keywords=%d", len(criteria.Keywords)))
	}
	if criteria.CaseSensitive {
		parts = append(parts, "caseSensitive=true")
	}
	if criteria.UseRegex {
		parts = append(parts, "regex=true")
	}
	if criteria.SearchServer {
		parts = append(parts, "serverSearch=true")
	}

	if len(parts) == 0 {
		return "empty"
	}

	return strings.Join(parts, ",")
}

func (w *IMAPWorker) getSearchableFolderNames() ([]string, error) {
	if folders, found, err := w.getCachedSubscribedFolders(); err == nil && found {
		w.logger.Debug("Using %d cached subscribed folders for search", len(folders))
		names := w.filterSelectableFolders(folders)
		if len(names) > 0 {
			return names, nil
		}
		w.logger.Debug("Cached folders produced no selectable entries, falling back to server list")
	}

	if w.commandClient == nil {
		return nil, fmt.Errorf("not connected")
	}

	w.logger.Info("Listing subscribed folders from server for search")
	listCmd := w.commandClient.List("", "*", &imap.ListOptions{SelectSubscribed: true})
	mailboxes, err := listCmd.Collect()
	if err != nil {
		return nil, fmt.Errorf("failed to list folders: %w", err)
	}

	folders := make([]email.Folder, 0, len(mailboxes))
	names := make([]string, 0, len(mailboxes))
	for _, mb := range mailboxes {
		if mb == nil {
			continue
		}
		attrs := w.convertAttributes(mb.Attrs)
		delimiter := string(mb.Delim)
		if delimiter == "" {
			delimiter = "/"
		}
		folder := email.Folder{
			Name:       mb.Mailbox,
			Path:       mb.Mailbox,
			Delimiter:  delimiter,
			Attributes: attrs,
			Subscribed: true,
		}
		folders = append(folders, folder)
		if !w.folderHasNoSelect(attrs) {
			names = append(names, folder.Name)
		}
	}

	if err := w.cacheSubscribedFolders(folders); err != nil {
		w.logger.Warn("Failed to cache subscribed folders: %v", err)
	} else {
		w.logger.Debug("Cached %d subscribed folders for future searches", len(folders))
	}

	return names, nil
}

func (w *IMAPWorker) filterSelectableFolders(folders []email.Folder) []string {
	var names []string
	for _, folder := range folders {
		if folder.Name == "" {
			continue
		}
		if w.folderHasNoSelect(folder.Attributes) {
			continue
		}
		names = append(names, folder.Name)
	}
	return names
}

func (w *IMAPWorker) folderHasNoSelect(attrs []string) bool {
	for _, attr := range attrs {
		if strings.EqualFold(attr, string(imap.MailboxAttrNoSelect)) {
			return true
		}
	}
	return false
}

// handleStoreSentMessage handles the store sent message command
func (w *IMAPWorker) handleStoreSentMessage(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling store sent message command")

	if w.commandClient == nil {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("not connected"))
	}

	folderName, ok := cmd.GetString(ParamFolderName)
	if !ok {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("folder name not provided"))
	}

	// Try both parameter names for compatibility
	messageData, ok := cmd.GetString(ParamMessageContent)
	if !ok {
		messageData, ok = cmd.GetString(ParamMessageData)
		if !ok {
			return NewResponse(cmd.ID, false, nil, fmt.Errorf("message content not provided"))
		}
	}

	w.logger.Debug("Storing sent message in folder: %s", folderName)

	// Append the message to the folder
	messageSize := int64(len(messageData))
	appendOptions := &imap.AppendOptions{
		Flags: []imap.Flag{imap.FlagSeen},
		Time:  time.Now(),
	}

	appendCmd := w.commandClient.Append(folderName, messageSize, appendOptions)

	// Write the message data
	if _, err := appendCmd.Write([]byte(messageData)); err != nil {
		w.logger.Error("Failed to write message data: %v", err)
		w.incrementErrorCount()
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to write message data: %w", err))
	}

	err := appendCmd.Close()
	if err != nil {
		w.logger.Error("Failed to store sent message: %v", err)
		w.incrementErrorCount()
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to store sent message: %w", err))
	}

	w.logger.Debug("Successfully stored sent message in folder: %s", folderName)
	return NewResponse(cmd.ID, true, nil, nil)
}

// handleStartIDLE handles the start IDLE monitoring command
func (w *IMAPWorker) handleStartIDLE(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling start IDLE command")

	if w.idleClient == nil {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("not connected"))
	}

	folderName, ok := cmd.GetString(ParamFolderName)
	if !ok {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("folder name not provided"))
	}

	// Check if IDLE is already active
	if w.idleActive {
		if w.idleFolder == folderName {
			w.logger.Debug("IDLE already active for folder: %s", folderName)
			return NewResponse(cmd.ID, true, map[string]interface{}{
				DataIdleStatus: "already_active",
			}, nil)
		} else {
			// Stop current IDLE and start new one
			w.stopIDLEInternal()
		}
	}

	// Select folder on IDLE client (IDLE client needs its own folder selection)
	_, err := w.idleClient.Select(folderName, nil).Wait()
	if err != nil {
		w.logger.Error("Failed to select folder %s for IDLE: %v", folderName, err)
		w.incrementErrorCount()
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to select folder for IDLE: %w", err))
	}

	// Check server capabilities for IDLE support
	caps := w.idleClient.Caps()
	if !caps.Has(imap.CapIdle) {
		w.logger.Warn("Server does not support IDLE extension")
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("server does not support IDLE"))
	}

	// Initialize folder tracking if needed
	w.initializeFolderTracking(folderName)

	// Start IDLE monitoring
	if err := w.startIDLEInternal(folderName); err != nil {
		w.logger.Error("Failed to start IDLE monitoring: %v", err)
		w.incrementErrorCount()
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to start IDLE: %w", err))
	}

	w.logger.Debug("Started IDLE monitoring for folder: %s", folderName)
	return NewResponse(cmd.ID, true, map[string]interface{}{
		DataIdleStatus: "started",
	}, nil)
}

// handleStopIDLE handles the stop IDLE monitoring command
func (w *IMAPWorker) handleStopIDLE(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling stop IDLE command")

	if !w.idleActive {
		w.logger.Debug("IDLE is not active")
		return NewResponse(cmd.ID, true, map[string]interface{}{
			DataIdleStatus: "not_active",
		}, nil)
	}

	w.stopIDLEInternal()

	w.logger.Info("Stopped IDLE monitoring")
	return NewResponse(cmd.ID, true, map[string]interface{}{
		DataIdleStatus: "stopped",
	}, nil)
}

// handlePauseIDLE handles the pause IDLE monitoring command
func (w *IMAPWorker) handlePauseIDLE(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling pause IDLE command")

	if !w.idleActive {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("IDLE is not active"))
	}

	if w.idlePaused {
		w.logger.Debug("IDLE is already paused")
		return NewResponse(cmd.ID, true, map[string]interface{}{
			DataIdleStatus: "already_paused",
		}, nil)
	}

	w.pauseIDLEInternal()

	w.logger.Debug("Paused IDLE monitoring")
	return NewResponse(cmd.ID, true, map[string]interface{}{
		DataIdleStatus: "paused",
	}, nil)
}

// handleResumeIDLE handles the resume IDLE monitoring command
func (w *IMAPWorker) handleResumeIDLE(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling resume IDLE command")

	if !w.idleActive {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("IDLE is not active"))
	}

	if !w.idlePaused {
		w.logger.Debug("IDLE is not paused")
		return NewResponse(cmd.ID, true, map[string]interface{}{
			DataIdleStatus: "not_paused",
		}, nil)
	}

	if err := w.resumeIDLEInternal(); err != nil {
		w.logger.Error("Failed to resume IDLE monitoring: %v", err)
		w.incrementErrorCount()
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to resume IDLE: %w", err))
	}

	w.logger.Debug("Resumed IDLE monitoring")
	return NewResponse(cmd.ID, true, map[string]interface{}{
		DataIdleStatus: "resumed",
	}, nil)
}

// Cache helper methods

// getCacheKey generates a cache key for the given type and identifier
// Deprecated: Use cache.GenerateAccountKey instead
func (w *IMAPWorker) getCacheKey(keyType, identifier string) string {
	if w.cacheKey == "" {
		return cache.GenerateKey(keyType, identifier)
	}
	return cache.GenerateAccountKey(w.cacheKey, keyType, identifier)
}

// getCachedMessages retrieves messages from cache
func (w *IMAPWorker) getCachedMessages(folder string) ([]email.Message, bool, error) {
	if w.cache == nil {
		return nil, false, nil // No cache available
	}

	key := w.getCacheKey("messages", folder)
	data, found, err := w.cache.Get(key)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get cached messages: %w", err)
	}

	if !found {
		return nil, false, nil
	}

	var messages []email.Message
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, false, fmt.Errorf("failed to unmarshal cached messages: %w", err)
	}

	return messages, true, nil
}

// cacheMessages stores messages in cache
func (w *IMAPWorker) cacheMessages(folder string, messages []email.Message) error {
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

// getCachedFolders retrieves folders from cache
func (w *IMAPWorker) getCachedFolders() ([]email.Folder, bool, error) {
	if w.cache == nil {
		return nil, false, nil // No cache available
	}

	key := w.getCacheKey("folders", "list")
	data, found, err := w.cache.Get(key)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get cached folders: %w", err)
	}

	if !found {
		return nil, false, nil
	}

	var folders []email.Folder
	if err := json.Unmarshal(data, &folders); err != nil {
		return nil, false, fmt.Errorf("failed to unmarshal cached folders: %w", err)
	}

	return folders, true, nil
}

// cacheFolders stores folders in cache
func (w *IMAPWorker) cacheFolders(folders []email.Folder) error {
	if w.cache == nil {
		return nil // No cache available
	}

	data, err := json.Marshal(folders)
	if err != nil {
		return fmt.Errorf("failed to marshal folders: %w", err)
	}

	key := w.getCacheKey("folders", "list")
	return w.cache.Set(key, data, 24*time.Hour) // Cache folders for 24 hours
}

// getCachedSubscribedFolders retrieves subscribed folders from cache
func (w *IMAPWorker) getCachedSubscribedFolders() ([]email.Folder, bool, error) {
	if w.cache == nil {
		return nil, false, nil // No cache available
	}

	key := w.getCacheKey("folders", "subscribed")
	data, found, err := w.cache.Get(key)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get cached subscribed folders: %w", err)
	}

	if !found {
		return nil, false, nil
	}

	var folders []email.Folder
	if err := json.Unmarshal(data, &folders); err != nil {
		return nil, false, fmt.Errorf("failed to unmarshal cached subscribed folders: %w", err)
	}

	return folders, true, nil
}

// cacheSubscribedFolders stores subscribed folders in cache
func (w *IMAPWorker) cacheSubscribedFolders(folders []email.Folder) error {
	if w.cache == nil {
		return nil // No cache available
	}

	data, err := json.Marshal(folders)
	if err != nil {
		return fmt.Errorf("failed to marshal subscribed folders: %w", err)
	}

	key := w.getCacheKey("folders", "subscribed")
	return w.cache.Set(key, data, 24*time.Hour) // Cache subscribed folders for 24 hours
}
