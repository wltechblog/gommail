package email

import (
	"fmt"
	"time"
)

// ConnectionEvent represents a connection state change event
type ConnectionEvent struct {
	State   ConnectionState
	Error   error
	Attempt int
}

// ConnectionState represents the state of an IMAP connection
type ConnectionState int

const (
	ConnectionStateDisconnected ConnectionState = iota
	ConnectionStateConnecting
	ConnectionStateConnected
	ConnectionStateReconnecting
	ConnectionStateFailed
)

// String returns the string representation of the connection state
func (cs ConnectionState) String() string {
	switch cs {
	case ConnectionStateDisconnected:
		return "Disconnected"
	case ConnectionStateConnecting:
		return "Connecting"
	case ConnectionStateConnected:
		return "Connected"
	case ConnectionStateReconnecting:
		return "Reconnecting"
	case ConnectionStateFailed:
		return "Failed"
	default:
		return "Unknown"
	}
}

// MonitorMode represents the monitoring mode for IMAP connections
type MonitorMode int

const (
	MonitorModePolling MonitorMode = iota
	MonitorModeIdle
)

// String returns the string representation of the monitor mode
func (mm MonitorMode) String() string {
	switch mm {
	case MonitorModePolling:
		return "Polling"
	case MonitorModeIdle:
		return "IDLE"
	default:
		return "Unknown"
	}
}

// Message represents an email message
type Message struct {
	ID           string            `json:"id"`
	UID          uint32            `json:"uid"`
	Subject      string            `json:"subject"`
	From         []Address         `json:"from"`
	To           []Address         `json:"to"`
	CC           []Address         `json:"cc,omitempty"`
	BCC          []Address         `json:"bcc,omitempty"`
	ReplyTo      []Address         `json:"reply_to,omitempty"`
	Date         time.Time         `json:"date"`          // Date from message header (can be forged)
	InternalDate time.Time         `json:"internal_date"` // Actual arrival date from IMAP server (reliable)
	Body         MessageBody       `json:"body"`
	Attachments  []Attachment      `json:"attachments,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Flags        []string          `json:"flags,omitempty"`
	Size         int64             `json:"size"`
}

// MessageIndexItem represents a message in a message list with all necessary context
// This eliminates the need for ad-hoc lookups and synchronization issues
type MessageIndexItem struct {
	Message       Message        `json:"message"`       // The actual message data
	AccountName   string         `json:"account_name"`  // Account display name
	AccountEmail  string         `json:"account_email"` // Account email address
	FolderName    string         `json:"folder_name"`   // Source folder (e.g., "INBOX")
	IMAPClient    IMAPClient     `json:"-"`             // IMAP client for operations (not serialized)
	SMTPClient    SMTPClient     `json:"-"`             // SMTP client for operations (not serialized)
	AccountConfig *AccountConfig `json:"-"`             // Full account config (not serialized)
}

// IsRead returns true if the message has the \Seen flag
func (item *MessageIndexItem) IsRead() bool {
	for _, flag := range item.Message.Flags {
		if flag == "\\Seen" {
			return true
		}
	}
	return false
}

// MarkAsRead marks the message as read on the server
func (item *MessageIndexItem) MarkAsRead() error {
	if item.IMAPClient == nil {
		return fmt.Errorf("no IMAP client available")
	}
	return item.IMAPClient.MarkAsRead(item.FolderName, item.Message.UID)
}

// MarkAsUnread marks the message as unread on the server
func (item *MessageIndexItem) MarkAsUnread() error {
	if item.IMAPClient == nil {
		return fmt.Errorf("no IMAP client available")
	}
	return item.IMAPClient.MarkAsUnread(item.FolderName, item.Message.UID)
}

// Delete deletes the message on the server
func (item *MessageIndexItem) Delete() error {
	if item.IMAPClient == nil {
		return fmt.Errorf("no IMAP client available")
	}
	return item.IMAPClient.DeleteMessage(item.FolderName, item.Message.UID)
}

// MoveTo moves the message to another folder
func (item *MessageIndexItem) MoveTo(targetFolder string) error {
	if item.IMAPClient == nil {
		return fmt.Errorf("no IMAP client available")
	}
	return item.IMAPClient.MoveMessage(item.FolderName, item.Message.UID, targetFolder)
}

// FetchFullContent fetches the complete message content from the server
func (item *MessageIndexItem) FetchFullContent() (*Message, error) {
	if item.IMAPClient == nil {
		return nil, fmt.Errorf("no IMAP client available")
	}
	return item.IMAPClient.FetchMessage(item.FolderName, item.Message.UID)
}

// Address represents an email address
type Address struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email"`
}

// MessageBody represents the body content of a message
type MessageBody struct {
	Text string `json:"text,omitempty"`
	HTML string `json:"html,omitempty"`
}

// Attachment represents a file attachment
type Attachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Data        []byte `json:"data,omitempty"` // Only populated when needed
}

