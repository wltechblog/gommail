package imap

// Code in this file was mechanically split from worker.go.

import (
	"fmt"
	"time"

	"github.com/emersion/go-imap/v2"
)

// ensureSelected selects folderName on the command connection when it is
// not already selected, recording the new selection under w.mu. Connection
// errors route into the standard automatic-reconnection path.
func (w *IMAPWorker) ensureSelected(folderName string) error {
	if w.selectedFolder == folderName {
		return nil
	}
	_, err := w.commandClient.Select(folderName, nil).Wait()
	if err != nil {
		w.logger.Error("Failed to select folder %s: %v", folderName, err)
		w.incrementErrorCount()
		if w.isConnectionError(err) {
			w.logger.Warn("Connection error during folder selection, initiating reconnection")
			w.bgGo(w.attemptAutomaticReconnection)
			return fmt.Errorf("connection error during folder selection: %w", err)
		}
		return fmt.Errorf("failed to select folder: %w", err)
	}
	w.mu.Lock()
	w.selectedFolder = folderName
	w.mu.Unlock()
	return nil
}

// handleMarkAsRead marks a message as read (\Seen add).
func (w *IMAPWorker) handleMarkAsRead(cmd *IMAPCommand) *IMAPResponse {
	return w.handleSetSeen(cmd, true)
}

// handleMarkAsUnread marks a message as unread (\Seen remove).
func (w *IMAPWorker) handleMarkAsUnread(cmd *IMAPCommand) *IMAPResponse {
	return w.handleSetSeen(cmd, false)
}

// handleSetSeen adds or removes the \Seen flag for one message. The two
// public handlers used to be byte-identical apart from the store operation.
func (w *IMAPWorker) handleSetSeen(cmd *IMAPCommand, seen bool) *IMAPResponse {
	action := "read"
	op := imap.StoreFlagsAdd
	if !seen {
		action = "unread"
		op = imap.StoreFlagsDel
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

	w.logger.Debug("Marking message UID %d as %s in folder: %s", uid, action, folderName)

	if err := w.ensureSelected(folderName); err != nil {
		return NewResponse(cmd.ID, false, nil, err)
	}

	uidSet := imap.UIDSetNum(imap.UID(uid))
	storeCmd := w.commandClient.Store(uidSet, &imap.StoreFlags{
		Op:    op,
		Flags: []imap.Flag{imap.FlagSeen},
	}, nil)

	// Consume any returned data (not needed for flag operations)
	for storeCmd.Next() != nil {
	}

	if err := storeCmd.Close(); err != nil {
		w.logger.Error("Failed to mark message UID %d as %s: %v", uid, action, err)
		w.incrementErrorCount()
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("failed to mark message as %s: %w", action, err))
	}

	w.logger.Debug("Successfully marked message UID %d as %s", uid, action)
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

	if err := w.ensureSelected(folderName); err != nil {
		return NewResponse(cmd.ID, false, nil, err)
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
	if err := w.ensureSelected(folderName); err != nil {
		return NewResponse(cmd.ID, false, nil, err)
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
