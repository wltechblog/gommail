package imap

import (
	"context"
	"time"

	"github.com/wltechblog/gommail/internal/email"
)

// CommandType represents the type of IMAP command to execute
type CommandType string

const (
	// Connection commands
	CmdConnect    CommandType = "connect"
	CmdDisconnect CommandType = "disconnect"
	CmdReconnect  CommandType = "reconnect"

	// Folder commands
	CmdSelectFolder          CommandType = "select_folder"
	CmdListFolders           CommandType = "list_folders"
	CmdListSubscribedFolders CommandType = "list_subscribed_folders"
	CmdCreateFolder          CommandType = "create_folder"
	CmdDeleteFolder          CommandType = "delete_folder"
	CmdSubscribeFolder       CommandType = "subscribe_folder"
	CmdUnsubscribeFolder     CommandType = "unsubscribe_folder"

	// Message commands
	CmdFetchMessages    CommandType = "fetch_messages"
	CmdFetchMessage     CommandType = "fetch_message"
	CmdMarkAsRead       CommandType = "mark_as_read"
	CmdMarkAsUnread     CommandType = "mark_as_unread"
	CmdDeleteMessage    CommandType = "delete_message"
	CmdMoveMessage      CommandType = "move_message"
	CmdSearchMessages   CommandType = "search_messages"
	CmdStoreSentMessage CommandType = "store_sent_message"

	// Monitoring commands
	CmdStartIDLE  CommandType = "start_idle"
	CmdStopIDLE   CommandType = "stop_idle"
	CmdPauseIDLE  CommandType = "pause_idle"
	CmdResumeIDLE CommandType = "resume_idle"

	// Health and status commands
	CmdHealthCheck CommandType = "health_check"
	CmdGetStatus   CommandType = "get_status"

	// Cache commands
	CmdGetCachedMessages               CommandType = "get_cached_messages"
	CmdInvalidateCache                 CommandType = "invalidate_cache"
	CmdCleanupCache                    CommandType = "cleanup_cache"
	CmdCleanupUnsubscribedFolderCache  CommandType = "cleanup_unsubscribed_folder_cache"
	CmdInvalidateFolderCache           CommandType = "invalidate_folder_cache"
	CmdInvalidateSubscribedFolderCache CommandType = "invalidate_subscribed_folder_cache"
	CmdInvalidateMessageCache          CommandType = "invalidate_message_cache"
	CmdForceDisconnect                 CommandType = "force_disconnect"
	CmdRefreshFoldersInBackground      CommandType = "refresh_folders_in_background"
)

// Note: ConnectionState is defined in client.go

// IMAPCommand represents a command to be executed by the IMAP worker
type IMAPCommand struct {
	ID         string                 // Unique command ID for tracking
	Type       CommandType            // Type of command to execute
	Parameters map[string]interface{} // Command parameters
	ResponseCh chan *IMAPResponse     // Channel to send response back
	Context    context.Context        // Context for cancellation and timeouts
	Timestamp  time.Time              // When the command was created
}

// IMAPResponse represents the response from an IMAP worker command
type IMAPResponse struct {
	ID        string                 // Matching command ID
	Success   bool                   // Whether the command succeeded
	Data      interface{}            // Response data (varies by command type)
	Error     error                  // Error if command failed
	Metadata  map[string]interface{} // Additional metadata
	Timestamp time.Time              // When the response was created
}

// Note: ConnectionEvent is defined in client.go

// NewMessageEvent represents a new message notification from IDLE monitoring
type NewMessageEvent struct {
	Folder    string          // Folder where new messages arrived
	Messages  []email.Message // New messages (may be empty if only count changed)
	Count     int             // New message count
	Timestamp time.Time       // When the event occurred
}

// WorkerStatus represents the current status of an IMAP worker
type WorkerStatus struct {
	State          ConnectionState // Current connection state
	SelectedFolder string          // Currently selected folder
	IDLEActive     bool            // Whether IDLE monitoring is active
	LastActivity   time.Time       // Last activity timestamp
	CommandsQueued int             // Number of commands in queue
	ReconnectCount int             // Number of reconnection attempts
	ErrorCount     int             // Number of errors encountered
}

