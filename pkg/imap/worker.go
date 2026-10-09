package imap

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

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
	fetchClient   *imapclient.Client // Dedicated to bulk message fetches

	// fetchSelectedFolder tracks the folder selected on fetchClient (guarded
	// by mu). It is separate from selectedFolder, which tracks commandClient.
	fetchSelectedFolder string

	// Communication channels
	commandCh chan *IMAPCommand
	fetchCh   chan *IMAPCommand // Bulk fetch commands, processed by fetchLoop
	stopCh    chan struct{}

	// Worker state (protected by mutex for external access)
	mu                     sync.RWMutex
	state                  ConnectionState
	selectedFolder         string
	idleActive             bool
	idlePaused             bool
	idleFolder             string
	idleTimeout            time.Duration
	newMessageCheckTimeout time.Duration
	idleCmd                *imapclient.IdleCommand
	idleCancel             context.CancelFunc
	lastActivity           time.Time
	commandsQueued         int
	reconnectCount         int
	errorCount             int

	// IDLE monitoring state
	lastMessageCount map[string]uint32
	lastHighestUID   map[string]uint32
	initialSyncDone  map[string]bool
	lastIDLEActivity time.Time     // Track last time IDLE loop was confirmed running
	idleUpdateCh     chan struct{} // Signalled by UnilateralDataHandler when server pushes a mailbox update

	// New-message retry state: a UID change was observed but the message was
	// not fetchable yet (e.g. Gmail pushes EXISTS before the message can be
	// fetched). Tracks pending short-delay retries so detection doesn't wait
	// for the next full IDLE timeout.
	newMsgRetryCount     map[string]int
	newMsgRetryScheduled map[string]bool

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
	// bgWG tracks detached helper goroutines (reconnect attempts, IDLE
	// monitors) so Stop can wait for them to observe the cancelled context.
	bgWG sync.WaitGroup

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
	// NewMessageCheckTimeout bounds a new-message check (SELECT plus a small
	// envelope fetch). The check runs on the event loop with no command
	// context, so a stalled server would otherwise freeze every command for
	// the account; the watchdog closes the connection to unblock it, and the
	// resulting connection error triggers the normal reconnection path.
	NewMessageCheckTimeout = 60 * time.Second
	// MaxNewMessageRetries bounds the short-delay re-checks scheduled when a
	// new message was signalled but could not be fetched yet. After exhausting
	// the retries, the regular IDLE timeout remains as a backstop.
	MaxNewMessageRetries = 5
	// NewMessageRetryBaseDelay is the delay before the first short-delay
	// re-check of a folder whose new message was not fetchable yet. The delay
	// doubles on every retry (1s, 2s, 4s, ...).
	NewMessageRetryBaseDelay = 1 * time.Second
	// ReconnectionTimeout is the per-attempt timeout for automatic reconnection.
	ReconnectionTimeout = 30 * time.Second
)

