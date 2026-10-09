package imap

// Code in this file was mechanically split from worker.go.

import (
	"fmt"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/wltechblog/gommail/internal/email"
)

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