// Command parameter keys for type safety
const (
	// Connection parameters
	ParamConfig = "config"

	// Folder parameters
	ParamFolderName   = "folder_name"
	ParamTargetFolder = "target_folder"

	// Message parameters
	ParamUID            = "uid"
	ParamLimit          = "limit"
	ParamMessageData    = "message_data"
	ParamMessageContent = "message_content"
	ParamCriteria       = "criteria"
	ParamSearchTerm     = "search_term"
	ParamBypassCache    = "bypass_cache"
	ParamFullHeaders    = "full_headers"

	// Cache parameters
	ParamCacheKey = "cache_key"
	ParamForce    = "force"

	// Monitoring parameters
	ParamTimeout     = "timeout"
	ParamIdleTimeout = "idle_timeout"

	// Health check parameters
	ParamHealthCheckInterval  = "health_check_interval"
	ParamReconnectDelay       = "reconnect_delay"
	ParamMaxReconnectDelay    = "max_reconnect_delay"
	ParamMaxReconnectAttempts = "max_reconnect_attempts"
)

// Response data type keys for type safety
const (
	// Connection response data
	DataConnectionState = "connection_state"
	DataServerInfo      = "server_info"

	// Folder response data
	DataFolders       = "folders"
	DataFolder        = "folder"
	DataFolderCreated = "folder_created"

	// Message response data
	DataMessages      = "messages"
	DataMessage       = "message"
	DataMessageUID    = "message_uid"
	DataSearchResults = "search_results"
	DataUIDs          = "uids"

	// Status response data
	DataWorkerStatus = "worker_status"
	DataHealthStatus = "health_status"

	// Monitoring response data
	DataIdleStatus    = "idle_status"
	DataNewMessages   = "new_messages"
	DataFolderUpdates = "folder_updates"

	// Cache response data
	DataCacheCleared = "cache_cleared"
	DataCacheStats   = "cache_stats"
	DataFound        = "found"
)

// Helper functions for creating commands

// NewCommand creates a new IMAP command with the specified type and parameters
func NewCommand(cmdType CommandType, params map[string]interface{}) *IMAPCommand {
	return &IMAPCommand{
		ID:         generateCommandID(),
		Type:       cmdType,
		Parameters: params,
		ResponseCh: make(chan *IMAPResponse, 1),
		Context:    context.Background(),
		Timestamp:  time.Now(),
	}
}

// NewCommandWithContext creates a new IMAP command with a specific context
func NewCommandWithContext(ctx context.Context, cmdType CommandType, params map[string]interface{}) *IMAPCommand {
	return &IMAPCommand{
		ID:         generateCommandID(),
		Type:       cmdType,
		Parameters: params,
		ResponseCh: make(chan *IMAPResponse, 1),
		Context:    ctx,
		Timestamp:  time.Now(),
	}
}

// NewResponse creates a new IMAP response
func NewResponse(commandID string, success bool, data interface{}, err error) *IMAPResponse {
	return &IMAPResponse{
		ID:        commandID,
		Success:   success,
		Data:      data,
		Error:     err,
		Metadata:  make(map[string]interface{}),
		Timestamp: time.Now(),
	}
}

// NewConnectionEvent creates a new connection event
func NewConnectionEvent(state ConnectionState, err error) *ConnectionEvent {
	return &ConnectionEvent{
		State:   state,
		Error:   err,
		Attempt: 0, // Default attempt count
	}
}

// NewNewMessageEvent creates a new message event
func NewNewMessageEvent(folder string, messages []email.Message, count int) *NewMessageEvent {
	return &NewMessageEvent{
		Folder:    folder,
		Messages:  messages,
		Count:     count,
		Timestamp: time.Now(),
	}
}

// Helper function to generate unique command IDs
func generateCommandID() string {
	return time.Now().Format("20060102150405.000000") + "-" + randomString(8)
}

