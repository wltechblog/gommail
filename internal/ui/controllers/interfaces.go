// Package controllers provides interfaces and implementations for UI controllers
// that manage different aspects of the main window functionality.
//
// This package is part of Phase 2 of the refactoring plan to decompose the
// 7,147-line MainWindow into manageable, single-responsibility components.
package controllers

import (
	"context"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
	"github.com/wltechblog/gommail/internal/config"
	"github.com/wltechblog/gommail/internal/email"
	"github.com/wltechblog/gommail/pkg/imap"
)

// SelectionManager handles message selection state and operations.
// It manages single and multiple message selection, including Ctrl/Shift modifiers.
type SelectionManager interface {
	// Selection state queries
	IsMessageSelected(index int) bool
	GetSelectedMessageIndices() []int
	GetSelectedMessages() []*email.MessageIndexItem
	GetSelectedMessage() *email.MessageIndexItem

	// Selection operations
	SelectMessage(index int)
	SelectMessageMultiple(index int, ctrlPressed, shiftPressed bool)
	SelectAllMessages()
	ClearSelection()

	// Multi-selection mode
	IsMultiSelectionMode() bool
	SetMultiSelectionMode(enabled bool)

	// Internal state management
	UpdateLastSelectedIndex(index int)
	GetLastSelectedIndex() int
}

// MessageViewController handles message display and rendering.
// It manages the message viewer, attachments, and HTML/text toggle.
type MessageViewController interface {
	// Message display
	DisplayMessage(msg *email.Message)
	UpdateMessageViewer(markdownContent string)
	ClearMessageView()

	// Attachment handling
	UpdateAttachmentSection(msg *email.Message)
	ShowAttachments()
	SaveAttachment(attachment email.Attachment)
	SaveAttachments()

	// HTML/Text conversion
	HTMLToMarkdown(html string) string
	HTMLToPlainText(html string) string
	FormatTextForMarkdown(text string) string

	// View toggle
	ToggleHTMLView()
	IsShowingHTML() bool

	// Attachment preview
	CreateAttachmentWidget(attachment email.Attachment, attachmentID string, index int) fyne.CanvasObject
	CreateImagePreview(attachment email.Attachment, attachmentID string) fyne.CanvasObject
	CreateTextPreview(attachment email.Attachment, attachmentID string) fyne.CanvasObject
	ShowImageFullSize(attachment email.Attachment, attachmentID string)

	// UI components access
	GetMessageViewer() *widget.RichText
	GetAttachmentSection() *fyne.Container
	GetMessageContainer() *fyne.Container
}

// FolderController handles folder operations and state.
// It manages folder list, selection, and folder-related operations.
type FolderController interface {
	// Folder selection
	SelectFolder(folder string)
	GetCurrentFolder() string

	// Folder list management
	GetFolders() []email.Folder
	SetFolders(folders []email.Folder)
	RefreshFolders()
	ReloadFolders()
	SortFolders(folders []email.Folder) []email.Folder

	// Folder operations
	ShowFolderSubscriptions()
	ShowFolderSubscriptionsForAccount(account *config.Account)
	SyncFolderSubscriptionsInBackground(accountName string)

	// Folder state
	IsFolderLoading() bool
	SetFolderLoading(loading bool)
	GetFolderMessageCount(folderName string) int
	UpdateFolderMessageCount(folderName string, newCount int)

	// Folder monitoring
	StartFolderMonitoring(folder string)
	StopFolderMonitoring()
	HandleDeletedFolder(deletedFolder string)

	// UI components access
	GetFolderList() *widget.List
}

// AccountController handles account operations and unified inbox.
// It manages account selection, IMAP clients, and unified inbox functionality.
// NOTE: This is a simplified interface for Phase 2.5. Additional methods will be added in later phases.
type AccountController interface {
	// Account management
	GetCurrentAccount() *config.Account
	SetCurrentAccount(account *config.Account)
	ClearCurrentAccount()

	// Unified inbox state
	IsUnifiedInbox() bool
	SetUnifiedInbox(enabled bool)

	// IMAP client management
	GetOrCreateIMAPClient(account *config.Account) (*imap.ClientWrapper, error)
	GetIMAPClientForAccount(accountName string) (*imap.ClientWrapper, bool)
	StoreIMAPClient(accountName string, client *imap.ClientWrapper)
	CloseAllClients()
	CloseClientForAccount(accountName string)
	ForEachClient(fn func(accountName string, client *imap.ClientWrapper))

	// Unified inbox monitoring
	StartUnifiedInboxMonitoring(ctx context.Context)
	StopUnifiedInboxMonitoring()
	IsMonitoringUnifiedInbox() bool

	// Cache operations
	InvalidateUnifiedInboxCache()
	CacheAccountMessages(accountName string, messages []email.Message)
}

// MessageListController handles message list display and operations.
// It manages message list, sorting, and message-related operations.
type MessageListController interface {
	// Message list management
	GetMessages() []email.MessageIndexItem
	SetMessages(messages []email.MessageIndexItem)
	RefreshMessageList()

	// Sorting
	SortMessages()
	SetSortBy(sortBy SortBy)
	GetSortBy() SortBy
	SetSortOrder(order SortOrder)
	GetSortOrder() SortOrder
	ToggleSortOrder()
	GetSortName() string
	UpdateSortButtons()
	UpdateColumnHeaders()
	ShowSortMenu()

	// Message operations
	MoveToTrash()
	MoveToTrashMultiple()
	DeleteMessage()
	DeleteMessagesMultiple()
	MoveToFolder(targetFolder string)
	ShowMoveToFolderDialog()
	ShowMoveToFolderDialogMultiple()

	// Message actions
	ComposeMessage()
	ReplyToMessage()
	ReplyAllToMessage()
	ForwardMessage()

	// Message display helpers
	GetSenderName(msg email.Message) string
	GetSenderNameFromIndexItem(item email.MessageIndexItem) string
	ConvertMessagesToIndexItems(messages []email.Message, folderName string) []email.MessageIndexItem

	// UI components access
	GetMessageList() *widget.List
	GetSortByButton() *widget.Button
	GetSortOrderButton() *widget.Button
}

// SortBy represents the sorting criteria for messages
type SortBy int

const (
	SortByDate SortBy = iota
	SortBySender
	SortBySubject
)

// SortOrder represents the sorting order
type SortOrder int

const (
	SortAscending SortOrder = iota
	SortDescending
)