// Folder represents a mail folder
type Folder struct {
	Name         string   `json:"name"`
	Path         string   `json:"path"`
	Delimiter    string   `json:"delimiter"`
	Attributes   []string `json:"attributes,omitempty"`
	MessageCount int      `json:"message_count"`
	UnreadCount  int      `json:"unread_count"`
	Subscribed   bool     `json:"subscribed"`
}

// SearchCriteria represents search parameters for email messages
type SearchCriteria struct {
	// Text search criteria
	Content string `json:"content,omitempty"` // Search in message body
	Subject string `json:"subject,omitempty"` // Search in subject line
	From    string `json:"from,omitempty"`    // Search in from field
	To      string `json:"to,omitempty"`      // Search in to field

	// Date range criteria
	DateFrom *time.Time `json:"date_from,omitempty"` // Messages after this date
	DateTo   *time.Time `json:"date_to,omitempty"`   // Messages before this date

	// Folder and flag criteria
	Folder         string `json:"folder,omitempty"`          // Specific folder to search (empty = all subscribed)
	HasAttachments bool   `json:"has_attachments,omitempty"` // Messages with attachments
	UnreadOnly     bool   `json:"unread_only,omitempty"`     // Only unread messages

	// Advanced criteria
	MessageSize *int64   `json:"message_size,omitempty"` // Minimum message size in bytes
	Keywords    []string `json:"keywords,omitempty"`     // Custom keywords/flags

	// Search options
	CaseSensitive bool `json:"case_sensitive,omitempty"` // Case sensitive text search
	UseRegex      bool `json:"use_regex,omitempty"`      // Use regex for text fields
	SearchServer  bool `json:"search_server,omitempty"`  // Search server vs cache only
	MaxResults    int  `json:"max_results,omitempty"`    // Maximum number of results (0 = no limit)
}

// Account represents an active email account connection
type Account struct {
	Config     AccountConfig
	IMAPClient IMAPClient
	SMTPClient SMTPClient
	Folders    []Folder
}

// AccountConfig represents account configuration
type AccountConfig struct {
	Name        string
	Email       string
	DisplayName string
	IMAP        ServerConfig
	SMTP        ServerConfig
}

// ServerConfig represents server connection configuration
type ServerConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	TLS      bool
}

// IMAPClient interface for IMAP operations
type IMAPClient interface {
	Connect() error
	Disconnect() error
	ForceReconnect() error
	ListFolders() ([]Folder, error)
	ListSubscribedFolders() ([]Folder, error)
	ListSubscribedFoldersFresh() ([]Folder, error)
	ForceRefreshSubscribedFolders() ([]Folder, error)
	ListAllFolders() ([]Folder, error)
	CreateFolder(name string) error
	DeleteFolder(name string) error
	SubscribeFolder(name string) error
	UnsubscribeFolder(name string) error
	SubscribeToDefaultFolders(sentFolder, trashFolder string) error
	StoreSentMessage(sentFolder string, messageContent []byte) error
	SelectFolder(name string) error
	FetchMessages(folder string, limit int) ([]Message, error)
	FetchMessage(folder string, uid uint32) (*Message, error)
	MarkAsRead(folder string, uid uint32) error
	MarkAsUnread(folder string, uid uint32) error
	DeleteMessage(folder string, uid uint32) error
	MoveMessage(folder string, uid uint32, targetFolder string) error
	// Search methods
	SearchMessages(criteria SearchCriteria) ([]Message, error)
	SearchMessagesInFolder(folder string, criteria SearchCriteria) ([]Message, error)
	SearchCachedMessages(criteria SearchCriteria) ([]Message, error)

	// Cache methods
	FetchFreshMessages(folder string, limit int) ([]Message, error)
	FetchMessageWithFullHeaders(folder string, uid uint32) (*Message, error)
	GetCachedMessages(folder string) ([]Message, bool, error)
	InvalidateFolderCache()
	InvalidateSubscribedFolderCache()
	InvalidateMessageCache(folder string)
	CleanupUnsubscribedFolderCache() error

	// Worker lifecycle methods
	Start() error
	Stop()

	// Connection state and monitoring methods
	SetConnectionStateCallback(callback func(ConnectionEvent))
	SetNewMessageCallback(callback func(string, []Message))
	StartMonitoring(folder string) error
	StopMonitoring()
	IsMonitoring() bool
	IsMonitoringPaused() bool
	PauseMonitoring()
	ResumeMonitoring()
	GetMonitoredFolder() string
	GetMonitoringMode() MonitorMode
	MarkInitialSyncComplete(folder string)

	// Health and status methods
	GetHealthStatus() map[string]interface{}
	SetHealthCheckInterval(interval time.Duration)
	SetReconnectConfig(initialDelay, maxDelay time.Duration, maxAttempts int)
	ForceDisconnect()
	RefreshFoldersInBackground()
}

// SMTPClient interface for SMTP operations
type SMTPClient interface {
	Connect() error
	Disconnect() error
	SendMessage(msg *Message) error
}