// randomString generates a random string of specified length
func randomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[time.Now().UnixNano()%int64(len(charset))]
	}
	return string(b)
}

// Convenience methods for parameter access

// GetString safely gets a string parameter
func (cmd *IMAPCommand) GetString(key string) (string, bool) {
	if val, exists := cmd.Parameters[key]; exists {
		if str, ok := val.(string); ok {
			return str, true
		}
	}
	return "", false
}

// GetInt safely gets an int parameter
func (cmd *IMAPCommand) GetInt(key string) (int, bool) {
	if val, exists := cmd.Parameters[key]; exists {
		if i, ok := val.(int); ok {
			return i, true
		}
	}
	return 0, false
}

// GetUint32 safely gets a uint32 parameter
func (cmd *IMAPCommand) GetUint32(key string) (uint32, bool) {
	if val, exists := cmd.Parameters[key]; exists {
		if u, ok := val.(uint32); ok {
			return u, true
		}
	}
	return 0, false
}

// GetBool safely gets a bool parameter
func (cmd *IMAPCommand) GetBool(key string) (bool, bool) {
	if val, exists := cmd.Parameters[key]; exists {
		if b, ok := val.(bool); ok {
			return b, true
		}
	}
	return false, false
}

// GetServerConfig safely gets a ServerConfig parameter
func (cmd *IMAPCommand) GetServerConfig(key string) (*email.ServerConfig, bool) {
	if val, exists := cmd.Parameters[key]; exists {
		if config, ok := val.(*email.ServerConfig); ok {
			return config, true
		}
	}
	return nil, false
}

// GetSearchCriteria safely gets a SearchCriteria parameter
func (cmd *IMAPCommand) GetSearchCriteria(key string) (*email.SearchCriteria, bool) {
	if val, exists := cmd.Parameters[key]; exists {
		if criteria, ok := val.(*email.SearchCriteria); ok {
			return criteria, true
		}
	}
	return nil, false
}

// Convenience functions for creating common IDLE commands

// NewStartIDLECommand creates a command to start IDLE monitoring for a folder
func NewStartIDLECommand(folder string) *IMAPCommand {
	return NewCommand(CmdStartIDLE, map[string]interface{}{
		ParamFolderName: folder,
	})
}

// NewStopIDLECommand creates a command to stop IDLE monitoring
func NewStopIDLECommand() *IMAPCommand {
	return NewCommand(CmdStopIDLE, nil)
}

// NewPauseIDLECommand creates a command to pause IDLE monitoring
func NewPauseIDLECommand() *IMAPCommand {
	return NewCommand(CmdPauseIDLE, nil)
}

// NewResumeIDLECommand creates a command to resume IDLE monitoring
func NewResumeIDLECommand() *IMAPCommand {
	return NewCommand(CmdResumeIDLE, nil)
}

// NewHealthCheckCommand creates a command to perform a health check
func NewHealthCheckCommand() *IMAPCommand {
	return NewCommand(CmdHealthCheck, nil)
}

// GetMessage retrieves a message from the response data
func (r *IMAPResponse) GetMessage(key string) (email.Message, bool) {
	if data, ok := r.Data.(map[string]interface{}); ok {
		if val, exists := data[key]; exists {
			if message, ok := val.(email.Message); ok {
				return message, true
			}
		}
	}
	return email.Message{}, false
}

// GetMessages retrieves a slice of messages from the response data
func (r *IMAPResponse) GetMessages(key string) ([]email.Message, bool) {
	if data, ok := r.Data.(map[string]interface{}); ok {
		if val, exists := data[key]; exists {
			if messages, ok := val.([]email.Message); ok {
				return messages, true
			}
		}
	}
	return nil, false
}

// GetFolders retrieves a slice of folders from the response data
func (r *IMAPResponse) GetFolders(key string) ([]email.Folder, bool) {
	if data, ok := r.Data.(map[string]interface{}); ok {
		if val, exists := data[key]; exists {
			if folders, ok := val.([]email.Folder); ok {
				return folders, true
			}
		}
	}
	return nil, false
}
