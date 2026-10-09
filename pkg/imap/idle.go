package imap

// Code in this file was mechanically split from worker.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/wltechblog/gommail/internal/email"
)

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
	w.bgGo(func() { w.idleMonitorLoop(ctx, idleCmd, folder) })

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
	w.bgGo(func() { w.idleMonitorLoop(ctx, idleCmd, w.idleFolder) })

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
		// while we were IDLing. Terminate IDLE immediately, then queue a new-message
		// check. The check runs on the event loop so its SELECT/fetch on the command
		// client is serialized with all other commands (no pipelining races).
		w.logger.Debug("IDLE: received server push notification for folder: %s, terminating IDLE to fetch updates", folder)
		if err := idleCmd.Close(); err != nil {
			// "already closed" can occur if stopIDLEInternal also called Close; treat as non-fatal.
			w.logger.Debug("IDLE close after server push (may be harmless): %v", err)
		}
		<-done // Wait for idleCmd.Wait() (server OK to DONE) to complete before any new command.
		w.sendCheckNewMessagesCommand(folder)

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

		// Queue a new-message check on the event loop (serialized with other
		// commands) rather than touching the command client from this goroutine.
		if contextErr == context.DeadlineExceeded {
			w.logger.Debug("IDLE timeout reached, checking for new messages in folder: %s", folder)
			w.sendCheckNewMessagesCommand(folder)
		}

	case err := <-done:
		// IDLE completed (server sent updates)
		if err != nil {
			w.logger.Error("IDLE command error for folder %s: %v", folder, err)
			w.incrementErrorCount()

			// Check if this is a connection error that requires reconnection
			if w.isConnectionError(err) {
				w.logger.Info("Connection error detected in IDLE monitoring, attempting automatic reconnection")
				w.bgGo(w.attemptAutomaticReconnection)
				return // Exit the loop, reconnection will restart IDLE if successful
			}
		} else {
			// IDLE completed successfully, meaning server has updates
			w.logger.Debug("IDLE completed, server has updates for folder: %s", folder)
			w.sendCheckNewMessagesCommand(folder)
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
				w.bgGo(w.attemptAutomaticReconnection)
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
			w.bgGo(w.attemptAutomaticReconnection)
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

	// The blocking IMAP operations below run on the event loop with no
	// command context; watchdog them so a stalled server cannot freeze the
	// whole account (the health check is queued behind this loop and could
	// not otherwise notice).
	checkDone := make(chan struct{})
	defer close(checkDone)
	go func(c *imapclient.Client) {
		select {
		case <-checkDone:
		case <-time.After(w.newMessageCheckTimeout):
			w.logger.Warn("New-message check for folder %s exceeded %v; closing the connection to unblock the event loop",
				folder, w.newMessageCheckTimeout)
			c.Close()
		}
	}(client)

	// Observe the mailbox with a fresh SELECT. RFC 9051 §6.3.11 discourages
	// STATUS on the currently selected mailbox, and some servers answer it
	// with stale select-time data — which made push-triggered checks silently
	// miss new messages on those servers (the EXISTS push arrives, but STATUS
	// still reports the old count). A SELECT is live by definition and
	// returns everything needed (NumMessages, UIDNext).
	selectData, err := client.Select(folder, nil).Wait()
	if err != nil {
		w.logger.Error("Failed to select folder %s for new-message check: %v", folder, err)
		return
	}
	w.mu.Lock()
	w.selectedFolder = folder
	w.mu.Unlock()

	currentMessageCount := selectData.NumMessages
	currentHighestUID := uint32(0)
	if selectData.UIDNext > 0 {
		currentHighestUID = uint32(selectData.UIDNext) - 1 // UIDNext is the next UID to be assigned
	}

	w.mu.Lock()
	lastCount := w.lastMessageCount[folder]
	lastUID := w.lastHighestUID[folder]
	initialSyncDone := w.initialSyncDone[folder]

	// Always advance the message-count watermark so we don't re-trigger on the
	// same EXISTS count change. We deliberately defer updating lastHighestUID
	// until after a successful fetch: some servers (e.g. Gmail) push EXISTS
	// before the message is actually fetchable. If fetchNewMessages returns 0,
	// leaving lastHighestUID at its old value lets the scheduled short-delay
	// retry (and, as a backstop, the IDLE timeout) pick the message up.
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
			w.newMsgRetryCount[folder] = 0
			w.mu.Unlock()
			go w.saveTrackingStateToCache()

			w.mu.RLock()
			callback := w.onNewMessage
			w.mu.RUnlock()

			if callback != nil {
				maxNotifications := MaxNewMessageNotifications
				notificationMessages := newMessages
				if len(newMessages) > maxNotifications {
					w.logger.Warn("Limiting notifications to %d messages (found %d new messages) to prevent spam",
						maxNotifications, len(newMessages))
					notificationMessages = newMessages[:maxNotifications]
				}

				event := NewMessageEvent{
					Folder:    folder,
					Messages:  notificationMessages,
					Count:     len(notificationMessages),
					Timestamp: time.Now(),
				}
				// Dispatch off the event loop so a slow consumer cannot
				// stall every IMAP command for the account.
				w.bgGo(func() { callback(event) })
			}
		} else if currentHighestUID > lastUID {
			// fetchNewMessages returned nothing despite a UID change. This typically
			// means the server sent EXISTS before the message was fully delivered.
			// lastHighestUID stays at the old value; schedule a short-delay retry so
			// detection does not stall until the next IDLE timeout.
			w.logger.Debug("fetchNewMessages returned 0 results for folder %s (UID %d->%d); scheduling short-delay retry",
				folder, lastUID, currentHighestUID)
			w.scheduleNewMessageRetry(folder)
		}
	} else {
		// No new messages; advance the UID watermark to match current server state.
		w.mu.Lock()
		w.lastHighestUID[folder] = currentHighestUID
		w.newMsgRetryCount[folder] = 0
		w.mu.Unlock()
	}
}

// scheduleNewMessageRetry schedules a short-delay re-check of a folder for
// which a UID change was observed but whose new message could not be fetched
// yet (some servers, e.g. Gmail, push EXISTS before the message is actually
// fetchable). Without this, the message would only be noticed when the IDLE
// command hits its timeout.
func (w *IMAPWorker) scheduleNewMessageRetry(folder string) {
	w.mu.Lock()
	if w.newMsgRetryScheduled[folder] {
		// A retry is already pending; it will re-check and re-schedule if needed.
		w.mu.Unlock()
		return
	}
	attempt := w.newMsgRetryCount[folder]
	if attempt >= MaxNewMessageRetries {
		w.mu.Unlock()
		w.logger.Debug("Exhausted %d new-message retries for folder %s; the IDLE timeout will retry",
			attempt, folder)
		return
	}
	w.newMsgRetryCount[folder] = attempt + 1
	w.newMsgRetryScheduled[folder] = true
	w.mu.Unlock()

	delay := NewMessageRetryBaseDelay << uint(attempt)
	w.logger.Debug("Scheduling new-message retry %d/%d for folder %s in %v",
		attempt+1, MaxNewMessageRetries, folder, delay)

	time.AfterFunc(delay, func() {
		w.mu.Lock()
		delete(w.newMsgRetryScheduled, folder)
		w.mu.Unlock()

		// Don't queue a check into a stopped worker
		select {
		case <-w.ctx.Done():
			return
		default:
		}

		w.sendCheckNewMessagesCommand(folder)
	})
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
