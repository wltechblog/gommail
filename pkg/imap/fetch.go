package imap

// Code in this file was mechanically split from worker.go.

import (
	"fmt"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/wltechblog/gommail/internal/email"
)

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

	// Bulk fetches run on a dedicated connection so a multi-minute transfer
	// never blocks regular commands (status, flags, monitoring) on the
	// command connection. Fall back to the command client if the fetch
	// client is unavailable.
	client := w.ensureFetchClient()
	if client == nil {
		client = w.commandClient
	}
	usingFetchClient := client != w.commandClient

	// Select folder if not already selected on this client
	selectedFolder := w.selectedFolder
	if usingFetchClient {
		selectedFolder = w.fetchSelectedFolder
	}
	if selectedFolder != folderName {
		_, err := client.Select(folderName, nil).Wait()
		if err != nil {
			w.logger.Error("Failed to select folder %s: %v", folderName, err)
			w.incrementErrorCount()
			return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to select folder: %w", err))
		}
		w.mu.Lock()
		if usingFetchClient {
			w.fetchSelectedFolder = folderName
		} else {
			w.selectedFolder = folderName
		}
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

	searchCmd := client.UIDSearch(searchCriteria, nil)
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
	fetchCmd := client.Fetch(uidSet, &imap.FetchOptions{
		Envelope:     true,
		Flags:        true,
		UID:          true,
		RFC822Size:   true,
		InternalDate: true, // Fetch actual arrival date from server
	})

	// On the dedicated fetch client, watch for caller cancellation: closing
	// the connection unblocks a stalled Next() (e.g. server stopped sending
	// mid-transfer), letting the handler abort promptly instead of hanging
	// until the connection dies on its own. The command-client fallback keeps
	// drain semantics, so it must NOT be closed here.
	if usingFetchClient {
		fetchDone := make(chan struct{})
		defer close(fetchDone)
		watchCtx := cmd.Context
		go func(c *imapclient.Client) {
			select {
			case <-watchCtx.Done():
				c.Close()
			case <-fetchDone:
			}
		}(client)
	}

	messages := []email.Message{}
	aborted := false
	for {
		msg := fetchCmd.Next()
		if msg == nil {
			break
		}

		// Abort as soon as the caller has given up (command timeout/context
		// cancellation). Without this, an abandoned bulk fetch keeps the
		// worker's serial event loop busy converting and caching messages
		// nobody waits for, cascading into timeouts for every command queued
		// behind it (start monitoring, mark as read, ...).
		select {
		case <-cmd.Context.Done():
			aborted = true
		default:
		}
		if aborted {
			break
		}

		emailMsg := w.convertFetchDataToMessage(msg)
		messages = append(messages, emailMsg)
	}

	// Also treat a cancellation that surfaced as a transfer error (the
	// watcher closed the connection) as an abort: never report or cache a
	// partial transfer as success.
	if !aborted {
		select {
		case <-cmd.Context.Done():
			aborted = true
		default:
		}
	}

	if aborted {
		// The caller has given up. On the dedicated fetch client, closing the
		// connection aborts instantly — no need to drain the remaining
		// transfer; it is recreated lazily by ensureFetchClient. On the
		// command-client fallback, Close() drains the remaining FETCH
		// responses so the shared connection stays usable.
		w.logger.Warn("Fetch of folder %s aborted after %d messages: caller cancelled (context done)",
			folderName, len(messages))
		if usingFetchClient {
			client.Close()
			w.mu.Lock()
			w.fetchClient = nil
			w.fetchSelectedFolder = ""
			w.mu.Unlock()
		} else if err := fetchCmd.Close(); err != nil {
			w.logger.Warn("Error closing fetch command: %v", err)
		}
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("command cancelled while fetching messages"))
	}

	// Close the fetch command; the transfer has completed, so this only waits
	// for the tagged response.
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
	if err := w.ensureSelected(folderName); err != nil {
		return NewResponse(cmd.ID, false, nil, err)
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
			w.bgGo(w.attemptAutomaticReconnection)
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
				w.bgGo(w.attemptAutomaticReconnection)
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