// NewIMAPWorker creates a new IMAP worker for the specified account
func NewIMAPWorker(config *email.ServerConfig, accountName, cacheKey string, cache *cache.Cache) *IMAPWorker {
	worker := &IMAPWorker{
		config:       config,
		accountName:  accountName,
		cacheKey:     cacheKey,
		cache:        cache,
		commandCh:    make(chan *IMAPCommand, DefaultCommandChBuffer),
		fetchCh:      make(chan *IMAPCommand, DefaultCommandChBuffer),
		idleUpdateCh: make(chan struct{}, 1),
		// stopCh and ctx will be created in Start()
		state:                  ConnectionStateDisconnected,
		idleTimeout:            DefaultIDLETimeout,
		newMessageCheckTimeout: NewMessageCheckTimeout,
		lastMessageCount:       make(map[string]uint32),
		lastHighestUID:         make(map[string]uint32),
		initialSyncDone:        make(map[string]bool),
		newMsgRetryCount:       make(map[string]int),
		newMsgRetryScheduled:   make(map[string]bool),
		reconnectGracePeriod:   DefaultReconnectGracePeriod,
		healthCheckInterval:    DefaultHealthCheckInterval,
		reconnectDelay:         DefaultReconnectDelay,
		maxReconnectDelay:      MaxReconnectDelay,
		maxReconnectAttempts:   DefaultMaxReconnectAttempts,
		logger:                 logging.NewComponent(fmt.Sprintf("imap-worker-%s", accountName)),
		tracer:                 nil, // Will be set by SetTracer if tracing is enabled
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

	// Start the dedicated fetch loop for bulk fetch commands
	w.wg.Add(1)
	go w.fetchLoop()

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

	// commandCh and fetchCh are deliberately NOT closed: detached helper
	// goroutines may still send on them, and a send on a closed channel
	// panics. Both loops have exited by now, so nothing reads the channels
	// and they are simply garbage-collected once unreferenced.

	// Wait briefly for detached helpers to observe the cancelled context.
	done := make(chan struct{})
	go func() {
		w.bgWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		w.logger.Warn("Background goroutines did not stop within 3s for account: %s", w.accountName)
	}

	w.logger.Info("IMAP worker stopped for account: %s", w.accountName)
	return nil
}

func (w *IMAPWorker) StopFast() error {
	if w.stopCh == nil {
		return nil
	}

	select {
	case <-w.stopCh:
		return nil
	default:
	}

	w.logger.Debug("Force stopping IMAP worker for account: %s", w.accountName)

	w.cancel()
	close(w.stopCh)
	// commandCh is not closed: detached senders would panic; see Stop().

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

// fetchLoop processes bulk fetch commands off the main event loop. Bulk
// fetches can run for minutes on large mailboxes; keeping them off the event
// loop ensures quick commands (new-message checks, flag updates, monitoring)
// stay responsive while a transfer is in progress.
func (w *IMAPWorker) fetchLoop() {
	defer w.wg.Done()
	w.logger.Debug("Fetch loop started for account: %s", w.accountName)

	for {
		select {
		case cmd := <-w.fetchCh:
			if cmd == nil {
				return
			}
			response := w.handleFetchMessages(cmd)
			w.sendResponse(cmd, response)

		case <-w.stopCh:
			w.logger.Debug("Fetch loop stopping for account: %s", w.accountName)
			w.drainFetchQueue()
			return

		case <-w.ctx.Done():
			w.logger.Debug("Fetch loop context cancelled for account: %s", w.accountName)
			// Drain queued fetch commands with an error so waiters aren't
			// stuck until their own timeout fires.
			w.drainFetchQueue()
			return
		}
	}
}

// drainFetchQueue responds to all queued bulk-fetch commands with a shutdown
// error so callers waiting on their response channels return promptly.
func (w *IMAPWorker) drainFetchQueue() {
	for {
		select {
		case cmd := <-w.fetchCh:
			if cmd == nil {
				return
			}
			w.sendResponse(cmd, NewResponse(cmd.ID, false, nil, fmt.Errorf("worker shutting down")))
		default:
			return
		}
	}
}

// bgGo runs fn on a tracked background goroutine so Stop can wait for it.
func (w *IMAPWorker) bgGo(fn func()) {
	w.bgWG.Add(1)
	go func() {
		defer w.bgWG.Done()
		fn()
	}()
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
		// Dispatch bulk fetches to the fetch loop; the response is sent
		// asynchronously so the event loop stays responsive.
		select {
		case w.fetchCh <- cmd:
		case <-cmd.Context.Done():
			w.sendResponse(cmd, NewResponse(cmd.ID, false, nil, fmt.Errorf("command timeout while queuing fetch")))
		case <-w.ctx.Done():
			w.sendResponse(cmd, NewResponse(cmd.ID, false, nil, fmt.Errorf("worker shutting down")))
		}
		response = nil // response handled asynchronously by fetchLoop
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

	// Send response back; nil response means the command was handled
	// asynchronously (e.g. dispatched to the fetch loop).
	if response != nil {
		w.sendResponse(cmd, response)
	}
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

	// Surface the reconnecting state immediately so the UI reflects the
	// outage during the backoff window before CmdReconnect runs.
	w.setState(ConnectionStateReconnecting, nil)

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
			w.mu.Lock()
			nextDelay := w.reconnectDelay
			for i := 0; i < w.reconnectAttempt; i++ {
				nextDelay *= 2
				if nextDelay > w.maxReconnectDelay {
					nextDelay = w.maxReconnectDelay
					break
				}
			}
			w.mu.Unlock()
			w.logger.Info("Scheduling next automatic reconnection attempt in %v after timeout", nextDelay)
			time.AfterFunc(nextDelay, func() {
				w.bgGo(w.attemptAutomaticReconnection)
			})
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
			w.bgGo(w.attemptAutomaticReconnection)
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
			w.bgGo(w.attemptAutomaticReconnection)
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
	if w.fetchClient != nil {
		w.logger.Debug("Disconnecting fetch client during cleanup")
		if err := w.fetchClient.Close(); err != nil {
			w.logger.Debug("Error closing fetch client during cleanup: %v", err)
		}
		w.fetchClient = nil
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

// Command handlers

// TrackingState represents the persistent tracking state for a folder
type TrackingState struct {
	LastMessageCount uint32    `json:"last_message_count"`
	LastHighestUID   uint32    `json:"last_highest_uid"`
	InitialSyncDone  bool      `json:"initial_sync_done"`
	LastUpdated      time.Time `json:"last_updated"`
}

// Cache helper methods
