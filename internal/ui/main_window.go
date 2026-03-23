// Package ui provides the main window implementation for the email client.
//
// MESSAGE LIST CLICK HANDLING IMPLEMENTATION
// ==========================================
//
// This file contains a critical implementation for handling mouse clicks on the message list.
// The solution addresses a fundamental Fyne UI framework limitation where container widgets
// can block mouse events from reaching child widgets.
//
// PROBLEM SOLVED:
// - Message list was not responding to left-clicks for selection
// - Right-click context menus appeared in wrong positions
// - Container widgets were intercepting mouse events
//
// SOLUTION IMPLEMENTED:
// The solution uses custom message list items that implement SecondaryTappable:
// 1. messageListWithRightClick wraps the List widget and handles right-clicks
// 2. messageListItem implements both Tappable and SecondaryTappable interfaces
// 3. Left-clicks trigger message selection via Tapped()
// 4. Right-clicks show context menus via TappedSecondary()
// 5. Coordinate conversion ensures proper menu positioning
//
// KEY COMPONENTS:
// - messageListWithRightClick: Wrapper widget that handles right-clicks on the list
// - messageListItem: Individual list items that handle both left and right clicks
// - TappedSecondary(): Handles right-clicks and shows context menu
// - Coordinate conversion for proper context menu positioning
package ui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	_ "image/gif"  // Register GIF format
	_ "image/jpeg" // Register JPEG format
	_ "image/png"  // Register PNG format
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	ttwidget "github.com/dweymouth/fyne-tooltip/widget"
	"github.com/skratchdot/open-golang/open"

	"github.com/PuerkitoBio/goquery"
	"github.com/wltechblog/gommail/internal/addressbook"
	"github.com/wltechblog/gommail/internal/config"
	"github.com/wltechblog/gommail/internal/email"
	"github.com/wltechblog/gommail/internal/logging"
	"github.com/wltechblog/gommail/internal/resources"
	"github.com/wltechblog/gommail/internal/trace"
	"github.com/wltechblog/gommail/internal/ui/controllers"
	"github.com/wltechblog/gommail/pkg/cache"
	"github.com/wltechblog/gommail/pkg/imap"
	"github.com/wltechblog/gommail/pkg/notification"
	"github.com/wltechblog/gommail/pkg/smtp"
)

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func accountCacheKey(accountName string) string {
	return fmt.Sprintf("account_%s", accountName)
}

func accountCacheKeyCandidates(accountName string) []string {
	primary := accountCacheKey(accountName)
	if primary == accountName {
		return []string{primary}
	}
	return []string{primary, accountName}
}

func messageViewModeButtonLabel(showHTML bool) string {
	if showHTML {
		return "View: Rich"
	}
	return "View: Plain"
}

func messageViewModeButtonTooltip(showHTML bool) string {
	if showHTML {
		return "Currently showing rich view. Click to switch to plain text."
	}
	return "Currently showing plain text. Click to switch to rich view."
}

func messageViewModeStatusText(showHTML bool) string {
	if showHTML {
		return "Showing rich message view"
	}
	return "Showing plain text message view"
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

const (
	messageDateColumnWidth   float32 = 120
	messageSenderColumnWidth float32 = 180
)

// MainWindow represents the main application window.
//
// Architecture:
// MainWindow serves as the orchestrator for the email client UI. It coordinates
// between specialized controllers that handle specific aspects of the application:
//
// - SelectionManager: Manages message selection state (single/multiple selection)
// - MessageViewController: Handles message display and HTML/text view toggling
// - FolderController: Manages folder list, selection, and operations
// - AccountController: Manages account list, selection, and IMAP/SMTP clients
// - MessageListController: Manages message list display, sorting, and operations
//
// MainWindow is responsible for:
// - UI layout and window lifecycle
// - Coordinating between controllers
// - Handling complex operations that span multiple controllers (e.g., unified inbox)
// - Managing background tasks (monitoring, health checks, refresh coordination)
// - Providing callbacks and event handlers that bridge controllers
//
// Controllers are responsible for:
// - Managing their specific UI components and state
// - Implementing business logic for their domain
// - Providing clean interfaces for MainWindow to orchestrate
type MainWindow struct {
	app    fyne.App
	window fyne.Window
	config config.ConfigManager

	// Email components
	processor         *email.MessageProcessor
	attachmentManager *email.AttachmentManager
	imapClient        *imap.ClientWrapper
	smtpClient        *smtp.Client
	cache             *cache.Cache
	notificationMgr   *notification.Manager
	addressbookMgr    *addressbook.Manager

	// UI components
	accountList *widget.List
	messageList *widget.List

	statusBar *widget.Label
	toolbar   *fyne.Container

	// Controllers - handle specific aspects of the UI
	messageViewController *controllers.MessageViewControllerImpl
	folderController      *controllers.FolderControllerImpl
	accountController     *controllers.AccountControllerImpl
	messageListController *controllers.MessageListControllerImpl
	selectionManager      *controllers.SelectionManagerImpl

	// UI components managed by controllers
	sortByButton     *widget.Button
	sortOrderButton  *widget.Button
	dateHeaderBtn    *widget.Button
	senderHeaderBtn  *widget.Button
	subjectHeaderBtn *widget.Button
	toggleViewButton *ttwidget.Button

	// backgroundWg tracks all fire-and-forget goroutines so cleanup can wait
	// for them to finish before tearing down shared state.
	backgroundWg sync.WaitGroup

	// Message state - synchronized with controllers
	// messagesMu protects messages and selectedMessage from concurrent access.
	// All reads/writes must hold this lock (or be guaranteed to run on the UI thread via fyne.Do).
	messagesMu      sync.RWMutex
	messages        []email.MessageIndexItem
	selectedMessage *email.MessageIndexItem

	// Sorting state - synchronized with MessageListController
	sortBy    SortBy
	sortOrder SortOrder

	// HTML/Text view toggle
	showHTMLContent bool

	// Auto-selection state
	autoSelectionDone bool

	// Fresh fetch state
	freshFetchInProgress bool

	// Read timer state
	readTimer *time.Timer

	// Right-click detection
	// Message list wrapper that handles right-click events on individual message items
	messageListWithRightClick *messageListWithRightClick

	// Debounced refresh operations
	folderRefreshDebouncer  *CallbackDebouncer
	messageRefreshDebouncer *CallbackDebouncer
	accountRefreshDebouncer *CallbackDebouncer

	// Tooltip layer enabled
	tooltipLayerEnabled bool

	// Context menu popup reference for dismissal
	currentContextPopup *widget.PopUp

	// Unified inbox delete operation tracking (per account)
	unifiedInboxDeleteInProgress map[string]bool // account name -> delete in progress
	deleteOperationMutex         sync.RWMutex

	// Unified inbox monitoring context
	unifiedInboxCtx      context.Context
	unifiedInboxCancel   context.CancelFunc
	unifiedInboxHealthMu sync.Mutex

	// Global health monitoring context (works in both unified and single account modes)
	globalHealthCtx    context.Context
	globalHealthCancel context.CancelFunc

	// Connection lifecycle guards
	connectionLifecycleMu           sync.Mutex
	reconnectInProgress             bool
	currentAccountConnectInProgress bool

	// Logging
	logger *logging.Logger
}

// messageListWithRightClick wraps a Fyne List widget and adds right-click support
// It implements fyne.Widget and fyne.SecondaryTappable to handle right-click events
type messageListWithRightClick struct {
	widget.BaseWidget
	List       *widget.List
	mainWindow *MainWindow
}

// newMessageListWithRightClick creates a new message list
func newMessageListWithRightClick(mainWindow *MainWindow) *messageListWithRightClick {
	// Create the underlying list widget
	list := widget.NewList(
		func() int {
			return len(mainWindow.messages)
		},
		func() fyne.CanvasObject {
			// Create custom message list item that handles right-clicks
			return newMessageListItem(mainWindow)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			// Update the custom message list item
			if item, ok := obj.(*messageListItem); ok && id < len(mainWindow.messages) {
				item.updateContent(id, &mainWindow.messages[id])
			}
		},
	)

	// Handle message selection
	list.OnSelected = func(id widget.ListItemID) {
		mainWindow.logger.Debug("OnSelected: List widget selected ID=%d, messageCount=%d", id, len(mainWindow.messages))
		if id >= 0 && id < len(mainWindow.messages) {
			mainWindow.logger.Debug("OnSelected: Calling selectMessage(%d) for subject: %s", id, mainWindow.messages[id].Message.Subject)
			mainWindow.selectMessage(id)
		} else {
			mainWindow.logger.Warn("OnSelected: Invalid ID %d (messageCount=%d) - ignoring selection", id, len(mainWindow.messages))
		}
	}

	wrapper := &messageListWithRightClick{
		List:       list,
		mainWindow: mainWindow,
	}
	wrapper.ExtendBaseWidget(wrapper)

	return wrapper
}

// CreateRenderer creates a renderer for the messageListWithRightClick widget
func (m *messageListWithRightClick) CreateRenderer() fyne.WidgetRenderer {
	return &messageListRenderer{list: m.List}
}

// TappedSecondary handles right-click events on the message list
func (m *messageListWithRightClick) TappedSecondary(pe *fyne.PointEvent) {
	m.mainWindow.logger.Debug("TappedSecondary: Right-click at position %v", pe.Position)

	// The right-click should act on the currently selected message
	// We don't need to calculate which message was clicked because the user
	// should first left-click to select, then right-click for context menu
	if m.mainWindow.selectedMessage != nil {
		m.mainWindow.logger.Debug("TappedSecondary: Showing context menu for selected message: %s", m.mainWindow.selectedMessage.Message.Subject)
		// Convert the position to canvas coordinates and show context menu
		canvasPos := fyne.CurrentApp().Driver().AbsolutePositionForObject(m)
		canvasPos = canvasPos.Add(pe.Position)
		m.mainWindow.showMessageContextMenuAtPosition(canvasPos)
	} else {
		m.mainWindow.logger.Debug("TappedSecondary: No message selected, cannot show context menu")
	}
}

// messageListRenderer renders the message list widget
type messageListRenderer struct {
	list *widget.List
}

func (r *messageListRenderer) Layout(size fyne.Size) {
	r.list.Resize(size)
}

func (r *messageListRenderer) MinSize() fyne.Size {
	return r.list.MinSize()
}

func (r *messageListRenderer) Refresh() {
	r.list.Refresh()
}

func (r *messageListRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.list}
}

func (r *messageListRenderer) Destroy() {
	// Nothing to destroy
}

type messageListColumnsLayout struct {
	dateWidth   float32
	senderWidth float32
}

func (l *messageListColumnsLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 3 {
		return
	}

	height := size.Height
	if height <= 0 {
		height = objects[0].MinSize().Height
	}

	dateObj := objects[0]
	senderObj := objects[1]
	subjectObj := objects[2]

	dateObj.Resize(fyne.NewSize(l.dateWidth, height))
	dateObj.Move(fyne.NewPos(0, 0))

	senderObj.Resize(fyne.NewSize(l.senderWidth, height))
	senderObj.Move(fyne.NewPos(l.dateWidth, 0))

	subjectWidth := size.Width - l.dateWidth - l.senderWidth
	if subjectWidth < 0 {
		subjectWidth = 0
	}

	subjectObj.Resize(fyne.NewSize(subjectWidth, height))
	subjectObj.Move(fyne.NewPos(l.dateWidth+l.senderWidth, 0))
}

func (l *messageListColumnsLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var height float32
	var subjectWidth float32

	if len(objects) >= 3 {
		subjectWidth = objects[2].MinSize().Width
	}

	if subjectWidth == 0 {
		subjectWidth = 50
	}

	for _, obj := range objects {
		if obj == nil {
			continue
		}

		if h := obj.MinSize().Height; h > height {
			height = h
		}
	}

	return fyne.NewSize(l.dateWidth+l.senderWidth+subjectWidth, height)
}

// messageListItem is a custom widget that represents a single message in the list
// It implements fyne.SecondaryTappable to handle right-click events directly
type messageListItem struct {
	widget.BaseWidget
	mainWindow   *MainWindow
	messageID    int
	dateLabel    *widget.Label
	senderLabel  *widget.Label
	subjectLabel *widget.Label
	content      *fyne.Container
	background   *canvas.Rectangle
	isSelected   bool
}

// newMessageListItem creates a new message list item widget
func newMessageListItem(mainWindow *MainWindow) *messageListItem {
	dateLabel := widget.NewLabel("Jan 1, 15:04")
	dateLabel.Truncation = fyne.TextTruncateEllipsis

	senderLabel := widget.NewLabel("Sender Name")
	senderLabel.Truncation = fyne.TextTruncateEllipsis

	subjectLabel := widget.NewLabel("Subject line that should be fully visible and not truncated")
	subjectLabel.Wrapping = fyne.TextWrapOff
	subjectLabel.Truncation = fyne.TextTruncateEllipsis

	content := container.New(&messageListColumnsLayout{
		dateWidth:   messageDateColumnWidth,
		senderWidth: messageSenderColumnWidth,
	}, dateLabel, senderLabel, subjectLabel)

	background := canvas.NewRectangle(theme.Color(theme.ColorNameBackground))
	background.Hide()

	item := &messageListItem{
		mainWindow:   mainWindow,
		messageID:    -1,
		dateLabel:    dateLabel,
		senderLabel:  senderLabel,
		subjectLabel: subjectLabel,
		content:      content,
		background:   background,
		isSelected:   false,
	}
	item.ExtendBaseWidget(item)
	return item
}

// CreateRenderer creates a renderer for the message list item
func (m *messageListItem) CreateRenderer() fyne.WidgetRenderer {
	return &messageListItemRenderer{item: m}
}

// messageListItemRenderer renders the message list item
type messageListItemRenderer struct {
	item *messageListItem
}

func (r *messageListItemRenderer) Layout(size fyne.Size) {
	r.item.background.Resize(size)
	r.item.content.Resize(size)
}

func (r *messageListItemRenderer) MinSize() fyne.Size {
	return r.item.content.MinSize()
}

func (r *messageListItemRenderer) Refresh() {
	if r.item.isSelected {
		r.item.background.FillColor = theme.Color(theme.ColorNameSelection)
		r.item.background.Show()
	} else {
		r.item.background.Hide()
	}
	r.item.background.Refresh()
	r.item.content.Refresh()
}

func (r *messageListItemRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.item.background, r.item.content}
}

func (r *messageListItemRenderer) Destroy() {
	// Nothing to destroy
}

// setSelected updates the selection state of the message list item
func (m *messageListItem) setSelected(selected bool) {
	if m.isSelected != selected {
		m.isSelected = selected
		m.Refresh()
	}
}

// Tapped handles left-click events on the message item
func (m *messageListItem) Tapped(pe *fyne.PointEvent) {
	m.mainWindow.logger.Debug("messageListItem.Tapped: Left-click on message ID=%d", m.messageID)

	// Validate message ID bounds before selection
	if m.messageID < 0 || m.messageID >= len(m.mainWindow.messages) {
		m.mainWindow.logger.Warn("messageListItem.Tapped: Invalid message ID %d (messageCount=%d) - ignoring click", m.messageID, len(m.mainWindow.messages))
		return
	}

	m.mainWindow.logger.Debug("messageListItem.Tapped: Selecting message %d: %s", m.messageID, m.mainWindow.messages[m.messageID].Message.Subject)

	// Simple single selection for left-click
	// Multiple selection will be handled via right-click or keyboard shortcuts
	ctrlPressed := false
	shiftPressed := false

	// Use multiple selection logic
	m.mainWindow.selectMessageMultiple(m.messageID, ctrlPressed, shiftPressed)

	// Also trigger the List widget's selection to keep it in sync
	if m.mainWindow.messageListWithRightClick != nil {
		m.mainWindow.messageListWithRightClick.List.Select(m.messageID)
	}
}

// TappedSecondary handles right-click events on the message item
func (m *messageListItem) TappedSecondary(pe *fyne.PointEvent) {
	m.mainWindow.logger.Debug("messageListItem.TappedSecondary: Right-click on message ID=%d", m.messageID)

	// Validate message ID bounds before selection
	if m.messageID < 0 || m.messageID >= len(m.mainWindow.messages) {
		m.mainWindow.logger.Debug("messageListItem.TappedSecondary: Invalid message ID %d (messageCount=%d) - ignoring right-click", m.messageID, len(m.mainWindow.messages))
		m.mainWindow.logger.Warn("Message list item right-click with invalid ID %d (messageCount=%d)", m.messageID, len(m.mainWindow.messages))
		return
	}

	m.mainWindow.logger.Debug("messageListItem.TappedSecondary: Right-click on message %d: %s", m.messageID, m.mainWindow.messages[m.messageID].Message.Subject)

	// Right-click can toggle selection in multiple selection mode
	// If message is not selected, select it (and add to multiple selection if others are selected)
	// If message is already selected and others are selected, keep all selections
	selectedMessages := m.mainWindow.getSelectedMessages()
	isCurrentlySelected := m.mainWindow.isMessageSelected(m.messageID)

	if len(selectedMessages) > 0 && !isCurrentlySelected {
		// Add this message to the existing selection
		m.mainWindow.selectMessageMultiple(m.messageID, true, false) // Ctrl+click behavior
	} else if !isCurrentlySelected {
		// Single selection
		m.mainWindow.selectMessageMultiple(m.messageID, false, false)
	}
	// If already selected, keep the selection as-is

	// Also trigger the List widget's selection to keep it in sync
	if m.mainWindow.messageListWithRightClick != nil {
		m.mainWindow.messageListWithRightClick.List.Select(m.messageID)
	}

	// Show context menu
	canvasPos := fyne.CurrentApp().Driver().AbsolutePositionForObject(m)
	canvasPos = canvasPos.Add(pe.Position)
	m.mainWindow.showMessageContextMenuAtPosition(canvasPos)
}

// updateContent updates the content of this message list item
func (m *messageListItem) updateContent(messageID int, message *email.MessageIndexItem) {
	m.messageID = messageID

	if message != nil {
		// Update labels with message data - use getDisplayDate for local timezone display
		displayDate := m.mainWindow.getDisplayDate(&message.Message)
		m.dateLabel.SetText(displayDate.Format("Jan 2, 15:04"))

		// Format sender name from address list
		senderName := "Unknown Sender"
		if len(message.Message.From) > 0 {
			if message.Message.From[0].Name != "" {
				senderName = message.Message.From[0].Name
			} else {
				senderName = message.Message.From[0].Email
			}
		}

		// If in unified inbox mode, prepend account name
		if m.mainWindow.accountController.IsUnifiedInbox() {
			senderName = fmt.Sprintf("[%s] %s", message.AccountName, senderName)
		}

		// Truncate long sender names to fit in column
		if len(senderName) > 25 { // Increased limit to accommodate account name
			senderName = senderName[:22] + "..."
		}
		m.senderLabel.SetText(senderName)

		// Check if message is read by looking at flags
		isRead := false
		for _, flag := range message.Message.Flags {
			if flag == "\\Seen" {
				isRead = true
				break
			}
		}

		// Format subject with unread indicator and apply styling
		subject := message.Message.Subject
		if subject == "" {
			subject = "(No Subject)"
		}

		if !isRead {
			// Apply bold styling for unread messages
			m.dateLabel.TextStyle = fyne.TextStyle{Bold: true}
			m.senderLabel.TextStyle = fyne.TextStyle{Bold: true}
			m.subjectLabel.TextStyle = fyne.TextStyle{Bold: true}
		} else {
			// Normal styling for read messages
			m.dateLabel.TextStyle = fyne.TextStyle{}
			m.senderLabel.TextStyle = fyne.TextStyle{}
			m.subjectLabel.TextStyle = fyne.TextStyle{}
		}
		m.subjectLabel.SetText(subject)

		// Refresh all labels to ensure TextStyle changes are applied
		// This is critical because TextStyle changes don't automatically trigger a refresh
		m.dateLabel.Refresh()
		m.senderLabel.Refresh()
		m.subjectLabel.Refresh()

		// Update selection highlighting based on whether this message is currently selected
		// Check both single selection and multiple selection
		isCurrentlySelected := (m.mainWindow.selectedMessage != nil &&
			m.mainWindow.selectedMessage.Message.UID == message.Message.UID &&
			m.mainWindow.selectedMessage.AccountName == message.AccountName) ||
			m.mainWindow.isMessageSelected(messageID)
		m.setSelected(isCurrentlySelected)
	}
}

// createMessageListWithRightClick creates a message list with right-click support
func (mw *MainWindow) createMessageListWithRightClick() fyne.CanvasObject {
	// Create message list with custom items that handle right-clicks directly
	mw.logger.Debug("createMessageListWithRightClick: Creating message list with custom items")
	return newMessageListWithRightClick(mw)
}

// applyTheme applies the theme setting from configuration to the Fyne app
func applyTheme(app fyne.App, themeSetting string, logger *logging.Logger) {
	switch themeSetting {
	case "dark":
		app.Settings().SetTheme(theme.DarkTheme())
		logger.Debug("Applied dark theme")
	case "light":
		app.Settings().SetTheme(theme.LightTheme())
		logger.Debug("Applied light theme")
	case "auto", "":
		// For auto theme, we could detect system theme, but for now default to light
		// In the future, this could use system theme detection
		app.Settings().SetTheme(theme.LightTheme())
		logger.Debug("Applied auto theme (defaulting to light)")
	default:
		logger.Warn("Unknown theme setting '%s', defaulting to light theme", themeSetting)
		app.Settings().SetTheme(theme.LightTheme())
	}
}

// NewMainWindow creates a new main window
func NewMainWindow(app fyne.App, cfg config.ConfigManager) *MainWindow {
	logger := logging.NewComponent("ui")
	logger.Debug("Creating main window")

	// Apply theme from configuration
	applyTheme(app, cfg.GetUI().Theme, logger)

	window := app.NewWindow("gommail client")

	// Set application icon from embedded data
	iconResource := resources.GetAppIcon()
	window.SetIcon(iconResource)
	logger.Debug("Window icon set successfully from embedded data")

	// Get UI configuration
	uiConfig := cfg.GetUI()
	window.Resize(fyne.NewSize(float32(uiConfig.WindowSize.Width), float32(uiConfig.WindowSize.Height)))
	logger.Debug("Main window created with size: %dx%d", uiConfig.WindowSize.Width, uiConfig.WindowSize.Height)

	// Initialize email components
	logger.Debug("Initializing email components")
	cacheConfig := cfg.GetCache()
	processor := email.NewMessageProcessorWithAttachments(cacheConfig.Directory, cacheConfig.Directory+"/downloads")

	// Initialize cache
	emailCache := cache.New(cacheConfig.Directory, cacheConfig.Compression, cacheConfig.MaxSizeMB)
	logger.Debug("Cache initialized: directory=%s, compression=%v, maxSizeMB=%d",
		cacheConfig.Directory, cacheConfig.Compression, cacheConfig.MaxSizeMB)

	// Initialize notification manager
	// Get icon path from embedded resources
	iconPath, err := resources.GetAppIconPath()
	if err != nil {
		logger.Warn("Failed to create temporary icon file for notifications: %v", err)
		iconPath = "" // Continue without icon
	}

	notificationConfig := notification.Config{
		Enabled:        uiConfig.Notifications.Enabled,
		DefaultTimeout: time.Duration(uiConfig.Notifications.TimeoutSeconds) * time.Second,
		AppName:        "gommail client",
		AppIcon:        iconPath,
	}
	notificationMgr, err := notification.NewManager(notificationConfig)
	if err != nil {
		logger.Error("Failed to initialize notification manager: %v", err)
		// Continue without notifications
		notificationMgr = nil
	} else {
		logger.Debug("Notification manager initialized")
	}

	// Initialize addressbook manager
	// Extract profile from config manager (assuming it's a PreferencesConfig)
	profile := "default"
	if prefsConfig, ok := cfg.(*config.PreferencesConfig); ok {
		profile = prefsConfig.GetProfile()
	}
	addressbookMgr := addressbook.NewManager(cfg, profile)
	logger.Debug("Addressbook manager initialized")

	// Create MessageViewController
	showHTMLByDefault := uiConfig.DefaultMessageView != "text"
	messageViewController := controllers.NewMessageViewController(processor.AttachmentManager, showHTMLByDefault)

	// Create FolderController
	folderController := controllers.NewFolderController(window)

	// Create AccountController
	accountController := controllers.NewAccountController(cfg, emailCache)

	// Create status bar early so it can be passed to controllers
	statusBar := widget.NewLabel("Ready")

	// Create MessageListController
	messageListController := controllers.NewMessageListController(accountController, statusBar, window)

	mw := &MainWindow{
		app:                   app,
		window:                window,
		config:                cfg,
		processor:             processor,
		attachmentManager:     processor.AttachmentManager,
		cache:                 emailCache,
		notificationMgr:       notificationMgr,
		addressbookMgr:        addressbookMgr,
		messages:              make([]email.MessageIndexItem, 0),
		autoSelectionDone:     false,
		sortBy:                SortByDate,     // Default sort by date
		sortOrder:             SortDescending, // Newest first
		tooltipLayerEnabled:   true,
		selectionManager:      controllers.NewSelectionManager(),
		messageViewController: messageViewController,
		folderController:      folderController,
		accountController:     accountController,
		messageListController: messageListController,
		statusBar:             statusBar,
		logger:                logger,
	}

	// Initialize debouncers for refresh operations (500ms delay)
	mw.folderRefreshDebouncer = NewCallbackDebouncer(500 * time.Millisecond)
	mw.messageRefreshDebouncer = NewCallbackDebouncer(500 * time.Millisecond)
	mw.accountRefreshDebouncer = NewCallbackDebouncer(500 * time.Millisecond)
	logger.Debug("Refresh debouncers initialized")

	// Set up selection manager callbacks
	mw.selectionManager.SetCallbacks(
		func() {
			// onSelectionChanged - refresh the message list UI
			if mw.messageList != nil {
				mw.refreshMessageList()
			}
		},
		func(index int) {
			// onMessageSelected - fetch and display the message
			if index >= 0 && index < len(mw.messages) {
				go mw.fetchAndDisplayMessage(&mw.messages[index])
			}
		},
	)

	// Set up folder controller callbacks
	mw.folderController.SetCallbacks(
		func(folder string) {
			// onFolderSelected - load messages for the selected folder
			mw.logger.Debug("Folder selected via controller: %s", folder)
		},
		func() {
			// onFoldersChanged - refresh the folder list UI
			mw.logger.Debug("Folders changed, UI should be updated")
		},
		func(folderName string, count int) {
			// onFolderCountUpdated - folder message count updated
			mw.logger.Debug("Folder %s count updated to %d", folderName, count)
		},
	)

	// Set up message list controller callback to keep messages in sync
	mw.messageListController.SetOnMessagesChanged(func() {
		// Sync messages from controller to MainWindow for backward compatibility
		mw.messages = mw.messageListController.GetMessages()
	})
	mw.messageListController.SetOnRefreshRequested(func() {
		mw.refreshMessageListView()
	})

	mw.setupUI()

	// Load cached data and perform auto-selection
	mw.loadCachedDataAndAutoSelect()

	return mw
}

// showAddressbook displays the addressbook dialog
func (mw *MainWindow) showAddressbook() {
	opts := AddressbookDialogOptions{
		OnClosed: func() {
			mw.statusBar.SetText("Addressbook dialog closed")
		},
	}

	dialog := NewAddressbookDialog(mw.app, mw.addressbookMgr, mw.config, opts)
	dialog.Show()
}

// configManagerToConfig creates a temporary *config.Config from ConfigManager for UI components
func (mw *MainWindow) configManagerToConfig() *config.Config {
	return &config.Config{
		Accounts:    mw.config.GetAccounts(),
		UI:          mw.config.GetUI(),
		Cache:       mw.config.GetCache(),
		Logging:     mw.config.GetLogging(),
		Tracing:     mw.config.GetTracing(),
		Addressbook: mw.config.GetAddressbook(),
	}
}

// setupUI initializes the user interface
func (mw *MainWindow) setupUI() {
	// Create account list with selection handling (includes unified inbox)
	mw.accountList = widget.NewList(
		func() int {
			// Add 1 for unified inbox
			accounts := mw.config.GetAccounts()
			return len(accounts) + 1
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("Account")
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			label := obj.(*widget.Label)
			accounts := mw.config.GetAccounts()

			if id == 0 {
				// First item is unified inbox
				label.SetText("📧 Unified Inbox")
				if mw.accountController.IsUnifiedInbox() {
					label.TextStyle = fyne.TextStyle{Bold: true}
				} else {
					label.TextStyle = fyne.TextStyle{}
				}
			} else if id-1 < len(accounts) {
				// Regular accounts (offset by 1)
				account := &accounts[id-1]
				label.SetText(account.Name)

				// Show connection status
				if !mw.accountController.IsUnifiedInbox() && mw.accountController.GetCurrentAccount() == account {
					label.TextStyle = fyne.TextStyle{Bold: true}
				} else {
					label.TextStyle = fyne.TextStyle{}
				}
			}
		},
	)

	// Handle account selection
	mw.accountList.OnSelected = func(id widget.ListItemID) {
		if id == 0 {
			// Unified inbox selected
			mw.selectUnifiedInbox()
		} else {
			// Regular account selected (offset by 1)
			accounts := mw.config.GetAccounts()
			if id-1 < len(accounts) {
				mw.selectAccount(&accounts[id-1])
			}
		}
	}

	// Create folder list with dynamic content
	folderList := widget.NewList(
		func() int {
			return len(mw.folderController.GetFolders())
		},
		func() fyne.CanvasObject {
			return container.NewHBox(
				widget.NewIcon(theme.FolderIcon()),
				widget.NewLabel("Folder"),
				widget.NewLabel(""), // Message count
			)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			folders := mw.folderController.GetFolders()
			if id < len(folders) {
				folder := folders[id]
				hbox := obj.(*fyne.Container)
				label := hbox.Objects[1].(*widget.Label)
				countLabel := hbox.Objects[2].(*widget.Label)

				label.SetText(folder.Name)

				// Show message count if available
				if folder.MessageCount > 0 {
					countLabel.SetText(fmt.Sprintf("(%d)", folder.MessageCount))
				} else {
					countLabel.SetText("")
				}

				// Highlight current folder
				if mw.folderController.GetCurrentFolder() == folder.Name {
					label.TextStyle = fyne.TextStyle{Bold: true}
				} else {
					label.TextStyle = fyne.TextStyle{}
				}
			}
		},
	)

	// Set folder list in controller
	mw.folderController.SetFolderList(folderList)

	// Handle folder selection
	folderList.OnSelected = func(id widget.ListItemID) {
		folders := mw.folderController.GetFolders()
		if id < len(folders) {
			mw.selectFolder(folders[id].Name)
		}
	}

	// Create message list with right-click support
	mw.messageListWithRightClick = newMessageListWithRightClick(mw)
	mw.messageList = mw.messageListWithRightClick.List

	// Set message list in controller
	mw.messageListController.SetMessageList(mw.messageList)

	// Add right-click context menu to message list
	mw.setupMessageListContextMenu()

	// Set up MessageViewController callback
	mw.messageViewController.SetOnViewToggled(func(showHTML bool) {
		fyne.Do(func() {
			mw.showHTMLContent = showHTML
			mw.updateMessageViewToggleButton()
			if mw.statusBar != nil {
				mw.statusBar.SetText(messageViewModeStatusText(showHTML))
			}
		})

		// When view is toggled, refresh the current message display
		mw.messagesMu.RLock()
		msg := mw.selectedMessage
		mw.messagesMu.RUnlock()
		if msg != nil {
			go mw.fetchAndDisplayMessage(msg)
		}
	})

	// Initialize the message view with default text
	mw.messageViewController.ClearMessageView()

	// Create sorting controls (kept for toolbar if needed)
	mw.sortByButton = widget.NewButton("Sort: Date", func() {
		mw.messageListController.ShowSortMenu()
	})
	mw.sortOrderButton = widget.NewButton("↓", func() {
		mw.messageListController.ToggleSortOrder()
	})

	// Set sort buttons in controller
	mw.messageListController.SetSortButtons(mw.sortByButton, mw.sortOrderButton)
	mw.messageListController.UpdateSortButtons()

	// Create custom toolbar with tooltip-enabled buttons

	// Create tooltip-enabled buttons
	composeWithTooltip := CreateTooltipButtonWithIcon("", "Compose new message", theme.MailComposeIcon(), func() {
		mw.composeMessage()
	})
	replyWithTooltip := CreateTooltipButtonWithIcon("", "Reply to selected message", theme.MailReplyIcon(), func() {
		mw.replyToMessage()
	})
	replyAllWithTooltip := CreateTooltipButtonWithIcon("", "Reply to all recipients", theme.MailReplyAllIcon(), func() {
		mw.replyAllToMessage()
	})
	forwardWithTooltip := CreateTooltipButtonWithIcon("", "Forward selected message", theme.MailForwardIcon(), func() {
		mw.forwardMessage()
	})
	clearCacheWithTooltip := CreateTooltipButtonWithIcon("", "Clear cache and refresh", theme.ViewRefreshIcon(), func() {
		mw.clearCache()
	})
	mw.toggleViewButton = CreateTooltipButtonWithIcon(messageViewModeButtonLabel(mw.messageViewController.IsShowingHTML()), messageViewModeButtonTooltip(mw.messageViewController.IsShowingHTML()), theme.DocumentIcon(), func() {
		mw.toggleHTMLView()
	})
	saveAttachmentsWithTooltip := CreateTooltipButtonWithIcon("", "Save all attachments", theme.DocumentSaveIcon(), func() {
		mw.saveAttachments()
	})
	deleteWithTooltip := CreateTooltipButtonWithIcon("", "Move to trash", theme.DeleteIcon(), func() {
		mw.moveToTrash()
	})
	// Create settings button
	settingsWithTooltip := CreateTooltipButtonWithIcon("", "Settings", theme.SettingsIcon(), func() {
		mw.showSettings()
	})

	// Create search button
	searchWithTooltip := CreateTooltipButtonWithIcon("", "Search messages", theme.SearchIcon(), func() {
		mw.showSearch()
	})

	// Create about button
	aboutWithTooltip := CreateTooltipButtonWithIcon("", "About", theme.InfoIcon(), func() {
		mw.showAbout()
	})

	// Create toolbar container
	mw.toolbar = container.NewHBox(
		composeWithTooltip,
		replyWithTooltip,
		replyAllWithTooltip,
		forwardWithTooltip,
		widget.NewSeparator(),
		clearCacheWithTooltip,
		mw.toggleViewButton,
		saveAttachmentsWithTooltip,
		deleteWithTooltip,
		widget.NewSeparator(),
		settingsWithTooltip,
		searchWithTooltip,
		aboutWithTooltip,
	)

	// Layout the UI
	leftPanel := container.NewVSplit(
		container.NewBorder(
			widget.NewLabel("Accounts"), nil, nil, nil,
			mw.accountList,
		),
		container.NewBorder(
			widget.NewLabel("Folders"), nil, nil, nil,
			mw.folderController.GetFolderList(),
		),
	)
	leftPanel.SetOffset(0.3)

	// Create column headers that perfectly align with message list columns
	// Use the same proportional sizing approach as the message list

	// Create header buttons
	mw.logger.Debug("Creating header buttons")
	mw.dateHeaderBtn = widget.NewButton("Date", func() {
		mw.logger.Debug("Date header button clicked")
		mw.messageListController.SetSortBy(controllers.SortByDate)
	})

	mw.senderHeaderBtn = widget.NewButton("Sender", func() {
		mw.logger.Debug("Sender header button clicked")
		mw.messageListController.SetSortBy(controllers.SortBySender)
	})

	mw.subjectHeaderBtn = widget.NewButton("Subject", func() {
		mw.logger.Debug("Subject header button clicked")
		mw.messageListController.SetSortBy(controllers.SortBySubject)
	})
	mw.logger.Debug("Header buttons created successfully")

	// Set header buttons in controller
	mw.messageListController.SetHeaderButtons(mw.dateHeaderBtn, mw.senderHeaderBtn, mw.subjectHeaderBtn)

	// Update button text to show current sort state
	mw.messageListController.UpdateColumnHeaders()

	// Create header with proportional layout matching message list exactly
	// No icon space needed - clean alignment with message content

	// Date column (fixed width to match message date display)
	dateHeaderContainer := container.NewWithoutLayout(mw.dateHeaderBtn)
	dateHeaderContainer.Resize(fyne.NewSize(messageDateColumnWidth, 32))
	mw.dateHeaderBtn.Resize(fyne.NewSize(messageDateColumnWidth, 32))

	// Sender column (fixed width to match message sender display)
	senderHeaderContainer := container.NewWithoutLayout(mw.senderHeaderBtn)
	senderHeaderContainer.Resize(fyne.NewSize(messageSenderColumnWidth, 32))
	mw.senderHeaderBtn.Resize(fyne.NewSize(messageSenderColumnWidth, 32))

	// Create the header layout using border to give subject remaining space
	leftSide := container.NewHBox(dateHeaderContainer, senderHeaderContainer)
	messageHeader := container.NewBorder(nil, nil, leftSide, nil, mw.subjectHeaderBtn)

	messageListWithHeader := container.NewBorder(
		messageHeader, nil, nil, nil,
		mw.messageListWithRightClick,
	)

	// Wrap message container in scroll container for vertical scrolling
	// RichText widgets work well with scroll containers for formatted content display
	messageScrollContainer := container.NewScroll(mw.messageViewController.GetMessageContainer())

	rightPanel := container.NewVSplit(
		messageListWithHeader,  // Remove wasteful "Messages" label
		messageScrollContainer, // Container with message and attachments wrapped in scroll
	)
	rightPanel.SetOffset(0.4)

	mainContent := container.NewHSplit(leftPanel, rightPanel)
	mainContent.SetOffset(0.25)

	content := container.NewBorder(
		mw.toolbar, mw.statusBar, nil, nil,
		mainContent,
	)

	// Wrap content with tooltip layer
	contentWithTooltips := AddTooltipLayer(content, mw.window.Canvas())
	mw.window.SetContent(contentWithTooltips)
	mw.updateMessageViewToggleButton()

	// Set up keyboard shortcuts
	mw.setupKeyboardShortcuts()
}

// setupKeyboardShortcuts configures keyboard shortcuts for the main window
func (mw *MainWindow) setupKeyboardShortcuts() {
	// Keyboard shortcuts are registered in registerKeyboardShortcuts() method
	mw.registerKeyboardShortcuts()
}

func (mw *MainWindow) updateMessageViewToggleButton() {
	if mw.toggleViewButton == nil || mw.messageViewController == nil {
		return
	}

	showHTML := mw.messageViewController.IsShowingHTML()
	mw.showHTMLContent = showHTML
	mw.toggleViewButton.SetText(messageViewModeButtonLabel(showHTML))
	mw.toggleViewButton.SetToolTip(messageViewModeButtonTooltip(showHTML))
	mw.toggleViewButton.Refresh()
	canvas.Refresh(mw.toggleViewButton)
}

// setupMessageListContextMenu sets up the right-click context menu for the message list
func (mw *MainWindow) setupMessageListContextMenu() {
	mw.logger.Debug("Setting up message list context menu")

	// Add keyboard shortcuts as alternatives
	canvas := mw.window.Canvas()

	// Use Shift+F10 as context menu shortcut (standard Windows/Linux shortcut)
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyF10, Modifier: fyne.KeyModifierShift}, func(shortcut fyne.Shortcut) {
		mw.logger.Debug("Context menu shortcut (Shift+F10) triggered")
		mw.showMessageContextMenu()
	})

	// Also add Ctrl+M as an alternative context menu shortcut
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyM, Modifier: fyne.KeyModifierControl}, func(shortcut fyne.Shortcut) {
		mw.logger.Debug("Context menu shortcut (Ctrl+M) triggered")
		mw.showMessageContextMenu()
	})

	mw.logger.Debug("Message list context menu setup complete")
}

// ============================================================================

// ShowAndRun shows the window and runs the application
func (mw *MainWindow) ShowAndRun() {
	mw.window.ShowAndRun()
}

// Show shows the window without running the event loop
func (mw *MainWindow) Show() {
	mw.logger.Debug("Showing main window")

	// Set up window close handler to cleanup IMAP connections
	mw.window.SetCloseIntercept(func() {
		mw.logger.Debug("Window close intercepted, performing cleanup")
		mw.cleanup(true) // true = fast shutdown mode
		mw.window.Close()
	})

	mw.window.Show()
	mw.logger.Debug("Main window displayed")

	// Start global health monitoring
	mw.startGlobalHealthMonitoring()
}

// loadCachedDataAndAutoSelect loads cached data and performs automatic selection
func (mw *MainWindow) loadCachedDataAndAutoSelect() {
	// Preload account caches for better responsiveness
	mw.preloadAccountCaches()

	// Auto-select unified inbox if accounts are available
	accounts := mw.config.GetAccounts()
	if len(accounts) > 0 && !mw.autoSelectionDone {
		// Use a timer instead of goroutine for the delay
		time.AfterFunc(100*time.Millisecond, func() {
			fyne.Do(func() {
				// Select unified inbox (index 0)
				mw.accountList.Select(0)
				mw.selectUnifiedInbox()
			})
		})
	}
}

// cleanup handles cleanup of resources when the window is closed
// fastShutdown: if true, skips optional cleanup steps for faster application termination
func (mw *MainWindow) cleanup(fastShutdown bool) {
	// Wait for background goroutines to finish (with a timeout to avoid blocking forever)
	done := make(chan struct{})
	go func() {
		mw.backgroundWg.Wait()
		close(done)
	}()
	select {
	case <-done:
		mw.logger.Debug("All background goroutines finished")
	case <-time.After(5 * time.Second):
		mw.logger.Warn("Timed out waiting for background goroutines to finish")
	}

	// Cancel any active read timer
	mw.cancelReadTimer()

	if fastShutdown {
		mw.logger.Debug("Fast shutdown mode - skipping optional cleanup steps")

		// Force stop monitoring without graceful cleanup
		mw.stopFolderMonitoringFast()
		mw.stopUnifiedInboxMonitoringFast()
		mw.stopGlobalHealthMonitoring()

		// Stop debouncers
		if mw.folderRefreshDebouncer != nil {
			mw.folderRefreshDebouncer.Stop()
		}
		if mw.messageRefreshDebouncer != nil {
			mw.messageRefreshDebouncer.Stop()
		}
		if mw.accountRefreshDebouncer != nil {
			mw.accountRefreshDebouncer.Stop()
		}

		// Force stop and disconnect IMAP clients without server communication
		if mw.imapClient != nil {
			mw.logger.Debug("Force stopping and disconnecting main IMAP client")
			mw.imapClient.Stop() // Stop the worker
			mw.imapClient.ForceDisconnect()
		}

		// Force stop and disconnect all account clients
		mw.accountController.CloseAllClients()
	} else {
		// Graceful cleanup for normal operations
		mw.logger.Debug("Graceful cleanup mode")

		// Stop folder monitoring
		mw.stopFolderMonitoring()
		mw.stopUnifiedInboxMonitoring()
		mw.stopGlobalHealthMonitoring()

		// Stop debouncers
		if mw.folderRefreshDebouncer != nil {
			mw.folderRefreshDebouncer.Stop()
		}
		if mw.messageRefreshDebouncer != nil {
			mw.messageRefreshDebouncer.Stop()
		}
		if mw.accountRefreshDebouncer != nil {
			mw.accountRefreshDebouncer.Stop()
		}

		// Stop and disconnect main IMAP client
		if mw.imapClient != nil {
			mw.logger.Debug("Stopping and disconnecting main IMAP client")
			mw.imapClient.Stop() // Stop the worker
			mw.imapClient.Disconnect()
		}

		// Stop and disconnect all account clients and clear the map
		mw.accountController.CloseAllClients()
	}
}

// sortFolders sorts folders to put INBOX first, then other important folders, then alphabetically
// Delegates to FolderController
func (mw *MainWindow) sortFolders(folders []email.Folder) []email.Folder {
	return mw.folderController.SortFolders(folders)
}

// sortMessages sorts the messages based on current sort criteria
// Delegates to MessageListController
func (mw *MainWindow) sortMessages() {
	// Update controller's IMAP/SMTP clients before sorting
	mw.messageListController.SetIMAPClient(mw.imapClient)
	mw.messageListController.SetSMTPClient(mw.smtpClient)

	// Sync sort state to controller
	var controllerSortBy controllers.SortBy
	switch mw.sortBy {
	case SortByDate:
		controllerSortBy = controllers.SortByDate
	case SortBySender:
		controllerSortBy = controllers.SortBySender
	case SortBySubject:
		controllerSortBy = controllers.SortBySubject
	default:
		controllerSortBy = controllers.SortByDate
	}

	// Set messages and sort criteria in controller
	mw.messageListController.SetMessages(mw.messages)
	mw.messageListController.SetSortCriteria(controllerSortBy, controllers.SortOrder(mw.sortOrder))
	mw.messageListController.SortMessages()

	// Sync back to MainWindow
	mw.messages = mw.messageListController.GetMessages()
}

// getSenderName extracts the sender name from a message
// Delegates to MessageListController
func (mw *MainWindow) getSenderName(msg email.Message) string {
	return mw.messageListController.GetSenderName(msg)
}

// getSenderNameFromIndexItem extracts the sender name from a MessageIndexItem
// Delegates to MessageListController
func (mw *MainWindow) getSenderNameFromIndexItem(item email.MessageIndexItem) string {
	return mw.messageListController.GetSenderNameFromIndexItem(item)
}

// convertMessagesToIndexItems converts regular messages to MessageIndexItem for the current account
// Delegates to MessageListController
func (mw *MainWindow) convertMessagesToIndexItems(messages []email.Message, folderName string) []email.MessageIndexItem {
	// Update controller's IMAP/SMTP clients before conversion
	mw.messageListController.SetIMAPClient(mw.imapClient)
	mw.messageListController.SetSMTPClient(mw.smtpClient)

	return mw.messageListController.ConvertMessagesToIndexItems(messages, folderName)
}

// setSortBy changes the sort criteria and refreshes the message list
// Delegates to MessageListController
func (mw *MainWindow) setSortBy(sortBy SortBy) {
	// Convert MainWindow SortBy to controller SortBy
	var controllerSortBy controllers.SortBy
	switch sortBy {
	case SortByDate:
		controllerSortBy = controllers.SortByDate
	case SortBySender:
		controllerSortBy = controllers.SortBySender
	case SortBySubject:
		controllerSortBy = controllers.SortBySubject
	default:
		controllerSortBy = controllers.SortByDate
	}

	// Update controller's IMAP/SMTP clients and messages before sorting
	mw.messageListController.SetIMAPClient(mw.imapClient)
	mw.messageListController.SetSMTPClient(mw.smtpClient)
	mw.messageListController.SetMessages(mw.messages)

	// Delegate to controller
	mw.messageListController.SetSortBy(controllerSortBy)

	// Sync back to MainWindow
	mw.messages = mw.messageListController.GetMessages()
	mw.sortBy = sortBy
	mw.sortOrder = SortOrder(mw.messageListController.GetSortOrder())
}

// getSortName returns a human-readable name for the current sort criteria
// Delegates to MessageListController
func (mw *MainWindow) getSortName() string {
	return mw.messageListController.GetSortName()
}

// updateSortButtons updates the sort button text to reflect current state
// Delegates to MessageListController
func (mw *MainWindow) updateSortButtons() {
	mw.messageListController.UpdateSortButtons()
}

// updateColumnHeaders updates the column header buttons to show current sort state
// Delegates to MessageListController
func (mw *MainWindow) updateColumnHeaders() {
	mw.messageListController.UpdateColumnHeaders()
}

// showSortMenu displays a menu to select sort criteria
// Delegates to MessageListController
func (mw *MainWindow) showSortMenu() {
	mw.messageListController.ShowSortMenu()
}

// toggleSortOrder toggles between ascending and descending sort order
// Delegates to MessageListController
func (mw *MainWindow) toggleSortOrder() {
	// Update controller's IMAP/SMTP clients and messages before toggling
	mw.messageListController.SetIMAPClient(mw.imapClient)
	mw.messageListController.SetSMTPClient(mw.smtpClient)
	mw.messageListController.SetMessages(mw.messages)

	// Delegate to controller
	mw.messageListController.ToggleSortOrder()

	// Sync back to MainWindow
	mw.messages = mw.messageListController.GetMessages()
	mw.sortOrder = SortOrder(mw.messageListController.GetSortOrder())
}

// clearAccountState clears the current account state when switching accounts
func (mw *MainWindow) clearAccountState() {
	// Clear current message list and folder selection when switching accounts
	// This ensures the UI immediately reflects the account switch
	mw.messagesMu.Lock()
	mw.messages = []email.MessageIndexItem{}
	mw.selectedMessage = nil
	mw.messagesMu.Unlock()

	// Clear folder list and current folder to prevent showing folders from previous account
	mw.folderController.SetFolders([]email.Folder{})
	mw.folderController.SelectFolder("") // Clear current folder

	// Clear unified inbox state
	mw.accountController.SetUnifiedInbox(false)
}

// selectAccount handles account selection and connects to the email server
func (mw *MainWindow) selectAccount(account *config.Account) {
	mw.logger.Info("Selecting account: %s (%s)", account.Name, account.Email)
	oldClient := mw.imapClient

	mw.accountController.SetCurrentAccount(account)
	mw.statusBar.SetText(fmt.Sprintf("Connecting to %s...", account.Name))

	// Stop monitoring when switching accounts
	mw.stopFolderMonitoring()
	mw.stopUnifiedInboxMonitoring()

	// Clear account state
	mw.clearAccountState()

	// Refresh UI components to show the cleared state
	mw.messageList.UnselectAll()
	mw.refreshMessageList()
	mw.updateMessageViewer("")
	if folderList := mw.folderController.GetFolderList(); folderList != nil {
		folderList.UnselectAll()
	}

	// Cancel any active read timer when switching accounts
	mw.cancelReadTimer()

	newClient, needsConnect, err := mw.prepareIMAPClientForAccountSelection(account)
	if err != nil {
		mw.logger.Error("Failed to prepare IMAP client for account %s: %v", account.Name, err)
		mw.statusBar.SetText(fmt.Sprintf("Failed to start IMAP worker for %s", account.Name))
		return
	}

	// Clean up the previous single-account client if it differs from the selected account client.
	// This avoids leaking extra workers while still allowing reuse of an already-managed client.
	if oldClient != nil && oldClient != newClient {
		mw.logger.Debug("Cleaning up previous IMAP client before switching to account: %s", account.Name)
		if err := oldClient.Disconnect(); err != nil {
			mw.logger.Warn("Failed to disconnect previous IMAP client while switching accounts: %v", err)
		}
		oldClient.Stop()
		if mw.imapClient == oldClient {
			mw.imapClient = nil
		}
	}

	mw.imapClient = newClient

	// Create SMTP client
	mw.logger.Debug("Creating SMTP client for %s (host: %s:%d, TLS: %v)",
		account.Name, account.SMTP.Host, account.SMTP.Port, account.SMTP.TLS)
	smtpConfig := email.ServerConfig{
		Host:     account.SMTP.Host,
		Port:     account.SMTP.Port,
		Username: account.SMTP.Username,
		Password: account.SMTP.Password,
		TLS:      account.SMTP.TLS,
	}
	mw.smtpClient = smtp.NewClient(smtpConfig)

	// IMMEDIATELY load cached folders for instant UI responsiveness
	// Access cache directly to avoid waiting for worker connection
	cachedFolders, cachedFoldersFound := mw.loadCachedFoldersDirectly(account.Name)
	if cachedFoldersFound {
		sortedFolders := mw.folderController.SortFolders(cachedFolders)
		mw.folderController.SetFolders(sortedFolders)
		mw.statusBar.SetText(fmt.Sprintf("Loaded %d folders from cache - connecting...", len(cachedFolders)))
		mw.logger.Debug("Immediately loaded %d cached folders for account %s", len(cachedFolders), account.Name)

		// Auto-select INBOX if available and auto-selection hasn't been done
		if !mw.autoSelectionDone {
			if mw.folderController.AutoSelectInbox() {
				mw.autoSelectionDone = true
			} else if mw.folderController.AutoSelectFirstFolder() {
				mw.autoSelectionDone = true
			}
		}
	} else {
		mw.logger.Debug("No cached folders available for account %s, will load from server", account.Name)
		mw.statusBar.SetText(fmt.Sprintf("Connecting to %s...", account.Name))
	}

	// If a connected managed client already exists for this account, reuse it instead of
	// spawning a duplicate worker-backed client.
	if !needsConnect {
		mw.logger.Info("Reusing existing connected IMAP client for account: %s", account.Name)
		newClient.SetConnectionStateCallback(func(event email.ConnectionEvent) {
			mw.handleConnectionStateChange(account.Name, event)
		})

		if cachedFoldersFound {
			mw.statusBar.SetText(fmt.Sprintf("Connected to %s - using cached folders", account.Name))
		} else {
			mw.statusBar.SetText(fmt.Sprintf("Connected to %s - syncing folder subscriptions", account.Name))
			mw.syncFolderSubscriptionsInBackground(account.Name, newClient)
		}
		return
	}

	mw.setCurrentAccountConnectInProgress(true)

	// Now connect and optionally sync folder subscriptions in background
	go func(client *imap.ClientWrapper, selectedAccount *config.Account, usedCachedFolders bool) {
		defer mw.setCurrentAccountConnectInProgress(false)

		// Set up connection state callback before connecting
		client.SetConnectionStateCallback(func(event email.ConnectionEvent) {
			mw.handleConnectionStateChange(selectedAccount.Name, event)
		})

		// Now connect to server and refresh data in background
		err := client.Connect()
		if err != nil {
			fyne.Do(func() {
				mw.statusBar.SetText(fmt.Sprintf("Failed to connect to %s: %v", selectedAccount.Name, err))
			})
			return
		}

		// Clean up cached data for unsubscribed folders on startup
		// This ensures the cache stays clean even if previous cleanup operations were missed
		mw.logger.Debug("Performing startup cleanup of unsubscribed folder cache for account: %s", selectedAccount.Name)
		if cleanupErr := client.CleanupUnsubscribedFolderCache(); cleanupErr != nil {
			mw.logger.Warn("Failed to cleanup unsubscribed folder cache for %s: %v", selectedAccount.Name, cleanupErr)
			// Don't fail the connection for cleanup errors, just log them
		} else {
			mw.logger.Debug("Successfully cleaned up unsubscribed folder cache for account: %s", selectedAccount.Name)
		}

		// Subscribe to default folders (INBOX, Sent, Drafts, Trash) for new accounts
		// This is done after connection but before folder refresh to ensure subscriptions are in place
		mw.logger.Debug("Attempting to subscribe to default folders for account: %s", selectedAccount.Name)
		if err := client.SubscribeToDefaultFolders(selectedAccount.SentFolder, selectedAccount.TrashFolder); err != nil {
			mw.logger.Warn("Failed to subscribe to some default folders for %s: %v", selectedAccount.Name, err)
			// Don't fail the connection for subscription errors, just log them
		} else {
			mw.logger.Info("Successfully subscribed to default folders for account: %s", selectedAccount.Name)
		}

		if usedCachedFolders {
			fyne.Do(func() {
				mw.statusBar.SetText(fmt.Sprintf("Connected to %s - using cached folders", selectedAccount.Name))
			})
			return
		}

		// Sync folder subscriptions in background and update UI if changes are detected
		mw.syncFolderSubscriptionsInBackground(selectedAccount.Name, client)

		fyne.Do(func() {
			mw.statusBar.SetText(fmt.Sprintf("Connected to %s - syncing folder subscriptions", selectedAccount.Name))
		})
	}(newClient, account, cachedFoldersFound)
}

func (mw *MainWindow) prepareIMAPClientForAccountSelection(account *config.Account) (*imap.ClientWrapper, bool, error) {
	if client, exists := mw.accountController.GetIMAPClientForAccount(account.Name); exists && client != nil {
		if client.IsConnected() {
			mw.logger.Debug("Reusing existing managed IMAP client for selected account: %s", account.Name)
			return client, false, nil
		}

		mw.logger.Debug("Managed IMAP client for account %s is disconnected, recreating it", account.Name)
		mw.accountController.CloseClientForAccount(account.Name)
	}

	mw.logger.Debug("Creating worker-based IMAP client for %s (host: %s:%d, TLS: %v)",
		account.Name, account.IMAP.Host, account.IMAP.Port, account.IMAP.TLS)
	imapConfig := email.ServerConfig{
		Host:     account.IMAP.Host,
		Port:     account.IMAP.Port,
		Username: account.IMAP.Username,
		Password: account.IMAP.Password,
		TLS:      account.IMAP.TLS,
	}

	var tracer io.Writer
	tracingConfig := mw.config.GetTracing()
	if tracingConfig.IMAP.Enabled {
		imapTracer := trace.NewIMAPTracer(true, tracingConfig.IMAP.Directory)
		tracer = imapTracer.GetAccountTracer(account.Name)
	}

	client := imap.NewClientWrapperWithCacheAndTracer(imapConfig, mw.cache, accountCacheKey(account.Name), tracer)
	if err := client.Start(); err != nil {
		return nil, false, fmt.Errorf("failed to start IMAP worker for %s: %w", account.Name, err)
	}

	mw.accountController.StoreIMAPClient(account.Name, client)
	return client, true, nil
}

// syncFolderSubscriptionsInBackground syncs folder subscriptions with server and updates UI if changes detected
func (mw *MainWindow) syncFolderSubscriptionsInBackground(accountName string, client *imap.ClientWrapper) {
	go func() {
		mw.logger.Debug("Starting background folder subscription sync for account: %s", accountName)

		// Get current cached folders for comparison
		currentFolders := mw.folderController.GetFolders()
		currentFoldersCopy := make([]email.Folder, len(currentFolders))
		copy(currentFoldersCopy, currentFolders)

		// Start background refresh of folders (this will update cache with subscribed folders)
		client.RefreshFoldersInBackground()

		// Wait a moment for background refresh to complete
		time.Sleep(2 * time.Second)

		// Get updated subscribed folders from cache
		updatedFolders, err := client.ListSubscribedFolders()
		if err != nil {
			mw.logger.Warn("Failed to get updated folders after sync: %v", err)
			return
		}

		// Check if folder list has changed
		if mw.folderController.FoldersChanged(currentFoldersCopy, updatedFolders) {
			mw.logger.Debug("Folder subscriptions changed, updating UI")
			fyne.Do(func() {
				sortedFolders := mw.folderController.SortFolders(updatedFolders)
				mw.folderController.SetFolders(sortedFolders)
				mw.statusBar.SetText(fmt.Sprintf("Connected to %s - %d folders synced", accountName, len(updatedFolders)))
			})
		} else {
			mw.logger.Debug("No folder subscription changes detected")
			fyne.Do(func() {
				mw.statusBar.SetText(fmt.Sprintf("Connected to %s - %d folders", accountName, len(updatedFolders)))
			})
		}
	}()
}

// foldersChanged compares two folder lists to detect changes
// Delegates to FolderController
func (mw *MainWindow) foldersChanged(current, updated []email.Folder) bool {
	return mw.folderController.FoldersChanged(current, updated)
}

// selectUnifiedInbox handles unified inbox selection by showing cached data first
// while reconnecting and refreshing in the background.
func (mw *MainWindow) selectUnifiedInbox() {
	mw.selectUnifiedInboxWithOptions(false)
}

// selectUnifiedInboxWithOptions handles unified inbox selection and can optionally
// force a fresh refresh for explicit reload paths.
func (mw *MainWindow) selectUnifiedInboxWithOptions(forceRefresh bool) {
	mw.logger.Info("Selecting unified inbox")

	// Set unified inbox state
	mw.accountController.SetUnifiedInbox(true)
	mw.accountController.ClearCurrentAccount()
	mw.folderController.SelectFolder("") // Clear current folder
	if forceRefresh {
		mw.statusBar.SetText("Refreshing unified inbox...")
	} else {
		mw.statusBar.SetText("Loading unified inbox...")
	}

	// Clear current selection and message viewer (but keep messages until new ones load)
	mw.selectedMessage = nil
	mw.updateMessageViewer("")
	if folderList := mw.folderController.GetFolderList(); folderList != nil {
		folderList.UnselectAll()
	}

	// Cancel any active read timer
	mw.cancelReadTimer()

	// Stop any existing monitoring
	mw.stopFolderMonitoring()
	mw.stopUnifiedInboxMonitoring()

	// Clear folder list when unified inbox is selected
	mw.folderController.SetFolders([]email.Folder{})
	if folderList := mw.folderController.GetFolderList(); folderList != nil {
		folderList.Refresh()
	}

	// Load unified messages (don't clear messages array until new ones are ready)
	go mw.loadUnifiedInboxMessages(forceRefresh)
}

func (mw *MainWindow) startUnifiedInboxMonitoringFromConnectedClients() {
	connectedClientCount := 0
	mw.accountController.ForEachClient(func(accountName string, client *imap.ClientWrapper) {
		if client == nil || !client.IsConnected() {
			return
		}

		client.MarkInitialSyncComplete("INBOX")
		connectedClientCount++
	})

	if connectedClientCount > 0 {
		mw.logger.Info("Starting unified inbox monitoring from %d already-connected account clients", connectedClientCount)
		mw.startUnifiedInboxMonitoring()
	}
}

// loadUnifiedInboxMessages loads messages from all accounts' INBOX folders.
// It always works toward a live connected unified inbox, but displays cached data
// immediately when available so the UI is responsive.
func (mw *MainWindow) loadUnifiedInboxMessages(forceRefresh bool) {
	mw.logger.Info("Loading unified inbox messages from all accounts (forceRefresh=%v)", forceRefresh)

	// Try cached unified inbox messages first for immediate display.
	cachedMessages, cachedMessagesFound := mw.loadUnifiedInboxFromCache()
	if cachedMessagesFound {
		fyne.Do(func() {
			if !mw.accountController.IsUnifiedInbox() {
				mw.logger.Debug("User switched away from unified inbox before cached load completed")
				return
			}

			mw.messages = cachedMessages
			mw.sortMessages()
			mw.messageList.UnselectAll()
			mw.refreshMessageList()
			mw.statusBar.SetText(fmt.Sprintf("Loaded %d cached messages from unified inbox (checking for updates...)", len(mw.messages)))
		})
	} else {
		// Show loading state immediately when cache is unavailable.
		fyne.Do(func() {
			mw.messages = []email.MessageIndexItem{}
			mw.messageList.UnselectAll()
			mw.refreshMessageList()
			mw.statusBar.SetText("Loading messages from all accounts...")
		})
	}

	// Start monitoring immediately for any accounts that are already connected,
	// then continue the full background refresh which will connect remaining
	// accounts and refresh the unified inbox contents.
	if !forceRefresh {
		mw.startUnifiedInboxMonitoringFromConnectedClients()
	}

	// Fetch fresh messages in background and wait for ALL accounts to complete
	// before replacing the unified mailbox contents.
	mw.backgroundWg.Add(1)
	go func() {
		defer mw.backgroundWg.Done()
		mw.fetchFreshUnifiedInboxMessagesInBackground()
	}()
}

// fetchFreshUnifiedInboxMessagesInBackground fetches fresh messages without blocking the UI
func (mw *MainWindow) fetchFreshUnifiedInboxMessagesInBackground() {
	mw.logger.Info("Starting background fetch of fresh unified inbox messages")

	// Account clients are managed by AccountController
	accounts := mw.config.GetAccounts()
	mw.logger.Info("Fetching fresh messages from %d accounts for unified inbox (waiting for all to complete)", len(accounts))

	// Use a channel to collect results from parallel account processing
	type accountResult struct {
		accountName string
		messages    []email.MessageIndexItem
		err         error
	}
	resultChan := make(chan accountResult, len(accounts))

	// Track total accounts for status updates
	totalAccounts := int32(len(accounts))

	// Process accounts in parallel
	for _, account := range accounts {
		go func(acc config.Account) {
			// Check if we should continue (user might have switched away from unified inbox)
			if !mw.accountController.IsUnifiedInbox() {
				mw.logger.Debug("No longer in unified inbox mode, skipping account %s", acc.Name)
				resultChan <- accountResult{accountName: acc.Name, err: fmt.Errorf("unified inbox mode exited")}
				return
			}

			mw.logger.Debug("Background fetching fresh INBOX messages for account: %s", acc.Name)

			// Create or get IMAP client for this account
			client, err := mw.getOrCreateIMAPClient(&acc)
			if err != nil {
				mw.logger.Error("Failed to create IMAP client for account %s: %v", acc.Name, err)
				resultChan <- accountResult{accountName: acc.Name, err: err}
				return
			}

			// Fetch fresh messages from INBOX
			messages, err := client.FetchFreshMessages("INBOX", 0)
			if err != nil {
				mw.logger.Error("Failed to fetch fresh INBOX messages for account %s: %v", acc.Name, err)
				resultChan <- accountResult{accountName: acc.Name, err: err}
				return
			}

			mw.logger.Debug("Background fetched %d fresh messages from account %s INBOX", len(messages), acc.Name)

			// Create SMTP client for this account
			smtpClient := smtp.NewClient(email.ServerConfig{
				Host:     acc.SMTP.Host,
				Port:     acc.SMTP.Port,
				Username: acc.SMTP.Username,
				Password: acc.SMTP.Password,
				TLS:      acc.SMTP.TLS,
			})

			// Convert to MessageIndexItem
			var indexItems []email.MessageIndexItem
			for _, msg := range messages {
				indexItem := email.MessageIndexItem{
					Message:      msg,
					AccountName:  acc.Name,
					AccountEmail: acc.Email,
					FolderName:   "INBOX",
					IMAPClient:   client,
					SMTPClient:   smtpClient,
					AccountConfig: &email.AccountConfig{
						Name:        acc.Name,
						Email:       acc.Email,
						DisplayName: acc.DisplayName,
					},
				}
				indexItems = append(indexItems, indexItem)
			}

			// Update the cache for this account in background
			go func(accName string, msgs []email.Message) {
				mw.cacheAccountMessages(accName, msgs)
			}(acc.Name, messages)

			// Send result
			resultChan <- accountResult{accountName: acc.Name, messages: indexItems, err: nil}
		}(account)
	}

	// Collect results from all accounts - wait for ALL to complete before displaying
	go func() {
		var allNewMessages []email.MessageIndexItem
		var completedCount int32

		for i := 0; i < len(accounts); i++ {
			result := <-resultChan

			if result.err != nil {
				mw.logger.Info("Account %s completed with error: %v", result.accountName, result.err)
			} else {
				mw.logger.Info("Account %s completed successfully with %d messages", result.accountName, len(result.messages))
				allNewMessages = append(allNewMessages, result.messages...)
			}

			// Update status to show progress (but don't update UI yet)
			completedCount++
			fyne.Do(func() {
				mw.statusBar.SetText(fmt.Sprintf("Loading %d/%d accounts...", completedCount, totalAccounts))
			})
		}

		// ALL accounts processed - now update UI with all messages at once
		mw.logger.Info("All %d accounts completed, displaying %d total messages", len(accounts), len(allNewMessages))

		fyne.Do(func() {
			if !mw.accountController.IsUnifiedInbox() {
				mw.logger.Warn("User switched away from unified inbox, skipping final update")
				return // User switched away, don't update
			}

			// Set all messages at once
			mw.messagesMu.Lock()
			mw.messages = allNewMessages
			mw.selectedMessage = nil // previous pointer is now invalid
			mw.messagesMu.Unlock()
			mw.logger.Info("Set messages array to %d messages from all accounts", len(mw.messages))

			// Apply user's sort preferences
			mw.sortMessages()

			// Validate message array consistency
			if !mw.validateMessageArrayConsistency() {
				mw.logger.Error("Message array consistency check failed after unified inbox load")
			}

			// Force the list to update by clearing selection and refreshing
			mw.messageList.UnselectAll()
			mw.refreshMessageList()

			mw.logger.Info("Refreshed message list with %d total messages", len(mw.messages))

			// Auto-select first message if available
			if len(mw.messages) > 0 && !mw.autoSelectionDone {
				mw.messageList.Select(0)
				mw.selectMessage(0)
				mw.autoSelectionDone = true
			}

			// Mark initial sync complete for all account INBOX folders to enable notifications
			mw.accountController.ForEachClient(func(accountName string, client *imap.ClientWrapper) {
				client.MarkInitialSyncComplete("INBOX")
			})

			// Start monitoring for all accounts in unified inbox
			mw.startUnifiedInboxMonitoring()

			// Update status to show sync is complete
			mw.statusBar.SetText(fmt.Sprintf("Loaded %d messages from unified inbox", len(mw.messages)))
			mw.logger.Info("Background unified inbox refresh completed - %d total messages", len(mw.messages))
		})
	}()
}

// refreshSingleAccountInUnifiedInbox fetches fresh messages from a single account and merges them into unified inbox
func (mw *MainWindow) refreshSingleAccountInUnifiedInbox(accountName string) {
	mw.logger.Info("Refreshing single account %s in unified inbox", accountName)

	// Find the account config
	accounts := mw.config.GetAccounts()
	var targetAccount *config.Account
	for i := range accounts {
		if accounts[i].Name == accountName {
			targetAccount = &accounts[i]
			break
		}
	}

	if targetAccount == nil {
		mw.logger.Error("Account %s not found in config", accountName)
		return
	}

	// Get or create IMAP client for this account
	client, err := mw.getOrCreateIMAPClient(targetAccount)
	if err != nil {
		mw.logger.Error("Failed to get IMAP client for account %s: %v", accountName, err)
		return
	}

	// Fetch fresh messages from INBOX
	messages, err := client.FetchFreshMessages("INBOX", 0)
	if err != nil {
		mw.logger.Error("Failed to fetch fresh INBOX messages for account %s: %v", accountName, err)
		return
	}

	mw.logger.Info("Fetched %d fresh messages from account %s", len(messages), accountName)

	// Create SMTP client for this account
	smtpClient := smtp.NewClient(email.ServerConfig{
		Host:     targetAccount.SMTP.Host,
		Port:     targetAccount.SMTP.Port,
		Username: targetAccount.SMTP.Username,
		Password: targetAccount.SMTP.Password,
		TLS:      targetAccount.SMTP.TLS,
	})

	// Convert to MessageIndexItem
	var indexItems []email.MessageIndexItem
	for _, msg := range messages {
		indexItem := email.MessageIndexItem{
			Message:      msg,
			AccountName:  targetAccount.Name,
			AccountEmail: targetAccount.Email,
			FolderName:   "INBOX",
			IMAPClient:   client,
			SMTPClient:   smtpClient,
			AccountConfig: &email.AccountConfig{
				Name:        targetAccount.Name,
				Email:       targetAccount.Email,
				DisplayName: targetAccount.DisplayName,
			},
		}
		indexItems = append(indexItems, indexItem)
	}

	// Update UI with merged messages
	fyne.Do(func() {
		if !mw.accountController.IsUnifiedInbox() {
			mw.logger.Debug("User switched away from unified inbox, skipping update")
			return
		}

		beforeCount := len(mw.messages)

		// Merge new messages with existing ones, avoiding duplicates
		mw.mergeNewMessagesIntoUnifiedInbox(indexItems)

		afterCount := len(mw.messages)
		addedCount := afterCount - beforeCount

		if addedCount > 0 {
			mw.logger.Info("Added %d new messages from account %s (total now: %d)", addedCount, accountName, afterCount)

			// Apply user's sort preferences
			mw.sortMessages()

			// Force the list to update
			mw.messageList.UnselectAll()
			mw.refreshMessageList()

			mw.statusBar.SetText(fmt.Sprintf("Added %d new messages from %s", addedCount, accountName))
		} else {
			mw.logger.Debug("No new messages to add from account %s (all were duplicates)", accountName)
			mw.statusBar.SetText(fmt.Sprintf("No new messages from %s", accountName))
		}
	})
}

// mergeNewMessagesIntoUnifiedInbox merges new messages into the existing unified inbox, avoiding duplicates
func (mw *MainWindow) mergeNewMessagesIntoUnifiedInbox(newMessages []email.MessageIndexItem) {
	if len(newMessages) == 0 {
		mw.logger.Debug("mergeNewMessagesIntoUnifiedInbox: No new messages to merge")
		return
	}

	mw.logger.Info("mergeNewMessagesIntoUnifiedInbox: Starting merge of %d new messages into %d existing messages",
		len(newMessages), len(mw.messages))

	// Create a map of existing messages by account and UID for efficient duplicate detection
	existingMap := make(map[string]map[uint32]bool) // account -> uid -> exists
	for _, msg := range mw.messages {
		if existingMap[msg.AccountName] == nil {
			existingMap[msg.AccountName] = make(map[uint32]bool)
		}
		existingMap[msg.AccountName][msg.Message.UID] = true
	}

	// Add only new messages that don't already exist
	var addedCount int
	var duplicateCount int
	for _, newMsg := range newMessages {
		// Check if this message already exists
		if existingMap[newMsg.AccountName] != nil && existingMap[newMsg.AccountName][newMsg.Message.UID] {
			duplicateCount++
			continue // Skip duplicate
		}

		// Add the new message
		mw.messages = append(mw.messages, newMsg)
		addedCount++

		// Update the existing map to track this new message
		if existingMap[newMsg.AccountName] == nil {
			existingMap[newMsg.AccountName] = make(map[uint32]bool)
		}
		existingMap[newMsg.AccountName][newMsg.Message.UID] = true
	}

	mw.logger.Info("mergeNewMessagesIntoUnifiedInbox: Merged %d new messages (skipped %d duplicates), total now: %d",
		addedCount, duplicateCount, len(mw.messages))
}

// loadUnifiedInboxFromCache loads unified inbox messages from cache
func (mw *MainWindow) loadUnifiedInboxFromCache() ([]email.MessageIndexItem, bool) {
	if mw.cache == nil || mw.config == nil {
		return nil, false
	}

	var allMessages []email.MessageIndexItem
	cacheFound := false

	accounts := mw.config.GetAccounts()
	for _, account := range accounts {
		messages, found := mw.loadCachedMessagesDirectly(account.Name, "INBOX")
		if !found {
			continue
		}

		cacheFound = true

		// Use an already-connected client if one exists; otherwise leave IMAPClient nil
		// so selecting a cached message can connect lazily on demand.
		var imapClient *imap.ClientWrapper
		if existingClient, exists := mw.accountController.GetIMAPClientForAccount(account.Name); exists && existingClient != nil && existingClient.IsConnected() {
			imapClient = existingClient
		}

		// Create SMTP client for this account (lightweight, no connection)
		smtpClient := smtp.NewClient(email.ServerConfig{
			Host:     account.SMTP.Host,
			Port:     account.SMTP.Port,
			Username: account.SMTP.Username,
			Password: account.SMTP.Password,
			TLS:      account.SMTP.TLS,
		})

		for _, msg := range messages {
			indexItem := email.MessageIndexItem{
				Message:      msg,
				AccountName:  account.Name,
				AccountEmail: account.Email,
				FolderName:   "INBOX",
				IMAPClient:   imapClient,
				SMTPClient:   smtpClient,
				AccountConfig: &email.AccountConfig{
					Name:        account.Name,
					Email:       account.Email,
					DisplayName: account.DisplayName,
					IMAP: email.ServerConfig{
						Host:     account.IMAP.Host,
						Port:     account.IMAP.Port,
						Username: account.IMAP.Username,
						Password: account.IMAP.Password,
						TLS:      account.IMAP.TLS,
					},
					SMTP: email.ServerConfig{
						Host:     account.SMTP.Host,
						Port:     account.SMTP.Port,
						Username: account.SMTP.Username,
						Password: account.SMTP.Password,
						TLS:      account.SMTP.TLS,
					},
				},
			}
			allMessages = append(allMessages, indexItem)
		}

		mw.logger.Debug("Loaded %d cached messages from account %s INBOX for unified inbox", len(messages), account.Name)
	}

	// Don't sort here - let the UI apply user's sort preferences
	return allMessages, cacheFound
}

// cacheAccountMessages caches messages for a specific account
func (mw *MainWindow) cacheAccountMessages(accountName string, messages []email.Message) {
	cacheKey := fmt.Sprintf("account_%s:messages:INBOX", accountName)
	if data, err := json.Marshal(messages); err == nil {
		// Cache for 24 hours
		if err := mw.cache.Set(cacheKey, data, 24*time.Hour); err != nil {
			mw.logger.Warn("Failed to cache messages for account %s: %v", accountName, err)
		} else {
			mw.logger.Debug("Cached %d messages for account %s", len(messages), accountName)
		}
	}
}

// getOrCreateIMAPClient gets or creates an IMAP client for the specified account.
// The check-and-create is routed through AccountController.GetOrCreateIMAPClientWithFactory
// to ensure atomicity (no duplicate clients for the same account).
func (mw *MainWindow) getOrCreateIMAPClient(account *config.Account) (*imap.ClientWrapper, error) {
	return mw.accountController.GetOrCreateIMAPClientWithFactory(account, func(acc *config.Account) (*imap.ClientWrapper, error) {
		mw.logger.Debug("Creating new IMAP client for account: %s (%s:%d)", acc.Name, acc.IMAP.Host, acc.IMAP.Port)

		serverConfig := email.ServerConfig{
			Host:     acc.IMAP.Host,
			Port:     acc.IMAP.Port,
			Username: acc.IMAP.Username,
			Password: acc.IMAP.Password,
			TLS:      acc.IMAP.TLS,
		}

		// Get tracing configuration and create tracer if enabled
		var tracer io.Writer
		tracingConfig := mw.config.GetTracing()
		if tracingConfig.IMAP.Enabled {
			imapTracer := trace.NewIMAPTracer(true, tracingConfig.IMAP.Directory)
			tracer = imapTracer.GetAccountTracer(acc.Name)
		}

		client := imap.NewClientWrapperWithCacheAndTracer(serverConfig, mw.cache, accountCacheKey(acc.Name), tracer)

		if err := client.Start(); err != nil {
			return nil, fmt.Errorf("failed to start IMAP worker for %s: %w", acc.Name, err)
		}

		// Set up connection state callback (types are aliases, no conversion needed)
		client.SetConnectionStateCallback(func(event email.ConnectionEvent) {
			mw.handleConnectionStateChange(acc.Name, event)
		})

		if err := client.Connect(); err != nil {
			client.Stop()
			return nil, fmt.Errorf("failed to connect to IMAP server for %s: %w", acc.Name, err)
		}

		mw.logger.Debug("Successfully connected IMAP client for account: %s", acc.Name)
		return client, nil
	})
}

// handleConnectionStateChange handles IMAP connection state changes
func (mw *MainWindow) handleConnectionStateChange(accountName string, event imap.ConnectionEvent) {
	mw.logger.Debug("Connection state changed for account %s: %s", accountName, event.State.String())

	// Update status bar based on connection state
	fyne.Do(func() {
		switch event.State {
		case imap.ConnectionStateConnecting:
			mw.statusBar.SetText(fmt.Sprintf("Connecting to %s...", accountName))
		case imap.ConnectionStateConnected:
			mw.statusBar.SetText(fmt.Sprintf("Connected to %s", accountName))
		case imap.ConnectionStateReconnecting:
			if event.Attempt > 1 {
				mw.statusBar.SetText(fmt.Sprintf("Reconnecting to %s (attempt %d)...", accountName, event.Attempt))
			} else {
				mw.statusBar.SetText(fmt.Sprintf("Reconnecting to %s...", accountName))
			}
		case imap.ConnectionStateDisconnected:
			mw.statusBar.SetText(fmt.Sprintf("Disconnected from %s", accountName))
		case imap.ConnectionStateFailed:
			if event.Error != nil {
				mw.statusBar.SetText(fmt.Sprintf("Connection failed for %s: %v", accountName, event.Error))
			} else {
				mw.statusBar.SetText(fmt.Sprintf("Connection failed for %s", accountName))
			}
		}
	})

	// Log connection errors for debugging
	if event.Error != nil {
		mw.logger.Warn("Connection error for account %s: %v", accountName, event.Error)

		// Check if this is a connection error that should trigger reconnection
		if mw.isConnectionError(event.Error) {
			mw.logger.Info("Connection error detected for account %s, monitoring will handle reconnection", accountName)
		}
	}

	// Handle successful reconnection
	if event.State == imap.ConnectionStateConnected && event.Attempt > 1 {
		mw.logger.Info("Successfully reconnected to account %s after %d attempts", accountName, event.Attempt)

		// Refresh data after reconnection
		go func() {
			// Give the connection a moment to stabilize
			time.Sleep(500 * time.Millisecond)

			// Refresh folder list and current folder if we're viewing this account
			if mw.accountController.GetCurrentAccount() != nil && mw.accountController.GetCurrentAccount().Name == accountName {
				mw.logger.Debug("Refreshing data after reconnection for account %s", accountName)

				// Refresh folders in background
				if mw.imapClient != nil {
					mw.imapClient.RefreshFoldersInBackground()
				}

				// Refresh current folder if one is selected
				if currentFolder := mw.folderController.GetCurrentFolder(); currentFolder != "" {
					fyne.Do(func() {
						mw.selectFolderWithOptions(currentFolder, true)
					})
				}
			}
		}()
	}
}

// isConnectionError checks if an error indicates a connection problem
func (mw *MainWindow) isConnectionError(err error) bool {
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
		strings.Contains(errStr, "connection timed out")
}

// invalidateUnifiedInboxCache clears cached data for all accounts to force fresh loading
func (mw *MainWindow) invalidateUnifiedInboxCache() {
	mw.logger.Debug("Invalidating unified inbox cache for all accounts")

	accounts := mw.config.GetAccounts()
	for _, account := range accounts {
		// Invalidate message cache for each account's INBOX
		if client, exists := mw.accountController.GetIMAPClientForAccount(account.Name); exists && client != nil {
			client.InvalidateMessageCache("INBOX")
		}
	}
}

// getAccountForMessage returns the account and SMTP client for a specific message (used by timer)
func (mw *MainWindow) getAccountForMessage(msg *email.Message) (*config.Account, *smtp.Client, error) {
	if !mw.accountController.IsUnifiedInbox() {
		// Regular mode - use current account
		if mw.accountController.GetCurrentAccount() == nil {
			return nil, nil, fmt.Errorf("no account selected")
		}
		return mw.accountController.GetCurrentAccount(), mw.smtpClient, nil
	}

	// Unified inbox mode - find the account for the specific message
	var accountName string
	for _, indexItem := range mw.messages {
		if indexItem.Message.UID == msg.UID && indexItem.Message.Subject == msg.Subject {
			accountName = indexItem.AccountName
			break
		}
	}

	if accountName == "" {
		return nil, nil, fmt.Errorf("could not find account for message")
	}

	// Find the account by name
	accounts := mw.config.GetAccounts()
	for i, account := range accounts {
		if account.Name == accountName {
			// Create SMTP client for this account if needed
			smtpClient := smtp.NewClient(email.ServerConfig{
				Host:     account.SMTP.Host,
				Port:     account.SMTP.Port,
				Username: account.SMTP.Username,
				Password: account.SMTP.Password,
				TLS:      account.SMTP.TLS,
			})
			return &accounts[i], smtpClient, nil
		}
	}

	return nil, nil, fmt.Errorf("account %s not found", accountName)
}

// selectFolder handles folder selection and loads messages
func (mw *MainWindow) selectFolder(folder string) {
	mw.selectFolderWithOptions(folder, false)
}

func (mw *MainWindow) selectFolderWithOptions(folder string, forceRefresh bool) {
	// Check for empty folder name
	if folder == "" {
		mw.logger.Warn("Cannot select folder: folder name is empty")
		return
	}

	// Don't allow folder selection when in unified inbox mode
	if mw.accountController.IsUnifiedInbox() {
		mw.logger.Debug("Ignoring folder selection %s while in unified inbox mode", folder)
		return
	}

	if mw.accountController.GetCurrentAccount() == nil || mw.imapClient == nil {
		mw.logger.Warn("Cannot select folder %s: no account or IMAP client", folder)
		return
	}

	// Check if the folder exists in our current folder list
	folders := mw.folderController.GetFolders()
	folderExists := false
	for _, f := range folders {
		if f.Name == folder {
			folderExists = true
			break
		}
	}

	if !folderExists {
		mw.logger.Warn("Folder %s no longer exists, selecting INBOX instead", folder)
		// Try to select INBOX as a fallback
		for _, f := range folders {
			if f.Name == "INBOX" {
				mw.selectFolderWithOptions("INBOX", forceRefresh)
				return
			}
		}
		// If no INBOX, select the first available folder
		if len(folders) > 0 {
			mw.selectFolderWithOptions(folders[0].Name, forceRefresh)
			return
		}
		// No folders available
		mw.logger.Error("No folders available to select")
		mw.statusBar.SetText("No folders available")
		return
	}

	// Prevent concurrent folder loading
	if mw.folderController.IsFolderLoading() {
		mw.logger.Debug("Folder loading already in progress, ignoring request for folder: %s", folder)
		return
	}

	mw.folderController.SetFolderLoading(true)
	defer func() {
		mw.folderController.SetFolderLoading(false)
	}()

	mw.logger.Info("Selecting folder: %s", folder)

	// Stop monitoring the previous folder
	mw.stopFolderMonitoring()

	// Update current folder in controller (this will also refresh the folder list UI)
	mw.folderController.SelectFolder(folder)
	mw.statusBar.SetText(fmt.Sprintf("Loading messages from %s...", folder))

	// Cancel any active read timer when switching folders
	mw.cancelReadTimer()

	// Load messages using a cache-first pattern. Regular selection stops at the
	// cache for instant display, while refresh paths can force a server sync.
	go func() {
		// Prevent concurrent fresh fetches
		if mw.freshFetchInProgress {
			mw.logger.Debug("Fresh fetch already in progress for folder %s, skipping", folder)
			return
		}
		mw.freshFetchInProgress = true
		defer func() {
			mw.freshFetchInProgress = false
		}()

		// First, try to load from cache directly for immediate display
		mw.logger.Debug("Loading cached messages from folder %s", folder)
		cachedMessages, cachedMessagesFound := mw.loadCachedMessagesDirectly(mw.accountController.GetCurrentAccount().Name, folder)
		if cachedMessagesFound {
			mw.logger.Debug("Found %d cached messages for folder %s", len(cachedMessages), folder)
			fyne.Do(func() {
				mw.messages = mw.convertMessagesToIndexItems(cachedMessages, folder)
				mw.sortMessages()

				// Force the list to update by clearing selection and refreshing
				mw.messageList.UnselectAll()
				mw.refreshMessageList()

				mw.updateFolderMessageCount(folder, len(cachedMessages))

				if forceRefresh {
					mw.statusBar.SetText(fmt.Sprintf("Loaded %d cached messages from %s (checking for updates...)", len(cachedMessages), folder))
				} else {
					mw.statusBar.SetText(fmt.Sprintf("Loaded %d cached messages from %s", len(cachedMessages), folder))
				}

				if !mw.autoSelectionDone && len(mw.messages) > 0 {
					mw.logger.Debug("Auto-selecting first cached message")
					mw.messageList.Select(0)
					mw.selectMessage(0)
					mw.autoSelectionDone = true
				}
			})
		} else {
			mw.logger.Debug("No cached messages found for folder %s, will fetch from server", folder)
		}

		if cachedMessagesFound && !forceRefresh {
			if mw.imapClient != nil && mw.imapClient.IsConnected() {
				mw.imapClient.MarkInitialSyncComplete(folder)
				mw.startFolderMonitoring(folder)
			}
			return
		}

		// Then fetch fresh messages from server
		if mw.imapClient != nil {
			mw.logger.Debug("Fetching fresh messages from server for folder %s", folder)
			messages, err := mw.imapClient.FetchFreshMessages(folder, 0) // No limit - fetch all messages
			if err != nil {
				mw.logger.Error("Failed to fetch fresh messages from folder %s: %v", folder, err)
				fyne.Do(func() {
					// Only show error if we don't have cached messages
					if len(mw.messages) == 0 {
						mw.statusBar.SetText(fmt.Sprintf("Failed to load messages from %s: %v", folder, err))
						mw.messages = []email.MessageIndexItem{}
						mw.messageList.UnselectAll()
						mw.refreshMessageList()
					} else {
						mw.statusBar.SetText(fmt.Sprintf("Using cached messages (%d) - server unavailable", len(mw.messages)))
					}
				})
				return
			}

			// Success! Update UI with fresh messages
			mw.logger.Debug("Successfully fetched %d fresh messages from folder %s", len(messages), folder)
			fyne.Do(func() {
				oldCount := 0
				if cachedMessagesFound {
					oldCount = len(cachedMessages)
				}
				mw.messages = mw.convertMessagesToIndexItems(messages, folder)
				mw.sortMessages()

				// Force the list to update by clearing selection and refreshing
				mw.messageList.UnselectAll()
				mw.refreshMessageList()

				newCount := len(messages)
				mw.updateFolderMessageCount(folder, newCount)

				if newCount > oldCount {
					mw.statusBar.SetText(fmt.Sprintf("Updated: %d messages (%d new) from %s", newCount, newCount-oldCount, folder))
				} else if newCount == oldCount {
					mw.statusBar.SetText(fmt.Sprintf("Up to date: %d messages from %s", newCount, folder))
				} else {
					mw.statusBar.SetText(fmt.Sprintf("Loaded %d messages from %s", newCount, folder))
				}

				// Mark initial sync complete for this folder to enable notifications
				if mw.imapClient != nil {
					mw.imapClient.MarkInitialSyncComplete(folder)
				}

				// Auto-select first message if this is the first folder selection
				if !mw.autoSelectionDone && len(mw.messages) > 0 {
					mw.logger.Debug("Auto-selecting first message")
					mw.messageList.Select(0)
					mw.selectMessage(0)
					mw.autoSelectionDone = true
				}

				// Start monitoring the folder for real-time updates
				mw.startFolderMonitoring(folder)
			})
		} else {
			mw.logger.Error("IMAP client not available")
			fyne.Do(func() {
				if len(mw.messages) == 0 {
					mw.statusBar.SetText("Could not connect to server and no cached messages available")
				}
			})
		}
	}()
}

// getFolderMessageCount returns the message count for a folder from the folder list
// Delegates to FolderController
func (mw *MainWindow) getFolderMessageCount(folderName string) int {
	return mw.folderController.GetFolderMessageCount(folderName)
}

// updateFolderMessageCount updates the message count for a folder in the folder list
// Delegates to FolderController
func (mw *MainWindow) updateFolderMessageCount(folderName string, newCount int) {
	mw.folderController.UpdateFolderMessageCount(folderName, newCount)
}

// loadSampleMessages creates sample messages for demonstration
func (mw *MainWindow) loadSampleMessages() {
	now := time.Now()
	sampleMessages := []email.Message{
		{
			ID:      "msg1",
			Subject: "Welcome to the new email client",
			From:    []email.Address{{Name: "System", Email: "system@example.com"}},
			To:      []email.Address{{Name: mw.accountController.GetCurrentAccount().Name, Email: mw.accountController.GetCurrentAccount().IMAP.Username}},
			Date:    now.Add(-2 * time.Hour),
			Body:    email.MessageBody{Text: "Welcome to your new email client! This is a sample message."},
			Flags:   []string{"\\Seen"},
		},
		{
			ID:      "msg2",
			Subject: "Re: Welcome to the new email client",
			From:    []email.Address{{Name: "User", Email: "user@example.com"}},
			To:      []email.Address{{Name: mw.accountController.GetCurrentAccount().Name, Email: mw.accountController.GetCurrentAccount().IMAP.Username}},
			Date:    now.Add(-1 * time.Hour),
			Body:    email.MessageBody{Text: "Thank you for the welcome message!"},
			Headers: map[string]string{"In-Reply-To": "msg1"},
		},
		{
			ID:      "msg3",
			Subject: "Important: Phase 2 Implementation Complete",
			From:    []email.Address{{Name: "Developer", Email: "dev@example.com"}},
			To:      []email.Address{{Name: mw.accountController.GetCurrentAccount().Name, Email: mw.accountController.GetCurrentAccount().IMAP.Username}},
			Date:    now,
			Body:    email.MessageBody{Text: "Phase 2 implementation is now complete with threading, SMTP enhancements, and attachment handling!"},
			Attachments: []email.Attachment{
				{
					Filename:    "implementation_notes.txt",
					ContentType: "text/plain",
					Size:        1024,
					Data:        []byte("Implementation details..."),
				},
			},
		},
	}

	mw.messages = mw.convertMessagesToIndexItems(sampleMessages, "INBOX")
}

// validateMessageArrayConsistency performs validation checks on the message array
func (mw *MainWindow) validateMessageArrayConsistency() bool {
	if len(mw.messages) == 0 {
		return true // Empty array is consistent
	}

	// Check for duplicate UIDs within the same account
	uidMap := make(map[string]map[uint32]int) // account -> uid -> index
	for i, msg := range mw.messages {
		if uidMap[msg.AccountName] == nil {
			uidMap[msg.AccountName] = make(map[uint32]int)
		}
		if existingIndex, exists := uidMap[msg.AccountName][msg.Message.UID]; exists {
			mw.logger.Error("Duplicate UID %d found in account %s at indices %d and %d", msg.Message.UID, msg.AccountName, existingIndex, i)
			return false
		}
		uidMap[msg.AccountName][msg.Message.UID] = i
	}

	mw.logger.Debug("Message array consistency check passed: %d messages", len(mw.messages))
	return true
}

// selectMessage handles message selection and displays the message content
func (mw *MainWindow) selectMessage(id widget.ListItemID) {
	mw.logger.Debug("selectMessage called with ID=%d, messageCount=%d", id, len(mw.messages))

	// Validate the ID is within bounds
	if id < 0 || id >= len(mw.messages) {
		mw.logger.Debug("selectMessage: Invalid ID %d (messageCount=%d) - ignoring selection", id, len(mw.messages))
		mw.logger.Warn("Attempted to select message with invalid ID %d (messageCount=%d)", id, len(mw.messages))

		// Perform consistency check when we detect invalid access
		if !mw.validateMessageArrayConsistency() {
			mw.logger.Error("Message array consistency check failed during invalid selection")
		}
		return
	}

	// Store previous selection to refresh its highlighting
	var previouslySelectedIndex = -1
	if mw.selectedMessage != nil {
		// Find the index of the previously selected message
		for i, m := range mw.messages {
			if m.Message.UID == mw.selectedMessage.Message.UID && m.AccountName == mw.selectedMessage.AccountName {
				previouslySelectedIndex = i
				break
			}
		}
	}

	msg := &mw.messages[id]
	mw.messagesMu.Lock()
	mw.selectedMessage = msg
	mw.messagesMu.Unlock()
	mw.logger.Debug("selectMessage: Selected message at index %d: %s (UID=%d, Account=%s)", id, msg.Message.Subject, msg.Message.UID, msg.AccountName)

	// Refresh the message list to update selection highlighting (if available)
	// Force immediate UI update by ensuring this runs on the UI thread
	if mw.messageList != nil {
		selectionRefreshStarted := time.Now()

		// Refresh the previously selected item to remove its highlight
		if previouslySelectedIndex >= 0 && previouslySelectedIndex < len(mw.messages) {
			mw.messageList.RefreshItem(previouslySelectedIndex)
		}

		// Refresh the newly selected item to show its highlight
		if previouslySelectedIndex != id {
			mw.messageList.RefreshItem(id)
		}

		mw.logger.Debug("selectMessage: refreshed selection highlights in %v", time.Since(selectionRefreshStarted))
	}

	// Cancel any existing read timer when switching messages
	mw.cancelReadTimer()

	// Show loading message while fetching full content
	loadingContent := fmt.Sprintf("# %s\n\n> **From:** %s\n> **Date:** %s\n\n---\n\n*Loading message content...*",
		msg.Message.Subject, mw.formatAddresses(msg.Message.From), mw.getDisplayDate(&msg.Message).Format("January 2, 2006 at 3:04 PM"))
	mw.updateMessageViewer(loadingContent)

	// Start the 5-second read timer for unread messages
	mw.startReadTimer(&msg.Message)

	// Fetch full message content in background (with lazy IMAP client creation)
	mw.logger.Debug("selectMessage: Starting goroutine to fetch message content for UID %d", msg.Message.UID)
	go mw.fetchAndDisplayMessage(msg)
}

// fetchAndDisplayMessage fetches the full message content and displays it
func (mw *MainWindow) fetchAndDisplayMessage(indexItem *email.MessageIndexItem) {
	mw.logger.Debug("fetchAndDisplayMessage: Starting fetch for message UID %d from account %s", indexItem.Message.UID, indexItem.AccountName)

	// Validate the MessageIndexItem
	if indexItem == nil {
		mw.logger.Error("fetchAndDisplayMessage called with nil MessageIndexItem")
		return
	}

	mw.logger.Debug("fetchAndDisplayMessage: MessageIndexItem validated, IMAPClient is nil: %t", indexItem.IMAPClient == nil)

	// Check if IMAPClient is available - if not, try to create it lazily
	if indexItem.IMAPClient == nil {
		// Skip lazy loading if config is not available (e.g., in tests)
		if mw.config == nil {
			mw.logger.Debug("Skipping lazy IMAP client creation - no config available (likely in test mode)")
			return
		}

		mw.logger.Debug("MessageIndexItem has nil IMAPClient for message UID %d from account %s, creating lazily", indexItem.Message.UID, indexItem.AccountName)

		// Find the account config for this message
		accounts := mw.config.GetAccounts()
		var accountConfig *config.Account
		for _, acc := range accounts {
			if acc.Name == indexItem.AccountName {
				accountConfig = &acc
				break
			}
		}

		if accountConfig == nil {
			mw.logger.Error("Could not find account config for %s", indexItem.AccountName)
			fyne.Do(func() {
				errorContent := fmt.Sprintf("# %s\n\n> **From:** %s\n> **Date:** %s\n\n---\n\n**Error:** Account configuration not found for %s\n\n*Please check your account settings.*",
					indexItem.Message.Subject, mw.formatAddresses(indexItem.Message.From), mw.getDisplayDate(&indexItem.Message).Format("January 2, 2006 at 3:04 PM"), indexItem.AccountName)
				mw.updateMessageViewer(errorContent)
			})
			return
		}

		// Create IMAP client lazily
		imapClient, err := mw.getOrCreateIMAPClient(accountConfig)
		if err != nil {
			mw.logger.Error("Failed to create IMAP client for account %s: %v", indexItem.AccountName, err)
			fyne.Do(func() {
				errorContent := fmt.Sprintf("# %s\n\n> **From:** %s\n> **Date:** %s\n\n---\n\n**Error:** Failed to connect to IMAP server for account %s: %v\n\n*Please check your connection and account settings.*",
					indexItem.Message.Subject, mw.formatAddresses(indexItem.Message.From), mw.getDisplayDate(&indexItem.Message).Format("January 2, 2006 at 3:04 PM"), indexItem.AccountName, err)
				mw.updateMessageViewer(errorContent)
			})
			return
		}

		// Update the MessageIndexItem with the new client
		indexItem.IMAPClient = imapClient
		mw.logger.Debug("Successfully created IMAP client for account %s", indexItem.AccountName)
	}

	// Safely cast to *imap.ClientWrapper
	imapClient, ok := indexItem.IMAPClient.(*imap.ClientWrapper)
	if !ok || imapClient == nil {
		mw.logger.Error("MessageIndexItem IMAPClient is not of type *imap.ClientWrapper or is nil for message UID %d from account %s (type: %T)", indexItem.Message.UID, indexItem.AccountName, indexItem.IMAPClient)
		fyne.Do(func() {
			errorContent := fmt.Sprintf("# %s\n\n> **From:** %s\n> **Date:** %s\n\n---\n\n**Error:** Message content unavailable\n\nThis cached message needs to be refreshed from the server to view its content.\n\n**Solutions:**\n• Press F5 to refresh all\n• Wait for the background sync to complete\n\n*Account: %s*",
				indexItem.Message.Subject, mw.formatAddresses(indexItem.Message.From), mw.getDisplayDate(&indexItem.Message).Format("January 2, 2006 at 3:04 PM"), indexItem.AccountName)
			mw.updateMessageViewer(errorContent)
		})
		return
	}

	// Check if IMAP client is connected, if not wait a moment for connection
	if !imapClient.IsConnected() {
		mw.logger.Debug("IMAP client not connected for account %s, waiting for connection...", indexItem.AccountName)
		// Wait up to 3 seconds for connection
		for i := 0; i < 6; i++ {
			time.Sleep(500 * time.Millisecond)
			if imapClient.IsConnected() {
				mw.logger.Debug("IMAP client connected for account %s after %d attempts", indexItem.AccountName, i+1)
				break
			}
		}

		// If still not connected, show helpful error
		if !imapClient.IsConnected() {
			fyne.Do(func() {
				errorContent := fmt.Sprintf("# %s\n\n> **From:** %s\n> **Date:** %s\n\n---\n\n**Error:** Server connection in progress\n\nThe email server connection is still being established. Please wait a moment and try again.\n\n**Solutions:**\n• Wait a few seconds and click the message again\n• Press F5 to refresh all\n• Check your internet connection\n\n*Account: %s*",
					indexItem.Message.Subject, mw.formatAddresses(indexItem.Message.From), mw.getDisplayDate(&indexItem.Message).Format("January 2, 2006 at 3:04 PM"), indexItem.AccountName)
				mw.updateMessageViewer(errorContent)
			})
			return
		}
	}

	folder := indexItem.FolderName
	msg := &indexItem.Message

	// Show loading message while fetching
	fyne.Do(func() {
		loadingContent := fmt.Sprintf("# %s\n\n> **From:** %s\n> **Date:** %s\n\n---\n\n**Loading message content...**",
			msg.Subject, mw.formatAddresses(msg.From), mw.getDisplayDate(msg).Format("January 2, 2006 at 3:04 PM"))
		mw.updateMessageViewer(loadingContent)
	})

	// Try to fetch full message content using UID with retry logic for connection issues
	maxRetries := 3
	retryDelay := 500 * time.Millisecond

	for attempt := 1; attempt <= maxRetries; attempt++ {
		fullMsg, err := imapClient.FetchMessage(folder, msg.UID)
		if err == nil {
			// Check if the message has body content - if not, it's likely cached envelope data only
			hasBodyContent := fullMsg.Body.Text != "" || fullMsg.Body.HTML != ""
			if !hasBodyContent {
				mw.logger.Debug("Cached message UID %d has no body content, fetching fresh from server", fullMsg.UID)
				// Force fetch with full headers to get body content
				freshMsg, freshErr := imapClient.FetchMessageWithFullHeaders(folder, msg.UID)
				if freshErr == nil && (freshMsg.Body.Text != "" || freshMsg.Body.HTML != "") {
					fullMsg = freshMsg
					mw.logger.Debug("Successfully fetched fresh message with body content")
				} else {
					mw.logger.Warn("Failed to fetch fresh message content: %v", freshErr)
				}
			}

			// Success! Update UI with the message
			mw.logger.Debug("Successfully fetched message UID %d", fullMsg.UID)
			mw.logger.Debug("Fetched message has Text: %t (len=%d), HTML: %t (len=%d)",
				fullMsg.Body.Text != "", len(fullMsg.Body.Text),
				fullMsg.Body.HTML != "", len(fullMsg.Body.HTML))

			fyne.Do(func() {
				// Update the message in our list with the full content
				for i := range mw.messages {
					if mw.messages[i].Message.UID == msg.UID {
						mw.messages[i].Message.Body = fullMsg.Body
						mw.messages[i].Message.Attachments = fullMsg.Attachments
						mw.logger.Debug("Updated message in array at index %d", i)
						break
					}
				}

				// Display the full message only if it's still the selected message
				if mw.selectedMessage != nil && mw.selectedMessage.Message.UID == fullMsg.UID {
					mw.displayMessage(fullMsg)
				} else {
					mw.logger.Debug("Message %d is no longer selected, not displaying", fullMsg.UID)
				}
			})
			return
		}

		// Check if it's a connection error
		if strings.Contains(err.Error(), "not connected") {
			mw.logger.Debug("Message fetch attempt %d failed due to connection issue, retrying in %v", attempt, retryDelay)

			// Try to establish connection if this is the first attempt
			if attempt == 1 {
				mw.logger.Debug("Attempting to establish IMAP connection for message fetch")
				if connectErr := imapClient.Connect(); connectErr != nil {
					mw.logger.Error("Failed to establish IMAP connection: %v", connectErr)
				} else {
					// Connection established, retry immediately
					continue
				}
			}

			// Wait before retry (except on last attempt)
			if attempt < maxRetries {
				time.Sleep(retryDelay)
				retryDelay *= 2 // Exponential backoff
				continue
			}
		}

		// Non-connection error or final attempt failed
		mw.logger.Error("Failed to fetch message UID %d after %d attempts: %v", msg.UID, attempt, err)
		fyne.Do(func() {
			errorContent := fmt.Sprintf("# %s\n\n> **From:** %s\n> **Date:** %s\n\n---\n\n**Error loading message content:** %v\n\n*Try clicking on the message again once the connection is established.*",
				msg.Subject, mw.formatAddresses(msg.From), mw.getDisplayDate(msg).Format("January 2, 2006 at 3:04 PM"), err)
			mw.updateMessageViewer(errorContent)
		})
		return
	}
}

// displayMessage shows a single message in the viewer
func (mw *MainWindow) displayMessage(msg *email.Message) {
	// Delegate to MessageViewController
	mw.messageViewController.DisplayMessage(msg, mw.formatAddresses, mw.getDisplayDateString)

	// Create attachment UI components
	mw.updateAttachmentSection(msg)
}

// Multiple selection helper methods

// isMessageSelected returns true if the message at the given index is selected
func (mw *MainWindow) isMessageSelected(index int) bool {
	return mw.selectionManager.IsMessageSelected(index)
}

// getSelectedMessageIndices returns a slice of all selected message indices
func (mw *MainWindow) getSelectedMessageIndices() []int {
	return mw.selectionManager.GetSelectedMessageIndices()
}

// getSelectedMessages returns a slice of all selected MessageIndexItems
func (mw *MainWindow) getSelectedMessages() []*email.MessageIndexItem {
	return mw.selectionManager.GetSelectedMessages(mw.messages)
}

// selectMessageMultiple handles multiple message selection with Ctrl/Shift modifiers
func (mw *MainWindow) selectMessageMultiple(index int, ctrlPressed, shiftPressed bool) {
	mw.selectionManager.SelectMessageMultiple(index, ctrlPressed, shiftPressed, mw.messages)

	// Update selectedMessage for backward compatibility
	mw.selectedMessage = mw.selectionManager.GetSelectedMessage()

	// If no messages selected, show placeholder
	if mw.selectedMessage == nil {
		mw.updateMessageViewer("Select a message to view")
	}
}

// getSelectedMessageIndicesUnsafe returns selected indices without locking (for internal use when already locked)
// This method is deprecated and kept for backward compatibility
func (mw *MainWindow) getSelectedMessageIndicesUnsafe() []int {
	return mw.selectionManager.GetSelectedMessageIndices()
}

// clearSelection clears all message selections
func (mw *MainWindow) clearSelection() {
	mw.selectionManager.ClearSelection()
	mw.selectedMessage = nil
	mw.updateMessageViewer("Select a message to view")
}

// Multiple message operations

// moveToTrashMultiple moves all selected messages to trash
func (mw *MainWindow) moveToTrashMultiple() {
	selectedMessages := mw.getSelectedMessages()
	if len(selectedMessages) == 0 {
		dialog.ShowInformation("No Messages", "Please select messages to move to trash", mw.window)
		return
	}

	// Show confirmation dialog
	dialog.ShowConfirm("Move Messages to Trash",
		fmt.Sprintf("Are you sure you want to move %d messages to trash?", len(selectedMessages)),
		func(confirmed bool) {
			if confirmed {
				mw.performMoveToTrashMultiple(selectedMessages)
			}
		}, mw.window)
}

// performMoveToTrashMultiple performs the actual move to trash operation for multiple messages
func (mw *MainWindow) performMoveToTrashMultiple(messages []*email.MessageIndexItem) {
	mw.statusBar.SetText(fmt.Sprintf("Moving %d messages to trash...", len(messages)))

	successCount := 0
	errorCount := 0

	for _, msg := range messages {
		err := mw.performSingleMoveToTrash(msg)
		if err != nil {
			mw.logger.Error("Failed to move message to trash: %v", err)
			errorCount++
		} else {
			successCount++
		}
	}

	// Update status
	if errorCount == 0 {
		mw.statusBar.SetText(fmt.Sprintf("Moved %d messages to trash", successCount))
	} else {
		mw.statusBar.SetText(fmt.Sprintf("Moved %d messages to trash, %d failed", successCount, errorCount))
	}

	// Clear selection and refresh
	mw.clearSelection()
	mw.refreshMessageList()
}

// performSingleMoveToTrash moves a single message to trash (extracted from existing logic)
func (mw *MainWindow) performSingleMoveToTrash(msg *email.MessageIndexItem) error {
	// Get the correct account for the message
	var currentAccount *config.Account

	if mw.accountController.IsUnifiedInbox() {
		// Find the account for this message
		accounts := mw.config.GetAccounts()
		for _, acc := range accounts {
			if acc.Name == msg.AccountName {
				currentAccount = &acc
				break
			}
		}
		if currentAccount == nil {
			return fmt.Errorf("account not found: %s", msg.AccountName)
		}
	} else {
		// Regular mode
		if mw.accountController.GetCurrentAccount() == nil {
			return fmt.Errorf("no account available")
		}
		currentAccount = mw.accountController.GetCurrentAccount()
	}

	// Determine trash folder (use configured or default)
	trashFolder := currentAccount.TrashFolder
	if trashFolder == "" {
		trashFolder = "Trash" // Default fallback
	}

	// Use the MessageIndexItem's MoveTo method to move to trash
	return msg.MoveTo(trashFolder)
}

// showMoveToFolderDialogMultiple shows folder selection dialog for multiple messages
func (mw *MainWindow) showMoveToFolderDialogMultiple() {
	selectedMessages := mw.getSelectedMessages()
	if len(selectedMessages) == 0 {
		dialog.ShowInformation("No Messages", "Please select messages to move", mw.window)
		return
	}

	// For unified inbox, we need to handle messages from different accounts
	// For now, we'll only allow moving messages from the same account
	if mw.accountController.IsUnifiedInbox() {
		// Check if all selected messages are from the same account
		accountName := selectedMessages[0].AccountName
		for _, msg := range selectedMessages[1:] {
			if msg.AccountName != accountName {
				dialog.ShowInformation("Mixed Accounts",
					"Cannot move messages from different accounts at once. Please select messages from a single account.",
					mw.window)
				return
			}
		}
	}

	// Get folders for the account
	var folders []email.Folder
	if mw.accountController.IsUnifiedInbox() {
		// Get folders for the specific account
		accountName := selectedMessages[0].AccountName
		client, exists := mw.accountController.GetIMAPClientForAccount(accountName)
		if exists && client != nil {
			var err error
			folders, err = client.ListFolders()
			if err != nil {
				dialog.ShowError(fmt.Errorf("failed to get folders: %v", err), mw.window)
				return
			}
		}
	} else {
		folders = mw.folderController.GetFolders()
	}

	if len(folders) == 0 {
		dialog.ShowInformation("No Folders", "No folders available", mw.window)
		return
	}

	// Create folder selection dialog
	go func() {
		folderNames := make([]string, len(folders))
		for i, folder := range folders {
			folderNames[i] = folder.Name
		}

		folderSelect := widget.NewSelect(folderNames, nil)
		folderSelect.PlaceHolder = "Select a folder..."

		content := container.NewVBox(
			widget.NewLabel(fmt.Sprintf("Move %d messages to folder:", len(selectedMessages))),
			folderSelect,
		)

		dialog.ShowCustomConfirm("Move Messages", "Move", "Cancel", content, func(confirmed bool) {
			if confirmed && folderSelect.Selected != "" {
				mw.logger.Debug("Moving %d messages to folder: %s", len(selectedMessages), folderSelect.Selected)
				mw.moveToFolderMultiple(selectedMessages, folderSelect.Selected)
			}
		}, mw.window)
	}()
}

// moveToFolderMultiple moves multiple messages to the specified folder
func (mw *MainWindow) moveToFolderMultiple(messages []*email.MessageIndexItem, folderName string) {
	mw.statusBar.SetText(fmt.Sprintf("Moving %d messages to %s...", len(messages), folderName))

	successCount := 0
	errorCount := 0

	for _, msg := range messages {
		err := mw.performSingleMoveToFolder(msg, folderName)
		if err != nil {
			mw.logger.Error("Failed to move message to folder %s: %v", folderName, err)
			errorCount++
		} else {
			successCount++
		}
	}

	// Update status
	if errorCount == 0 {
		mw.statusBar.SetText(fmt.Sprintf("Moved %d messages to %s", successCount, folderName))
	} else {
		mw.statusBar.SetText(fmt.Sprintf("Moved %d messages to %s, %d failed", successCount, folderName, errorCount))
	}

	// Clear selection and refresh
	mw.clearSelection()
	mw.refreshMessageList()
}

// performSingleMoveToFolder moves a single message to the specified folder
func (mw *MainWindow) performSingleMoveToFolder(msg *email.MessageIndexItem, folderName string) error {
	// Use the MessageIndexItem's MoveTo method
	return msg.MoveTo(folderName)
}

// refreshMessageListView refreshes both the visible wrapper widget and the inner
// Fyne list so changes appear immediately without requiring user interaction.
func (mw *MainWindow) refreshMessageListView() {
	if mw.messageList != nil {
		mw.messageList.Refresh()
		canvas.Refresh(mw.messageList)
	}
	if mw.messageListWithRightClick != nil {
		mw.messageListWithRightClick.Refresh()
		canvas.Refresh(mw.messageListWithRightClick)
	}
}

// refreshMessageList refreshes the message list display.
func (mw *MainWindow) refreshMessageList() {
	mw.refreshMessageListView()
}

// deleteMessagesMultiple permanently deletes all selected messages
func (mw *MainWindow) deleteMessagesMultiple() {
	selectedMessages := mw.getSelectedMessages()
	if len(selectedMessages) == 0 {
		dialog.ShowInformation("No Messages", "Please select messages to delete", mw.window)
		return
	}

	// Show confirmation dialog
	dialog.ShowConfirm("Delete Messages",
		fmt.Sprintf("Are you sure you want to permanently delete %d messages?\n\nThis action cannot be undone.", len(selectedMessages)),
		func(confirmed bool) {
			if confirmed {
				mw.performDeleteMultiple(selectedMessages)
			}
		}, mw.window)
}

// performDeleteMultiple performs the actual delete operation for multiple messages
func (mw *MainWindow) performDeleteMultiple(messages []*email.MessageIndexItem) {
	mw.statusBar.SetText(fmt.Sprintf("Deleting %d messages...", len(messages)))

	successCount := 0
	errorCount := 0

	for _, msg := range messages {
		err := msg.Delete()
		if err != nil {
			mw.logger.Error("Failed to delete message: %v", err)
			errorCount++
		} else {
			successCount++
		}
	}

	// Update status
	if errorCount == 0 {
		mw.statusBar.SetText(fmt.Sprintf("Deleted %d messages", successCount))
	} else {
		mw.statusBar.SetText(fmt.Sprintf("Deleted %d messages, %d failed", successCount, errorCount))
	}

	// Clear selection and refresh
	mw.clearSelection()
	mw.refreshMessageList()
}

// selectAllMessages selects all messages in the current list
func (mw *MainWindow) selectAllMessages() {
	mw.selectionManager.SelectAllMessages(mw.messages)
	mw.selectedMessage = mw.selectionManager.GetSelectedMessage()
}

// updateMessageViewer updates the message viewer with content
func (mw *MainWindow) updateMessageViewer(markdownContent string) {
	// Delegate to MessageViewController
	mw.messageViewController.UpdateMessageViewer(markdownContent)
}

// updateAttachmentSection creates UI components for message attachments
func (mw *MainWindow) updateAttachmentSection(msg *email.Message) {
	// Delegate to MessageViewController with wrapper functions
	cacheFunc := func(attachment email.Attachment) string {
		return mw.cacheAttachmentIfNeeded(msg, attachment)
	}
	mw.messageViewController.UpdateAttachmentSection(msg, cacheFunc, mw.createAttachmentWidget)
}

// createAttachmentWidget creates an interactive UI component for a single attachment
func (mw *MainWindow) createAttachmentWidget(attachment email.Attachment, attachmentID string, index int) fyne.CanvasObject {
	// Create attachment info section
	icon := mw.getAttachmentIcon(attachment.ContentType)
	nameLabel := widget.NewRichTextFromMarkdown(fmt.Sprintf("### %s %s", icon, attachment.Filename))
	infoLabel := widget.NewLabel(fmt.Sprintf("%s • %s", attachment.ContentType, formatFileSize(attachment.Size)))

	// Create action buttons
	openButton := widget.NewButton("Open", func() {
		mw.openAttachment(attachment)
	})

	saveButton := widget.NewButton("Save", func() {
		mw.saveAttachment(attachment)
	})

	var previewWidget fyne.CanvasObject

	// Create preview based on content type
	if strings.HasPrefix(attachment.ContentType, "image/") {
		// For images, create an image widget and view button
		previewWidget = mw.createImagePreview(attachment, attachmentID)

		viewButton := widget.NewButton("View Full Size", func() {
			mw.showImageFullSize(attachment, attachmentID)
		})

		buttonContainer := container.NewHBox(openButton, saveButton, viewButton)

		return container.NewVBox(
			nameLabel,
			infoLabel,
			previewWidget,
			buttonContainer,
			widget.NewSeparator(),
		)
	} else if strings.HasPrefix(attachment.ContentType, "text/") {
		// For text files, show preview content
		previewWidget = mw.createTextPreview(attachment, attachmentID)

		buttonContainer := container.NewHBox(openButton, saveButton)

		return container.NewVBox(
			nameLabel,
			infoLabel,
			previewWidget,
			buttonContainer,
			widget.NewSeparator(),
		)
	} else {
		// For other files, just show info and buttons
		buttonContainer := container.NewHBox(openButton, saveButton)

		return container.NewVBox(
			nameLabel,
			infoLabel,
			widget.NewLabel("Preview not available for this file type"),
			buttonContainer,
			widget.NewSeparator(),
		)
	}
}

// createImagePreview creates a thumbnail preview for image attachments
func (mw *MainWindow) createImagePreview(attachment email.Attachment, attachmentID string) fyne.CanvasObject {
	if mw.attachmentManager == nil || attachmentID == "" {
		return widget.NewLabel("Image preview not available")
	}

	// Get image data from attachment manager
	previewData, err := mw.attachmentManager.GetAttachmentPreview(attachmentID, 0) // Get full image data
	if err != nil {
		return widget.NewLabel("Failed to load image preview")
	}

	// Clean base64 data by removing line breaks and whitespace
	cleanedData := strings.ReplaceAll(string(previewData), "\r\n", "")
	cleanedData = strings.ReplaceAll(cleanedData, "\n", "")
	cleanedData = strings.ReplaceAll(cleanedData, "\r", "")
	cleanedData = strings.ReplaceAll(cleanedData, " ", "")

	// Try to decode base64 data
	var imageData []byte
	if decoded, err := base64.StdEncoding.DecodeString(cleanedData); err == nil && len(decoded) > 0 {
		// Successfully decoded base64, use decoded data
		imageData = decoded
	} else {
		// Not base64 encoded, use raw data
		imageData = previewData
	}

	// Create image resource from decoded data
	resource := fyne.NewStaticResource(attachment.Filename, imageData)

	if resource == nil {
		return widget.NewLabel("Failed to create image resource")
	}

	maxWidth := float32(600)
	maxHeight := float32(400)

	imageWidget := canvas.NewImageFromResource(resource)
	imageWidget.FillMode = canvas.ImageFillCover
	imageWidget.CornerRadius = theme.Size(theme.SizeNameSelectionRadius) * 2

	previewSize := fyne.NewSize(maxWidth, maxHeight)
	imageWidget.SetMinSize(previewSize)
	imageWidget.Resize(previewSize)

	background := canvas.NewRectangle(theme.Color(theme.ColorNameInputBackground))
	background.CornerRadius = imageWidget.CornerRadius
	background.SetMinSize(previewSize)
	background.Resize(previewSize)

	card := container.NewStack(background, imageWidget)
	imageContainer := container.NewCenter(card)

	// Create a button to view full size
	viewFullSizeBtn := widget.NewButton("View Full Size", func() {
		mw.showImageFullSize(attachment, attachmentID)
	})

	// Create the preview container with proper spacing
	previewContainer := container.NewVBox(
		widget.NewLabel("Image Preview:"),
		imageContainer,
		container.NewHBox(
			viewFullSizeBtn,
			widget.NewButton("Save Image", func() {
				mw.saveAttachment(attachment)
			}),
		),
	)

	return previewContainer
}

// createTextPreview creates a text preview for text attachments
func (mw *MainWindow) createTextPreview(attachment email.Attachment, attachmentID string) fyne.CanvasObject {
	if mw.attachmentManager == nil || attachmentID == "" {
		return widget.NewLabel("Text preview not available")
	}

	// Get text data from attachment manager
	previewData, err := mw.attachmentManager.GetAttachmentPreview(attachmentID, 500) // 500 chars max
	if err != nil {
		return widget.NewLabel("Failed to load text preview")
	}

	preview := string(previewData)
	if len(preview) > 500 {
		preview = preview[:500] + "..."
	}

	// Create text widget without scroll to avoid mouse wheel conflicts
	textWidget := widget.NewRichTextFromMarkdown(fmt.Sprintf("**Preview:**\n```\n%s\n```", preview))
	textWidget.Wrapping = fyne.TextWrapWord

	// Put it in a simple container with limited height - let the main viewer handle scrolling
	textContainer := container.NewBorder(nil, nil, nil, nil, textWidget)

	return textContainer
}

// showImageFullSize displays an image attachment in full size
func (mw *MainWindow) showImageFullSize(attachment email.Attachment, attachmentID string) {
	if mw.attachmentManager == nil || attachmentID == "" {
		dialog.ShowError(fmt.Errorf("image preview not available"), mw.window)
		return
	}

	// Get full image data
	rawImageData, err := mw.attachmentManager.GetAttachmentPreview(attachmentID, 0)
	if err != nil {
		dialog.ShowError(fmt.Errorf("failed to load image: %w", err), mw.window)
		return
	}

	// Clean base64 data by removing line breaks and whitespace
	cleanedData := strings.ReplaceAll(string(rawImageData), "\r\n", "")
	cleanedData = strings.ReplaceAll(cleanedData, "\n", "")
	cleanedData = strings.ReplaceAll(cleanedData, "\r", "")
	cleanedData = strings.ReplaceAll(cleanedData, " ", "")

	// Decode base64 data
	var imageData []byte
	if decoded, err := base64.StdEncoding.DecodeString(cleanedData); err == nil && len(decoded) > 0 {
		// Successfully decoded base64, use decoded data
		imageData = decoded
	} else {
		// Not base64 encoded, use raw data
		imageData = rawImageData
	}

	// Create image resource
	resource := fyne.NewStaticResource(attachment.Filename, imageData)

	// Create full-size image widget
	imageWidget := canvas.NewImageFromResource(resource)
	imageWidget.FillMode = canvas.ImageFillContain

	// Create scrollable container for large images
	scrollContainer := container.NewScroll(imageWidget)

	// Create dialog to show the image
	imageDialog := dialog.NewCustom(
		fmt.Sprintf("Image: %s", attachment.Filename),
		"Close",
		scrollContainer,
		mw.window,
	)

	// Set dialog size based on screen size
	imageDialog.Resize(fyne.NewSize(800, 600))
	imageDialog.Show()
}

// formatTextForMarkdown formats plain text for proper markdown display
func (mw *MainWindow) formatTextForMarkdown(text string) string {
	// Replace single line breaks with double line breaks for markdown
	// This ensures paragraphs are properly separated
	lines := strings.Split(text, "\n")
	var formattedLines []string

	for i, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		// If this is an empty line, keep it as a paragraph separator
		if trimmedLine == "" {
			formattedLines = append(formattedLines, "")
			continue
		}

		// Add the line
		formattedLines = append(formattedLines, trimmedLine)

		// Add an extra empty line after non-empty lines to create proper paragraph breaks
		// unless the next line is already empty or we're at the end
		if i < len(lines)-1 {
			nextLine := strings.TrimSpace(lines[i+1])
			if nextLine != "" {
				formattedLines = append(formattedLines, "")
			}
		}
	}

	return strings.Join(formattedLines, "\n")
}

// htmlToPlainText converts HTML content to plain text with aggressive CSS filtering
func (mw *MainWindow) htmlToPlainText(html string) string {
	// Check if this looks like mostly CSS content by counting CSS-like patterns
	cssPatterns := []string{
		"border:", "margin:", "padding:", "font-size:", "color:", "background:",
		"display:", "width:", "height:", "text-decoration:", "line-height:",
		"font-family:", "font-weight:", "text-align:", "border-collapse:",
		"webkit-text-size-adjust:", "ms-text-size-adjust:", "!important",
	}

	cssCount := 0
	for _, pattern := range cssPatterns {
		if strings.Contains(html, pattern) {
			cssCount++
		}
	}

	// If we detect lots of CSS patterns, try to extract meaningful content
	if cssCount > 5 { // Lower threshold to catch more CSS-heavy content
		// Split into lines and filter out CSS-heavy lines
		lines := strings.Split(html, "\n")
		var contentLines []string

		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			// Skip lines that are clearly CSS or styling
			isCSSLine := false

			// Check for CSS patterns
			for _, pattern := range cssPatterns {
				if strings.Contains(line, pattern) {
					isCSSLine = true
					break
				}
			}

			// Additional CSS/styling patterns to skip
			if !isCSSLine {
				// Skip lines with CSS-like syntax
				if strings.Contains(line, "{") || strings.Contains(line, "}") ||
					strings.HasSuffix(line, ";") && len(line) < 200 ||
					strings.Contains(line, "px") && strings.Contains(line, ":") ||
					strings.Contains(line, "rgb(") || strings.Contains(line, "rgba(") ||
					strings.Contains(line, "#") && len(line) < 50 ||
					strings.Contains(line, "class=") || strings.Contains(line, "style=") ||
					strings.Contains(line, "font-") || strings.Contains(line, "text-") ||
					strings.Contains(line, "border-") || strings.Contains(line, "margin-") ||
					strings.Contains(line, "padding-") || strings.Contains(line, "background-") ||
					strings.Contains(line, "webkit-") || strings.Contains(line, "moz-") ||
					strings.Contains(line, "ms-") || strings.Contains(line, "-o-") {
					isCSSLine = true
				}
			}

			// Keep lines that look like actual content
			if !isCSSLine && len(line) > 10 {
				// Remove any remaining HTML tags from this line
				cleanLine := regexp.MustCompile(`<[^>]*>`).ReplaceAllString(line, "")
				cleanLine = strings.TrimSpace(cleanLine)

				// Additional filtering for content quality
				if cleanLine != "" && len(cleanLine) > 5 {
					// Skip lines that are mostly symbols or very short
					letterCount := 0
					for _, r := range cleanLine {
						if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
							letterCount++
						}
					}

					// Only keep lines with reasonable letter content
					if letterCount > 3 && float64(letterCount)/float64(len(cleanLine)) > 0.3 {
						contentLines = append(contentLines, cleanLine)
					}
				}
			}
		}

		if len(contentLines) > 0 {
			result := strings.Join(contentLines, "\n")
			// Clean up HTML entities
			result = mw.decodeHTMLEntities(result)
			return result
		}
	}

	// Fallback to standard HTML processing
	return mw.standardHTMLToText(html)
}

// decodeHTMLEntities decodes common HTML entities
func (mw *MainWindow) decodeHTMLEntities(text string) string {
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&#39;", "'")
	text = strings.ReplaceAll(text, "&apos;", "'")
	text = strings.ReplaceAll(text, "&#8217;", "'")
	text = strings.ReplaceAll(text, "&#8220;", "\"")
	text = strings.ReplaceAll(text, "&#8221;", "\"")
	text = strings.ReplaceAll(text, "&#8211;", "-")
	text = strings.ReplaceAll(text, "&#8212;", "—")
	return text
}

// standardHTMLToText performs standard HTML to text conversion
func (mw *MainWindow) standardHTMLToText(html string) string {
	// Remove CSS style blocks and inline styles
	cssBlockRegex := regexp.MustCompile(`(?s)<style[^>]*>.*?</style>`)
	plainText := cssBlockRegex.ReplaceAllString(html, "")

	// Remove script blocks
	scriptBlockRegex := regexp.MustCompile(`(?s)<script[^>]*>.*?</script>`)
	plainText = scriptBlockRegex.ReplaceAllString(plainText, "")

	// Remove HTML comments
	commentRegex := regexp.MustCompile(`(?s)<!--.*?-->`)
	plainText = commentRegex.ReplaceAllString(plainText, "")

	// Convert common block elements to line breaks before removing tags
	blockElements := []string{"div", "p", "br", "h1", "h2", "h3", "h4", "h5", "h6", "li", "tr"}
	for _, element := range blockElements {
		// Convert opening and closing tags to line breaks
		openTagRegex := regexp.MustCompile(`(?i)<` + element + `[^>]*>`)
		plainText = openTagRegex.ReplaceAllString(plainText, "\n")
		closeTagRegex := regexp.MustCompile(`(?i)</` + element + `>`)
		plainText = closeTagRegex.ReplaceAllString(plainText, "\n")
	}

	// Handle self-closing br tags specifically
	brRegex := regexp.MustCompile(`(?i)<br\s*/?>\s*`)
	plainText = brRegex.ReplaceAllString(plainText, "\n")

	// Remove all remaining HTML tags
	htmlTagRegex := regexp.MustCompile(`<[^>]*>`)
	plainText = htmlTagRegex.ReplaceAllString(plainText, "")

	// Decode HTML entities
	plainText = mw.decodeHTMLEntities(plainText)

	// Clean up excessive whitespace and line breaks
	plainText = regexp.MustCompile(`[ \t]+`).ReplaceAllString(plainText, " ")
	plainText = regexp.MustCompile(`\n\s*\n\s*\n+`).ReplaceAllString(plainText, "\n\n")
	plainText = regexp.MustCompile(`^\s+|\s+$`).ReplaceAllString(plainText, "")

	return plainText
}

// htmlToMarkdown converts HTML content to markdown for rich text display using goquery
func (mw *MainWindow) htmlToMarkdown(html string) string {
	// Use goquery for proper HTML parsing
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		// Fallback to simple text extraction if parsing fails
		return mw.extractTextFromHTML(html)
	}

	// Remove style and script elements completely
	doc.Find("style, script").Remove()

	var result strings.Builder

	// Process the document recursively
	mw.processHTMLNode(doc.Selection, &result, 0)

	content := result.String()

	// Clean up excessive whitespace while preserving intentional formatting
	content = regexp.MustCompile(`[ \t]+`).ReplaceAllString(content, " ")
	content = regexp.MustCompile(`\n\s*\n\s*\n+`).ReplaceAllString(content, "\n\n")
	content = strings.TrimSpace(content)

	return content
}

// TestHTMLToMarkdown is a helper function for testing HTML to markdown conversion
func (mw *MainWindow) TestHTMLToMarkdown(html string) string {
	return mw.htmlToMarkdown(html)
}

// processHTMLNode recursively processes HTML nodes and converts them to markdown
func (mw *MainWindow) processHTMLNode(s *goquery.Selection, result *strings.Builder, depth int) {
	s.Contents().Each(func(i int, child *goquery.Selection) {
		if goquery.NodeName(child) == "#text" {
			// Text node - add the text content with proper spacing
			text := child.Text()
			if text != "" {
				// Preserve spaces but normalize whitespace
				text = strings.ReplaceAll(text, "\n", " ")
				text = strings.ReplaceAll(text, "\t", " ")
				// Collapse multiple spaces but preserve single spaces
				for strings.Contains(text, "  ") {
					text = strings.ReplaceAll(text, "  ", " ")
				}
				result.WriteString(text)
			}
		} else {
			// Element node - handle based on tag type
			tagName := goquery.NodeName(child)
			switch strings.ToLower(tagName) {
			case "h1":
				result.WriteString("\n\n# ")
				mw.processHTMLNode(child, result, depth+1)
				result.WriteString("\n\n")
			case "h2":
				result.WriteString("\n\n## ")
				mw.processHTMLNode(child, result, depth+1)
				result.WriteString("\n\n")
			case "h3":
				result.WriteString("\n\n### ")
				mw.processHTMLNode(child, result, depth+1)
				result.WriteString("\n\n")
			case "h4":
				result.WriteString("\n\n#### ")
				mw.processHTMLNode(child, result, depth+1)
				result.WriteString("\n\n")
			case "h5":
				result.WriteString("\n\n##### ")
				mw.processHTMLNode(child, result, depth+1)
				result.WriteString("\n\n")
			case "h6":
				result.WriteString("\n\n###### ")
				mw.processHTMLNode(child, result, depth+1)
				result.WriteString("\n\n")
			case "p":
				result.WriteString("\n\n")
				mw.processHTMLNode(child, result, depth+1)
				result.WriteString("\n\n")
			case "br":
				result.WriteString("\n")
			case "strong", "b":
				result.WriteString("**")
				mw.processHTMLNode(child, result, depth+1)
				result.WriteString("**")
			case "em", "i":
				result.WriteString("*")
				mw.processHTMLNode(child, result, depth+1)
				result.WriteString("*")
			case "a":
				href, exists := child.Attr("href")
				if exists {
					// Get the text content of the link
					var linkTextBuilder strings.Builder
					mw.processHTMLNode(child, &linkTextBuilder, depth+1)
					linkText := strings.TrimSpace(linkTextBuilder.String())

					// If no meaningful text content, try to get alt text from images or use a generic label
					if linkText == "" {
						if alt, hasAlt := child.Find("img").First().Attr("alt"); hasAlt && alt != "" {
							linkText = alt
						} else if child.Find("img").Length() > 0 {
							linkText = "Image Link"
						} else {
							linkText = "Link"
						}
					}

					result.WriteString("[")
					result.WriteString(linkText)
					result.WriteString("](")
					result.WriteString(href)
					result.WriteString(")")
				} else {
					mw.processHTMLNode(child, result, depth+1)
				}
			case "ul", "ol":
				result.WriteString("\n\n")
				mw.processHTMLNode(child, result, depth+1)
				result.WriteString("\n\n")
			case "li":
				result.WriteString("- ")
				mw.processHTMLNode(child, result, depth+1)
				result.WriteString("\n")
			case "blockquote":
				result.WriteString("\n\n> ")
				mw.processHTMLNode(child, result, depth+1)
				result.WriteString("\n\n")
			case "code":
				result.WriteString("`")
				mw.processHTMLNode(child, result, depth+1)
				result.WriteString("`")
			case "pre":
				result.WriteString("\n\n```\n")
				mw.processHTMLNode(child, result, depth+1)
				result.WriteString("\n```\n\n")
			case "div":
				mw.processHTMLNode(child, result, depth+1)
				result.WriteString("\n")
			case "style", "script", "head", "meta", "title", "link", "noscript":
				// Skip these elements entirely - they contain metadata, not content
				// Don't process their children
			case "img":
				// For images, try to use alt text if available
				if alt, exists := child.Attr("alt"); exists && alt != "" {
					result.WriteString(alt)
				}
			default:
				// For other elements, just process their content
				mw.processHTMLNode(child, result, depth+1)
			}
		}
	})
}

// extractTextFromHTML is a fallback function for simple text extraction
func (mw *MainWindow) extractTextFromHTML(html string) string {
	// Simple fallback - remove all HTML tags and decode entities
	content := regexp.MustCompile(`<[^>]*>`).ReplaceAllString(html, "")
	content = mw.decodeHTMLEntities(content)
	content = strings.TrimSpace(content)
	return content
}

// formatAddresses formats a slice of addresses for display
func (mw *MainWindow) formatAddresses(addresses []email.Address) string {
	if len(addresses) == 0 {
		return ""
	}

	var formatted []string
	for _, addr := range addresses {
		if addr.Name != "" && addr.Email != "" {
			// Always show both name and email address in the format: Name <email@domain.com>
			formatted = append(formatted, fmt.Sprintf("%s <%s>", addr.Name, addr.Email))
		} else if addr.Email != "" {
			// Only email available
			formatted = append(formatted, addr.Email)
		} else if addr.Name != "" {
			// Only name available (unusual case)
			formatted = append(formatted, addr.Name)
		}
	}

	return strings.Join(formatted, ", ")
}

// getDisplayDate returns the appropriate date for display in local timezone
// Prefers InternalDate (actual arrival) over Date (potentially forged header)
// getDisplayDateString returns the display date as a formatted string
func (mw *MainWindow) getDisplayDateString(msg *email.Message) string {
	return mw.getDisplayDate(msg).Format("January 2, 2006 at 3:04 PM")
}

func (mw *MainWindow) getDisplayDate(msg *email.Message) time.Time {
	var displayDate time.Time

	// Use InternalDate if available (actual arrival date from IMAP server - cannot be forged)
	if !msg.InternalDate.IsZero() {
		displayDate = msg.InternalDate
	} else {
		// Fallback to Date header (can be forged by spammers)
		displayDate = msg.Date
	}

	// Return in local timezone for user-friendly display
	return displayDate.Local()
}

// getSortDate returns the date for sorting purposes, normalized to UTC
// This ensures consistent sorting regardless of timezone
func (mw *MainWindow) getSortDate(msg *email.Message) time.Time {
	var sortDate time.Time

	// Use InternalDate if available (actual arrival date from IMAP server - cannot be forged)
	if !msg.InternalDate.IsZero() {
		sortDate = msg.InternalDate
	} else {
		// Fallback to Date header (can be forged by spammers)
		sortDate = msg.Date
	}

	// Normalize to UTC for consistent sorting and comparison
	// This ensures messages from different timezones are properly ordered
	return sortDate.UTC()
}

// Toolbar action methods
func (mw *MainWindow) showFolderSubscriptions() {
	if mw.accountController.GetCurrentAccount() == nil {
		dialog.ShowInformation("No Account", "Please select an account first", mw.window)
		return
	}

	if mw.imapClient == nil {
		dialog.ShowInformation("Not Connected", "Please connect to an account first", mw.window)
		return
	}

	// Create and show folder subscription dialog with debounced refresh callbacks
	folderDialog := NewFolderSubscriptionDialog(mw.app, mw.imapClient, mw.logger, func() {
		// Refresh folder list when dialog is closed using debounced refresh
		mw.refreshFoldersDebounced(true, func(err error) {
			if err != nil {
				mw.logger.Error("Failed to refresh folders after dialog close: %v", err)
				fyne.Do(func() {
					mw.statusBar.SetText(fmt.Sprintf("Failed to refresh folders: %v", err))
				})
			} else {
				fyne.Do(func() {
					mw.statusBar.SetText("Folders refreshed successfully")
				})
			}
		})
	}, func() {
		// Reload folder list when a folder is created (without invalidating cache) using debounced refresh
		mw.refreshFoldersDebounced(false, func(err error) {
			if err != nil {
				mw.logger.Error("Failed to reload folders after creation: %v", err)
				fyne.Do(func() {
					mw.statusBar.SetText(fmt.Sprintf("Failed to reload folders: %v", err))
				})
			} else {
				fyne.Do(func() {
					mw.statusBar.SetText("Folders reloaded successfully")
				})
			}
		})
	}, func(deletedFolder string) {
		// Handle deleted folder to avoid selection issues
		mw.logger.Info("Folder deletion callback triggered for folder: %s", deletedFolder)
		mw.handleDeletedFolder(deletedFolder)
		// Also refresh folders after handling deletion
		mw.refreshFoldersDebounced(true, func(err error) {
			if err != nil {
				mw.logger.Error("Failed to refresh folders after deletion: %v", err)
			}
		})
	})
	folderDialog.Show()
}

// showFolderSubscriptionsForAccount opens folder subscription dialog for a specific account
func (mw *MainWindow) showFolderSubscriptionsForAccount(account *config.Account) {
	if account == nil {
		dialog.ShowInformation("No Account", "No account specified", mw.window)
		return
	}

	// Show loading dialog
	loadingDialog := dialog.NewInformation("Connecting", "Connecting to account for folder management...", mw.window)
	loadingDialog.Show()

	go func() {
		// Create temporary IMAP client for this account
		imapConfig := email.ServerConfig{
			Host:     account.IMAP.Host,
			Port:     account.IMAP.Port,
			Username: account.IMAP.Username,
			Password: account.IMAP.Password,
			TLS:      account.IMAP.TLS,
		}

		// Use account name as cache key
		accountKey := fmt.Sprintf("account_%s", account.Name)

		// Get tracing configuration and create tracer if enabled
		var tracer io.Writer
		tracingConfig := mw.config.GetTracing()
		if tracingConfig.IMAP.Enabled {
			imapTracer := trace.NewIMAPTracer(true, tracingConfig.IMAP.Directory)
			tracer = imapTracer.GetAccountTracer(account.Name)
		}

		tempClient := imap.NewClientWrapperWithCacheAndTracer(imapConfig, mw.cache, accountKey, tracer)

		// Start the worker
		if err := tempClient.Start(); err != nil {
			fyne.Do(func() {
				loadingDialog.Hide()
				dialog.ShowError(fmt.Errorf("failed to start IMAP worker: %w", err), mw.window)
			})
			return
		}

		// Connect to server
		if err := tempClient.Connect(); err != nil {
			fyne.Do(func() {
				loadingDialog.Hide()
				dialog.ShowError(fmt.Errorf("failed to connect to %s: %w", account.Name, err), mw.window)
			})
			tempClient.Stop()
			return
		}

		// Create and show folder subscription dialog
		fyne.Do(func() {
			loadingDialog.Hide()

			folderDialog := NewFolderSubscriptionDialog(mw.app, tempClient, mw.logger, func() {
				// Clean up temporary client when dialog is closed
				go func() {
					tempClient.Stop()
				}()
				mw.logger.Debug("Folder subscription dialog closed, temporary client stopped")
			}, nil, nil)

			folderDialog.Show()
		})
	}()
}

// getAccountByName returns the account with the given name.
// Returns nil if the account is not found.
func (mw *MainWindow) getAccountByName(accountName string) *config.Account {
	accounts := mw.config.GetAccounts()
	for i, acc := range accounts {
		if acc.Name == accountName {
			return &accounts[i]
		}
	}
	return nil
}

func (mw *MainWindow) composeMessage() {
	if mw.accountController.GetCurrentAccount() == nil {
		// In unified inbox mode — let the user pick which account/persona to send from
		mw.showAccountSelectionForCompose()
		return
	}

	opts := ComposeOptions{
		Account:        mw.accountController.GetCurrentAccount(),
		SMTPClient:     mw.smtpClient,
		AddressbookMgr: mw.addressbookMgr,
		OnSent: func() {
			// Use debounced refresh to refresh sent folder after sending
			if mw.accountController.GetCurrentAccount().SentFolder != "" {
				mw.refreshMessagesDebounced(mw.accountController.GetCurrentAccount().SentFolder, func(err error) {
					if err != nil {
						mw.logger.Error("Failed to refresh sent folder after sending: %v", err)
					}
				})
			}
			fyne.Do(func() {
				mw.statusBar.SetText("Message sent successfully")
			})
		},
		OnClosed: func() {
			mw.statusBar.SetText("Compose window closed")
		},
	}

	composeWindow := NewComposeWindow(mw.app, mw.configManagerToConfig(), opts)
	composeWindow.Show()
}

// showAccountSelectionForCompose shows a dialog for selecting a sending account/persona
// when composing a new message from the unified inbox view.
func (mw *MainWindow) showAccountSelectionForCompose() {
	accounts := mw.config.GetAccounts()
	if len(accounts) == 0 {
		dialog.ShowInformation("No Accounts", "No email accounts are configured.", mw.window)
		return
	}

	type accountEntry struct {
		accountIndex     int
		personalityEmail string // empty = main account identity
		label            string
	}

	entries := make([]accountEntry, 0)
	for i, acc := range accounts {
		// Main account identity
		entries = append(entries, accountEntry{
			accountIndex:     i,
			personalityEmail: "",
			label:            fmt.Sprintf("%s (%s)", acc.Name, acc.Email),
		})
		// Personas
		for _, p := range acc.Personalities {
			entries = append(entries, accountEntry{
				accountIndex:     i,
				personalityEmail: p.Email,
				label:            fmt.Sprintf("%s — %s (%s)", acc.Name, p.DisplayName, p.Email),
			})
		}
	}

	options := make([]string, len(entries))
	for i, e := range entries {
		options[i] = e.label
	}

	sel := widget.NewSelect(options, nil)
	if len(options) > 0 {
		sel.SetSelected(options[0])
	}

	content := container.NewVBox(
		widget.NewLabel("Select the account or persona to send from:"),
		sel,
	)

	dialog.ShowCustomConfirm("Select Sending Account", "Compose", "Cancel", content, func(confirmed bool) {
		if !confirmed || sel.Selected == "" {
			return
		}

		// Find the selected entry
		var selectedEntry *accountEntry
		for i, option := range options {
			if option == sel.Selected {
				e := entries[i]
				selectedEntry = &e
				break
			}
		}
		if selectedEntry == nil {
			return
		}

		allAccounts := mw.config.GetAccounts()
		account := &allAccounts[selectedEntry.accountIndex]

		smtpClient := smtp.NewClient(email.ServerConfig{
			Host:     account.SMTP.Host,
			Port:     account.SMTP.Port,
			Username: account.SMTP.Username,
			Password: account.SMTP.Password,
			TLS:      account.SMTP.TLS,
		})

		opts := ComposeOptions{
			Account:        account,
			SMTPClient:     smtpClient,
			AddressbookMgr: mw.addressbookMgr,
			SelectedFrom:   selectedEntry.personalityEmail,
			OnSent: func() {
				if account.SentFolder != "" {
					mw.refreshMessagesDebounced(account.SentFolder, func(err error) {
						if err != nil {
							mw.logger.Error("Failed to refresh sent folder after sending: %v", err)
						}
					})
				}
				fyne.Do(func() {
					mw.statusBar.SetText("Message sent successfully")
				})
			},
			OnClosed: func() {
				mw.statusBar.SetText("Compose window closed")
			},
		}

		composeWindow := NewComposeWindow(mw.app, mw.configManagerToConfig(), opts)
		composeWindow.Show()
	}, mw.window)
}

func (mw *MainWindow) replyToMessage() {
	if mw.selectedMessage == nil {
		dialog.ShowInformation("No Message", "Please select a message to reply to", mw.window)
		return
	}

	// Get account from config using the account name from MessageIndexItem
	account := mw.getAccountByName(mw.selectedMessage.AccountName)
	if account == nil {
		dialog.ShowError(fmt.Errorf("Cannot reply: account %s not found", mw.selectedMessage.AccountName), mw.window)
		return
	}

	opts := ComposeOptions{
		Account:        account,
		SMTPClient:     mw.selectedMessage.SMTPClient.(*smtp.Client),
		AddressbookMgr: mw.addressbookMgr,
		ReplyTo:        &mw.selectedMessage.Message,
		OnSent: func() {
			// Use RefreshCoordinator to refresh sent folder after reply
			if account.SentFolder != "" {
				mw.refreshMessagesDebounced(account.SentFolder, func(err error) {
					if err != nil {
						mw.logger.Error("Failed to refresh sent folder after reply: %v", err)
					}
				})
			}
			fyne.Do(func() {
				mw.statusBar.SetText("Reply sent successfully")
			})
		},
		OnClosed: func() {
			mw.statusBar.SetText("Reply window closed")
		},
	}

	composeWindow := NewComposeWindow(mw.app, mw.configManagerToConfig(), opts)
	composeWindow.Show()
}

func (mw *MainWindow) replyAllToMessage() {
	if mw.selectedMessage == nil {
		dialog.ShowInformation("No Message", "Please select a message to reply to", mw.window)
		return
	}

	// Get account from config using the account name from MessageIndexItem
	account := mw.getAccountByName(mw.selectedMessage.AccountName)
	if account == nil {
		dialog.ShowError(fmt.Errorf("Cannot reply all: account %s not found", mw.selectedMessage.AccountName), mw.window)
		return
	}

	opts := ComposeOptions{
		Account:        account,
		SMTPClient:     mw.selectedMessage.SMTPClient.(*smtp.Client),
		AddressbookMgr: mw.addressbookMgr,
		ReplyTo:        &mw.selectedMessage.Message,
		ReplyAll:       true, // This is the key difference from regular reply
		OnSent: func() {
			// Use RefreshCoordinator to refresh sent folder after reply all
			if account.SentFolder != "" {
				mw.refreshMessagesDebounced(account.SentFolder, func(err error) {
					if err != nil {
						mw.logger.Error("Failed to refresh sent folder after reply all: %v", err)
					}
				})
			}
			fyne.Do(func() {
				mw.statusBar.SetText("Reply sent successfully")
			})
		},
		OnClosed: func() {
			mw.statusBar.SetText("Reply window closed")
		},
	}

	composeWindow := NewComposeWindow(mw.app, mw.configManagerToConfig(), opts)
	composeWindow.Show()
}

func (mw *MainWindow) forwardMessage() {
	if mw.selectedMessage == nil {
		dialog.ShowInformation("No Message", "Please select a message to forward", mw.window)
		return
	}

	// Get account from config using the account name from MessageIndexItem
	account := mw.getAccountByName(mw.selectedMessage.AccountName)
	if account == nil {
		dialog.ShowError(fmt.Errorf("Cannot forward: account %s not found", mw.selectedMessage.AccountName), mw.window)
		return
	}

	opts := ComposeOptions{
		Account:        account,
		SMTPClient:     mw.selectedMessage.SMTPClient.(*smtp.Client),
		AddressbookMgr: mw.addressbookMgr,
		Forward:        &mw.selectedMessage.Message,
		OnSent: func() {
			// Use RefreshCoordinator to refresh sent folder after forward
			if account.SentFolder != "" {
				mw.refreshMessagesDebounced(account.SentFolder, func(err error) {
					if err != nil {
						mw.logger.Error("Failed to refresh sent folder after forward: %v", err)
					}
				})
			}
			fyne.Do(func() {
				mw.statusBar.SetText("Message forwarded successfully")
			})
		},
		OnClosed: func() {
			mw.statusBar.SetText("Forward window closed")
		},
	}

	composeWindow := NewComposeWindow(mw.app, mw.configManagerToConfig(), opts)
	composeWindow.Show()
}

func (mw *MainWindow) deleteMessage() {
	// Check if multiple messages are selected
	selectedMessages := mw.getSelectedMessages()
	if len(selectedMessages) > 1 {
		mw.deleteMessagesMultiple()
		return
	}

	if mw.selectedMessage == nil {
		dialog.ShowInformation("No Message", "Please select a message to delete", mw.window)
		return
	}

	// Show confirmation dialog with account info (works for both unified and regular inbox)
	if mw.accountController.IsUnifiedInbox() {
		dialog.ShowConfirm("Delete Message",
			fmt.Sprintf("Are you sure you want to permanently delete the message from %s:\n\n%s",
				mw.selectedMessage.AccountName, mw.selectedMessage.Message.Subject),
			func(confirmed bool) {
				if confirmed {
					mw.performDelete()
				}
			}, mw.window)
	} else {
		// Regular mode
		if mw.accountController.GetCurrentAccount() == nil || mw.imapClient == nil {
			dialog.ShowInformation("No Connection", "Please connect to an account first", mw.window)
			return
		}

		// Show confirmation dialog
		dialog.ShowConfirm("Delete Message",
			fmt.Sprintf("Are you sure you want to permanently delete the message:\n\n%s", mw.selectedMessage.Message.Subject),
			func(confirmed bool) {
				if confirmed {
					mw.performDelete()
				}
			}, mw.window)
	}
}

func (mw *MainWindow) moveToTrash() {
	// Check if multiple messages are selected
	selectedMessages := mw.getSelectedMessages()
	if len(selectedMessages) > 1 {
		mw.moveToTrashMultiple()
		return
	}

	if mw.selectedMessage == nil {
		dialog.ShowInformation("No Message", "Please select a message to move to trash", mw.window)
		return
	}

	// Get the correct IMAP client and account for the selected message
	var imapClient *imap.ClientWrapper
	var currentAccount *config.Account

	if mw.accountController.IsUnifiedInbox() {
		// In unified inbox mode, get the account-specific IMAP client
		// Lock handled by AccountController
		accountClient, exists := mw.accountController.GetIMAPClientForAccount(mw.selectedMessage.AccountName)
		// Unlock handled by AccountController

		if !exists || accountClient == nil {
			dialog.ShowInformation("No Connection", fmt.Sprintf("No connection available for account %s", mw.selectedMessage.AccountName), mw.window)
			return
		}

		imapClient = accountClient

		// Get the account configuration
		accounts := mw.config.GetAccounts()
		for i, account := range accounts {
			if account.Name == mw.selectedMessage.AccountName {
				currentAccount = &accounts[i]
				break
			}
		}

		if currentAccount == nil {
			dialog.ShowInformation("Account Error", fmt.Sprintf("Account configuration not found for %s", mw.selectedMessage.AccountName), mw.window)
			return
		}
	} else {
		// Regular mode
		if mw.accountController.GetCurrentAccount() == nil || mw.imapClient == nil {
			dialog.ShowInformation("No Connection", "Please connect to an account first", mw.window)
			return
		}

		imapClient = mw.imapClient
		currentAccount = mw.accountController.GetCurrentAccount()
	}

	var trashFolder string

	// Get subscribed folders to ensure we only use folders the user has access to
	subscribedFolders, err := imapClient.ListSubscribedFolders()
	if err != nil {
		dialog.ShowError(fmt.Errorf("Failed to get subscribed folders: %v", err), mw.window)
		return
	}

	// Create a map of subscribed folder names for quick lookup
	subscribedMap := make(map[string]bool)
	for _, folder := range subscribedFolders {
		subscribedMap[folder.Name] = true
	}

	// First, check if a trash folder is configured for this account and is subscribed
	if currentAccount.TrashFolder != "" {
		if subscribedMap[currentAccount.TrashFolder] {
			trashFolder = currentAccount.TrashFolder
			mw.logger.Debug("Using configured trash folder: %s", trashFolder)
		} else {
			mw.logger.Warn("Configured trash folder %s is not subscribed", currentAccount.TrashFolder)
		}
	}

	// If no configured trash folder or it's not subscribed, fall back to auto-detection
	if trashFolder == "" {
		trashFolders := []string{"Trash", "INBOX.Trash", "Deleted Items", "Deleted Messages", "[Gmail]/Trash"}

		for _, folder := range subscribedFolders {
			for _, trashName := range trashFolders {
				if strings.EqualFold(folder.Name, trashName) {
					trashFolder = folder.Name
					break
				}
			}
			if trashFolder != "" {
				break
			}
		}

		if trashFolder == "" {
			// No trash folder found among subscribed folders
			dialog.ShowConfirm("No Trash Folder",
				"No trash folder found among your subscribed folders. You can:\n\n1. Configure a trash folder in your account settings\n2. Subscribe to a trash folder using the folder subscription dialog\n\nDelete message permanently?",
				func(confirmed bool) {
					if confirmed {
						mw.performDelete()
					}
				}, mw.window)
			return
		}

		mw.logger.Debug("Auto-detected trash folder among subscribed folders: %s", trashFolder)
	}

	// Capture the selected message to avoid race conditions in the goroutine
	mw.messagesMu.RLock()
	selectedMessage := mw.selectedMessage
	mw.messagesMu.RUnlock()
	if selectedMessage == nil {
		mw.statusBar.SetText("No message selected")
		return
	}

	// Move to trash folder
	mw.statusBar.SetText(fmt.Sprintf("Moving message to trash folder '%s'...", trashFolder))
	go func() {
		mw.logger.Debug("Starting move operation for message %d to trash folder %s", selectedMessage.Message.UID, trashFolder)

		// For unified inbox mode, set a flag to prevent IDLE monitoring from triggering
		// automatic refresh that would interfere with our optimistic update
		if mw.accountController.IsUnifiedInbox() {
			mw.deleteOperationMutex.Lock()
			if mw.unifiedInboxDeleteInProgress == nil {
				mw.unifiedInboxDeleteInProgress = make(map[string]bool)
			}
			mw.unifiedInboxDeleteInProgress[selectedMessage.AccountName] = true
			mw.deleteOperationMutex.Unlock()
			mw.logger.Debug("Set unified inbox delete operation flag for account %s", selectedMessage.AccountName)
		}

		// Use the MessageIndexItem's MoveTo method - much simpler!
		mw.logger.Debug("Calling MoveTo method for message %d", selectedMessage.Message.UID)
		err := selectedMessage.MoveTo(trashFolder)
		mw.logger.Debug("MoveTo method returned for message %d, error: %v", selectedMessage.Message.UID, err)

		// Clear the delete operation flag after the operation completes
		if mw.accountController.IsUnifiedInbox() {
			// Use a small delay to allow our optimistic update to complete first
			go func() {
				accountName := selectedMessage.AccountName // Capture the account name
				time.Sleep(200 * time.Millisecond)
				mw.deleteOperationMutex.Lock()
				if mw.unifiedInboxDeleteInProgress != nil {
					delete(mw.unifiedInboxDeleteInProgress, accountName)
				}
				mw.deleteOperationMutex.Unlock()
				mw.logger.Debug("Cleared unified inbox delete operation flag for account %s", accountName)
			}()
		}

		if err != nil {
			fyne.Do(func() {
				mw.statusBar.SetText(fmt.Sprintf("Failed to move to trash: %v", err))
			})
			mw.logger.Error("Failed to move message %d to trash folder %s: %v", selectedMessage.Message.UID, trashFolder, err)
			return
		}

		mw.logger.Info("Successfully moved message %d to trash folder %s", selectedMessage.Message.UID, trashFolder)

		// Handle refresh differently for unified inbox vs regular mode
		if mw.accountController.IsUnifiedInbox() {
			// For unified inbox, use optimistic UI update followed by proper refresh coordination
			fyne.Do(func() {
				// Find and remove the deleted message from the local array for immediate UI feedback
				deletedUID := selectedMessage.Message.UID
				deletedAccountName := selectedMessage.AccountName

				mw.logger.Debug("Attempting to remove message UID=%d from account %s (current array size: %d)", deletedUID, deletedAccountName, len(mw.messages))

				// Remove the message from the local array and track the index for auto-selection
				messageRemoved := false
				deletedIndex := -1
				for i, msg := range mw.messages {
					if msg.Message.UID == deletedUID && msg.AccountName == deletedAccountName {
						// Remove the message at index i
						mw.messages = append(mw.messages[:i], mw.messages[i+1:]...)
						mw.logger.Debug("Successfully removed message from local array at index %d (UID=%d, Account=%s, new size: %d)", i, deletedUID, deletedAccountName, len(mw.messages))
						messageRemoved = true
						deletedIndex = i
						break
					}
				}

				if !messageRemoved {
					mw.logger.Error("Failed to find message to remove from local array (UID=%d, Account=%s)", deletedUID, deletedAccountName)
					mw.statusBar.SetText("Message moved to trash (UI update failed)")
					return
				}

				// Validate message array consistency after removal
				if !mw.validateMessageArrayConsistency() {
					mw.logger.Error("Message array consistency check failed after removing deleted message")
				}

				// Clear current selection first to avoid stale references
				mw.selectedMessage = nil
				mw.messageList.UnselectAll()

				// Force the message list to refresh with new length
				mw.refreshMessageList()
				mw.logger.Debug("Forced message list refresh after removal (new length: %d)", len(mw.messages))

				// Auto-select next message if we have messages remaining
				if len(mw.messages) > 0 {
					// Try to select the message at the same index, or the last message if we were at the end
					nextIndex := deletedIndex
					if nextIndex >= len(mw.messages) {
						nextIndex = len(mw.messages) - 1
					}

					mw.logger.Debug("Auto-selecting next message at index %d after deletion", nextIndex)

					// Use a small delay to ensure the list widget has processed the refresh
					go func() {
						time.Sleep(50 * time.Millisecond)
						fyne.Do(func() {
							mw.selectMessage(nextIndex)
							mw.messageList.Select(nextIndex)
						})
					}()
				} else {
					// No messages left - clear the message display
					mw.messageViewController.ClearMessageView()
				}

				mw.statusBar.SetText("Message moved to trash")
			})

			// Skip the expensive full unified inbox refresh since the optimistic update is sufficient
			// The message has been successfully moved on the server, and we've updated the local UI
			// Real-time monitoring will catch any other changes from other clients
			mw.logger.Debug("Skipping full unified inbox refresh after trash move - using optimistic update only")
		} else {
			// Use RefreshCoordinator for regular folder refresh
			currentFolder := mw.folderController.GetCurrentFolder()
			mw.refreshMessagesDebounced(currentFolder, func(err error) {
				if err != nil {
					mw.logger.Error("Failed to refresh messages after moving to trash: %v", err)
					fyne.Do(func() {
						mw.statusBar.SetText(fmt.Sprintf("Message moved to trash, but refresh failed: %v", err))
					})
				} else {
					fyne.Do(func() {
						mw.statusBar.SetText("Message moved to trash")
					})
				}
			})
		}
	}()
}

func (mw *MainWindow) performDelete() {
	// Capture the selected message to avoid race conditions in the goroutine
	mw.messagesMu.RLock()
	selectedMessage := mw.selectedMessage
	mw.messagesMu.RUnlock()
	if selectedMessage == nil {
		mw.statusBar.SetText("No message selected")
		return
	}

	mw.statusBar.SetText("Deleting message...")
	go func() {
		// For unified inbox mode, set a flag to prevent IDLE monitoring from triggering
		// automatic refresh that would interfere with our optimistic update
		if mw.accountController.IsUnifiedInbox() {
			mw.deleteOperationMutex.Lock()
			if mw.unifiedInboxDeleteInProgress == nil {
				mw.unifiedInboxDeleteInProgress = make(map[string]bool)
			}
			mw.unifiedInboxDeleteInProgress[selectedMessage.AccountName] = true
			mw.deleteOperationMutex.Unlock()
			mw.logger.Debug("Set unified inbox delete operation flag for account %s", selectedMessage.AccountName)
		}

		// Use the MessageIndexItem's Delete method - much simpler!
		err := selectedMessage.Delete()

		// Clear the delete operation flag after the operation completes
		if mw.accountController.IsUnifiedInbox() {
			// Use a small delay to allow our optimistic update to complete first
			go func() {
				accountName := selectedMessage.AccountName // Capture the account name
				time.Sleep(200 * time.Millisecond)
				mw.deleteOperationMutex.Lock()
				if mw.unifiedInboxDeleteInProgress != nil {
					delete(mw.unifiedInboxDeleteInProgress, accountName)
				}
				mw.deleteOperationMutex.Unlock()
				mw.logger.Debug("Cleared unified inbox delete operation flag for account %s", accountName)
			}()
		}
		if err != nil {
			fyne.Do(func() {
				mw.statusBar.SetText(fmt.Sprintf("Failed to delete message: %v", err))
			})
			return
		}

		// Use RefreshCoordinator to refresh messages after deletion
		if mw.accountController.IsUnifiedInbox() {
			// For unified inbox, use optimistic UI update followed by proper refresh coordination
			fyne.Do(func() {
				// Find and remove the deleted message from the local array for immediate UI feedback
				deletedUID := selectedMessage.Message.UID
				deletedAccountName := selectedMessage.AccountName

				mw.logger.Debug("Attempting to remove deleted message UID=%d from account %s (current array size: %d)", deletedUID, deletedAccountName, len(mw.messages))

				// Remove the message from the local array
				messageRemoved := false
				for i, msg := range mw.messages {
					if msg.Message.UID == deletedUID && msg.AccountName == deletedAccountName {
						// Remove the message at index i
						mw.messages = append(mw.messages[:i], mw.messages[i+1:]...)
						mw.logger.Debug("Successfully removed deleted message from local array at index %d (UID=%d, Account=%s, new size: %d)", i, deletedUID, deletedAccountName, len(mw.messages))
						messageRemoved = true
						break
					}
				}

				if !messageRemoved {
					mw.logger.Error("Failed to find message to remove from local array (UID=%d, Account=%s)", deletedUID, deletedAccountName)
					mw.statusBar.SetText("Message deleted (UI update failed)")
					return
				}

				// Validate message array consistency after removal
				if !mw.validateMessageArrayConsistency() {
					mw.logger.Error("Message array consistency check failed after removing deleted message")
				}

				// Clear current selection first to avoid stale references
				mw.selectedMessage = nil
				mw.messageList.UnselectAll()

				// Force the message list to refresh with new length
				mw.refreshMessageList()
				mw.logger.Debug("Forced message list refresh after deletion (new length: %d)", len(mw.messages))

				// Clear the message display since no message is selected
				mw.messageViewController.ClearMessageView()

				mw.statusBar.SetText("Message deleted")
			})

			// Skip the expensive full unified inbox refresh since the optimistic update is sufficient
			// The message has been successfully deleted on the server, and we've updated the local UI
			// Real-time monitoring will catch any other changes from other clients
			mw.logger.Debug("Skipping full unified inbox refresh after deletion - using optimistic update only")
		} else {
			// Use RefreshCoordinator for regular folder refresh
			currentFolder := mw.folderController.GetCurrentFolder()
			mw.refreshMessagesDebounced(currentFolder, func(err error) {
				if err != nil {
					mw.logger.Error("Failed to refresh messages after deletion: %v", err)
					fyne.Do(func() {
						mw.statusBar.SetText(fmt.Sprintf("Message deleted, but refresh failed: %v", err))
					})
				} else {
					fyne.Do(func() {
						mw.statusBar.SetText("Message deleted")
					})
				}
			})
		}
	}()
}

// showMessageContextMenu displays a context menu for the selected message
func (mw *MainWindow) showMessageContextMenu() {
	if mw.selectedMessage == nil {
		dialog.ShowInformation("No Message", "Please select a message first", mw.window)
		return
	}

	mw.logger.Debug("Creating context menu for message: %s", mw.selectedMessage.Message.Subject)

	// Show the context menu at a reasonable position
	canvasSize := mw.window.Canvas().Size()
	menuPos := fyne.NewPos(canvasSize.Width/2-100, canvasSize.Height/2-100)

	mw.logger.Debug("Showing context menu at position: %v", menuPos)
	mw.showCustomContextMenuAtPosition(menuPos)
}

// handleRightClickAtPosition handles right-click at a specific position
func (mw *MainWindow) handleRightClickAtPosition(pos fyne.Position) {
	mw.logger.Debug("Handling right-click at position: %v", pos)
	// For now, we'll show the context menu for the currently selected message
	// In a more advanced implementation, we could calculate which message was clicked
	// based on the position and the list item height
	mw.showMessageContextMenuAtPosition(pos)
}

// showMessageContextMenuAtPosition shows the context menu at a specific position
func (mw *MainWindow) showMessageContextMenuAtPosition(pos fyne.Position) {
	if mw.selectedMessage == nil {
		dialog.ShowInformation("No Message", "Please select a message first", mw.window)
		return
	}

	// Create a custom popup with buttons instead of using ShowPopUpMenuAtPosition
	mw.showCustomContextMenuAtPosition(pos)
}

// showCustomContextMenuAtPosition creates a custom popup menu with buttons that can be properly dismissed
func (mw *MainWindow) showCustomContextMenuAtPosition(pos fyne.Position) {
	selectedIndices := mw.getSelectedMessageIndices()
	selectedCount := len(selectedIndices)

	var content *fyne.Container

	if selectedCount <= 1 {
		// Single message context menu
		content = mw.createSingleMessageContextMenu()
	} else {
		// Multiple messages context menu
		content = mw.createMultipleMessagesContextMenu(selectedCount)
	}

	// Add padding around the content for better appearance
	paddedContent := container.NewPadded(content)

	// Create popup and store reference for dismissal
	mw.currentContextPopup = widget.NewPopUp(paddedContent, mw.window.Canvas())

	// Set a minimum size for the popup to ensure consistent width
	mw.currentContextPopup.Resize(fyne.NewSize(180, 0)) // Fixed width, auto height
	mw.currentContextPopup.ShowAtPosition(pos)
}

// createSingleMessageContextMenu creates context menu for single message selection
func (mw *MainWindow) createSingleMessageContextMenu() *fyne.Container {
	replyButton := mw.createContextMenuButton("Reply", func() {
		mw.currentContextPopup.Hide()
		mw.replyToMessage()
	})

	replyAllButton := mw.createContextMenuButton("Reply All", func() {
		mw.currentContextPopup.Hide()
		mw.replyAllToMessage()
	})

	forwardButton := mw.createContextMenuButton("Forward", func() {
		mw.currentContextPopup.Hide()
		mw.forwardMessage()
	})

	viewHeadersButton := mw.createContextMenuButton("View Headers", func() {
		mw.currentContextPopup.Hide()
		mw.showMessageHeaders()
	})

	moveToFolderButton := mw.createContextMenuButton("Move to Folder...", func() {
		mw.currentContextPopup.Hide()
		mw.showMoveToFolderDialog()
	})

	deleteButton := mw.createContextMenuButton("Delete", func() {
		mw.currentContextPopup.Hide()
		mw.moveToTrash()
	})

	return container.NewVBox(
		replyButton,
		replyAllButton,
		forwardButton,
		widget.NewSeparator(),
		viewHeadersButton,
		widget.NewSeparator(),
		moveToFolderButton,
		widget.NewSeparator(),
		deleteButton,
	)
}

// createMultipleMessagesContextMenu creates context menu for multiple message selection
func (mw *MainWindow) createMultipleMessagesContextMenu(count int) *fyne.Container {
	// Header showing selection count
	headerLabel := widget.NewLabel(fmt.Sprintf("%d messages selected", count))
	headerLabel.TextStyle = fyne.TextStyle{Bold: true}

	moveToFolderButton := mw.createContextMenuButton("Move to Folder...", func() {
		mw.currentContextPopup.Hide()
		mw.showMoveToFolderDialogMultiple()
	})

	deleteButton := mw.createContextMenuButton(fmt.Sprintf("Delete %d Messages", count), func() {
		mw.currentContextPopup.Hide()
		mw.moveToTrashMultiple()
	})

	clearSelectionButton := mw.createContextMenuButton("Clear Selection", func() {
		mw.currentContextPopup.Hide()
		mw.clearSelection()
	})

	return container.NewVBox(
		headerLabel,
		widget.NewSeparator(),
		moveToFolderButton,
		widget.NewSeparator(),
		deleteButton,
		widget.NewSeparator(),
		clearSelectionButton,
	)
}

// createContextMenuButton creates a button styled like a context menu item
func (mw *MainWindow) createContextMenuButton(text string, onTapped func()) *widget.Button {
	button := widget.NewButton(text, onTapped)

	// Style the button to look like a context menu item
	button.Alignment = widget.ButtonAlignLeading // Left-align the text
	button.Importance = widget.LowImportance     // Remove button styling/background

	return button
}

// dismissContextMenu helper function to properly dismiss any open context menu
func (mw *MainWindow) dismissContextMenu() {
	// This function is now mainly for backward compatibility
	// The new custom popup menus are dismissed directly via popup.Hide()
	// but we keep this for any remaining native menu usage
	go func() {
		// Small delay to allow menu action to complete
		time.Sleep(50 * time.Millisecond)
		fyne.Do(func() {
			// Focus the message list which may help dismiss any remaining native menus
			mw.window.Canvas().Focus(mw.messageList)
		})
	}()
}

// showMoveToFolderDialog shows a dialog to select a folder to move the message to
func (mw *MainWindow) showMoveToFolderDialog() {
	if mw.selectedMessage == nil {
		dialog.ShowInformation("No Message", "Please select a message first", mw.window)
		return
	}

	// Get the correct IMAP client for the account
	var imapClient *imap.ClientWrapper
	if mw.accountController.IsUnifiedInbox() {
		// In unified inbox mode, get the account-specific IMAP client
		// Lock handled by AccountController
		accountClient, exists := mw.accountController.GetIMAPClientForAccount(mw.selectedMessage.AccountName)
		// Unlock handled by AccountController

		if exists && accountClient != nil {
			imapClient = accountClient
		} else {
			dialog.ShowError(fmt.Errorf("no IMAP client available for account %s", mw.selectedMessage.AccountName), mw.window)
			return
		}
	} else {
		// Regular mode - use the current IMAP client
		imapClient = mw.imapClient
	}

	if imapClient == nil {
		dialog.ShowError(fmt.Errorf("no IMAP client available"), mw.window)
		return
	}

	// Show loading dialog
	loadingDialog := dialog.NewInformation("Loading", "Fetching folders...", mw.window)
	loadingDialog.Show()

	// Fetch folders asynchronously
	go func() {
		subscribedFolders, err := imapClient.ListSubscribedFolders()

		// Update UI on main thread
		fyne.Do(func() {
			loadingDialog.Hide()

			if err != nil {
				dialog.ShowError(fmt.Errorf("failed to get folders: %w", err), mw.window)
				return
			}

			// Create folder options
			currentFolder := mw.folderController.GetCurrentFolder()
			var folderNames []string
			for _, folder := range subscribedFolders {
				if folder.Name != currentFolder { // Don't show current folder
					folderNames = append(folderNames, folder.Name)
				}
			}

			if len(folderNames) == 0 {
				dialog.ShowInformation("No Folders", "No other folders available", mw.window)
				return
			}

			// Create selection dialog
			folderSelect := widget.NewSelect(folderNames, func(selected string) {
				if selected != "" {
					mw.logger.Debug("Moving message to folder: %s", selected)
					mw.moveToFolder(selected)
				}
			})
			folderSelect.PlaceHolder = "Select a folder..."

			content := container.NewVBox(
				widget.NewLabel("Move message to folder:"),
				folderSelect,
			)

			dialog.ShowCustomConfirm("Move Message", "Move", "Cancel", content, func(confirmed bool) {
				if confirmed && folderSelect.Selected != "" {
					mw.logger.Debug("Moving message to folder: %s", folderSelect.Selected)
					mw.moveToFolder(folderSelect.Selected)
				}
			}, mw.window)
		})
	}()
}

// showMessageHeaders displays a window with the message headers in plain text
func (mw *MainWindow) showMessageHeaders() {
	mw.messagesMu.RLock()
	selMsg := mw.selectedMessage
	mw.messagesMu.RUnlock()

	if selMsg == nil {
		dialog.ShowInformation("No Message", "Please select a message first", mw.window)
		return
	}

	// Create a new window for headers
	headersWindow := mw.app.NewWindow("Message Headers")
	headersWindow.Resize(fyne.NewSize(800, 600))

	// Show loading message while fetching full headers
	loadingLabel := widget.NewLabel("Loading full message headers...")
	loadingContainer := container.NewCenter(loadingLabel)
	headersWindow.SetContent(loadingContainer)
	headersWindow.Show()

	// Fetch the full message with all headers in a goroutine
	go func() {
		var fullMessage *email.Message
		var err error

		// In unified inbox mode, we need to get the correct IMAP client for the account
		var imapClient *imap.ClientWrapper
		if mw.accountController.IsUnifiedInbox() {
			// Get the account-specific IMAP client
			accounts := mw.config.GetAccounts()
			for _, acc := range accounts {
				if acc.Name == selMsg.AccountName {
					// Get the IMAP client for this specific account
					if accountClient, exists := mw.accountController.GetIMAPClientForAccount(acc.Name); exists {
						imapClient = accountClient
						break
					}
				}
			}
		} else {
			// Regular mode - use current IMAP client
			imapClient = mw.imapClient
		}

		// Try to fetch the full message from the server to get all headers
		if imapClient != nil {
			mw.logger.Debug("Fetching full message for headers: account=%s, folder=%s, UID=%d",
				selMsg.AccountName, selMsg.FolderName, selMsg.Message.UID)

			fullMessage, err = imapClient.FetchMessageWithFullHeaders(selMsg.FolderName, selMsg.Message.UID)
			if err == nil && fullMessage != nil {
				mw.logger.Debug("Successfully fetched message with %d headers", len(fullMessage.Headers))
			}
		} else {
			// Fallback to using the cached message data
			mw.logger.Debug("No IMAP client available for account %s, using cached message data", selMsg.AccountName)
			fullMessage = &selMsg.Message
		}

		// Update UI on main thread
		fyne.Do(func() {
			if err != nil {
				// If we can't fetch the full message, show what we have with a warning
				mw.logger.Warn("Failed to fetch full message headers: %v", err)
				mw.displayHeadersInWindow(headersWindow, &mw.selectedMessage.Message, true)
			} else {
				// Display the full headers
				mw.displayHeadersInWindow(headersWindow, fullMessage, false)
			}
		})
	}()
}

// displayHeadersInWindow displays the message headers in the provided window
func (mw *MainWindow) displayHeadersInWindow(headersWindow fyne.Window, message *email.Message, isPartial bool) {
	// Format headers for display
	var headerText strings.Builder

	// Add warning if headers are partial
	if isPartial {
		headerText.WriteString("WARNING: Could not fetch full headers from server. Showing cached headers only.\n")
		headerText.WriteString("Some headers may be missing.\n\n")
	}

	// Add basic headers first
	headerText.WriteString(fmt.Sprintf("Subject: %s\n", message.Subject))
	headerText.WriteString(fmt.Sprintf("Date: %s\n", mw.getDisplayDate(message).Format(time.RFC1123Z)))

	// From addresses
	if len(message.From) > 0 {
		var fromAddrs []string
		for _, addr := range message.From {
			if addr.Name != "" {
				fromAddrs = append(fromAddrs, fmt.Sprintf("%s <%s>", addr.Name, addr.Email))
			} else {
				fromAddrs = append(fromAddrs, addr.Email)
			}
		}
		headerText.WriteString(fmt.Sprintf("From: %s\n", strings.Join(fromAddrs, ", ")))
	}

	// To addresses
	if len(message.To) > 0 {
		var toAddrs []string
		for _, addr := range message.To {
			if addr.Name != "" {
				toAddrs = append(toAddrs, fmt.Sprintf("%s <%s>", addr.Name, addr.Email))
			} else {
				toAddrs = append(toAddrs, addr.Email)
			}
		}
		headerText.WriteString(fmt.Sprintf("To: %s\n", strings.Join(toAddrs, ", ")))
	}

	// CC addresses
	if len(message.CC) > 0 {
		var ccAddrs []string
		for _, addr := range message.CC {
			if addr.Name != "" {
				ccAddrs = append(ccAddrs, fmt.Sprintf("%s <%s>", addr.Name, addr.Email))
			} else {
				ccAddrs = append(ccAddrs, addr.Email)
			}
		}
		headerText.WriteString(fmt.Sprintf("CC: %s\n", strings.Join(ccAddrs, ", ")))
	}

	// Reply-To addresses
	if len(message.ReplyTo) > 0 {
		var replyToAddrs []string
		for _, addr := range message.ReplyTo {
			if addr.Name != "" {
				replyToAddrs = append(replyToAddrs, fmt.Sprintf("%s <%s>", addr.Name, addr.Email))
			} else {
				replyToAddrs = append(replyToAddrs, addr.Email)
			}
		}
		headerText.WriteString(fmt.Sprintf("Reply-To: %s\n", strings.Join(replyToAddrs, ", ")))
	}

	// Message ID and other technical headers
	headerText.WriteString(fmt.Sprintf("Message-ID: %s\n", message.ID))
	if message.UID > 0 {
		headerText.WriteString(fmt.Sprintf("UID: %d\n", message.UID))
	}
	if message.Size > 0 {
		headerText.WriteString(fmt.Sprintf("Size: %d bytes\n", message.Size))
	}

	// Flags
	if len(message.Flags) > 0 {
		headerText.WriteString(fmt.Sprintf("Flags: %s\n", strings.Join(message.Flags, ", ")))
	}

	// Add separator before all headers
	if len(message.Headers) > 0 {
		headerText.WriteString("\n--- All Message Headers ---\n")

		// Sort header keys for consistent display
		var keys []string
		for key := range message.Headers {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		for _, key := range keys {
			headerText.WriteString(fmt.Sprintf("%s: %s\n", key, message.Headers[key]))
		}
	} else {
		headerText.WriteString("\n--- No Additional Headers Available ---\n")
		headerText.WriteString("This may indicate that full headers were not fetched from the server.\n")
	}

	// Create a multiline entry widget for the headers (supports text selection and copying)
	headersEntry := widget.NewMultiLineEntry()
	headersEntry.SetText(headerText.String())
	headersEntry.Wrapping = fyne.TextWrapWord

	// Make it read-only by intercepting key events, but keep it visually enabled for readability
	headersEntry.OnChanged = func(string) {
		// Reset to original text if user tries to edit
		headersEntry.SetText(headerText.String())
	}

	// Create scroll container for the headers
	headersScroll := container.NewScroll(headersEntry)

	// Create close button
	closeButton := widget.NewButton("Close", func() {
		headersWindow.Close()
	})

	// Create copy button
	copyButton := widget.NewButton("Copy All", func() {
		mw.window.Clipboard().SetContent(headerText.String())
		// Show brief confirmation
		dialog.ShowInformation("Copied", "Headers copied to clipboard", headersWindow)
	})

	// Layout the window
	buttonContainer := container.NewHBox(copyButton, closeButton)
	content := container.NewBorder(nil, buttonContainer, nil, nil, headersScroll)

	headersWindow.SetContent(content)
}

// moveToFolder moves the selected message to the specified folder
func (mw *MainWindow) moveToFolder(targetFolder string) {
	if mw.selectedMessage == nil {
		dialog.ShowInformation("No Message", "Please select a message to move", mw.window)
		return
	}

	if mw.accountController.GetCurrentAccount() == nil || mw.imapClient == nil {
		dialog.ShowInformation("No Connection", "Please connect to an account first", mw.window)
		return
	}

	currentFolder := mw.folderController.GetCurrentFolder()
	if targetFolder == currentFolder {
		dialog.ShowInformation("Same Folder", "Message is already in this folder", mw.window)
		return
	}

	// Show confirmation dialog
	dialog.ShowConfirm("Move Message",
		fmt.Sprintf("Move message '%s' to folder '%s'?", mw.selectedMessage.Message.Subject, targetFolder),
		func(confirmed bool) {
			if confirmed {
				mw.performMoveToFolder(targetFolder)
			}
		}, mw.window)
}

// performMoveToFolder performs the actual move operation
func (mw *MainWindow) performMoveToFolder(targetFolder string) {
	mw.statusBar.SetText(fmt.Sprintf("Moving message to folder '%s'...", targetFolder))

	// Capture selectedMessage before spawning goroutine to avoid data race
	mw.messagesMu.RLock()
	msg := mw.selectedMessage
	mw.messagesMu.RUnlock()
	if msg == nil {
		return
	}

	go func() {
		// Use the MessageIndexItem's MoveTo method - much simpler!
		err := msg.MoveTo(targetFolder)
		if err != nil {
			fyne.Do(func() {
				mw.statusBar.SetText(fmt.Sprintf("Failed to move message: %v", err))
				dialog.ShowError(fmt.Errorf("Failed to move message: %v", err), mw.window)
			})
			mw.logger.Error("Failed to move message %d to folder %s: %v", msg.Message.UID, targetFolder, err)
			return
		}

		mw.logger.Info("Successfully moved message %d to folder %s", msg.Message.UID, targetFolder)

		// Clear the selected message since it's no longer in the current folder
		fyne.Do(func() {
			mw.selectedMessage = nil
			if mw.messageList != nil {
				mw.refreshMessageList() // Update highlighting
			}
		})

		// Use RefreshCoordinator to refresh both source and target folders
		sourceFolder := mw.folderController.GetCurrentFolder()
		mw.refreshMessagesDebounced(sourceFolder, func(err error) {
			if err != nil {
				mw.logger.Error("Failed to refresh source folder after move: %v", err)
				fyne.Do(func() {
					mw.statusBar.SetText(fmt.Sprintf("Message moved to '%s', but refresh failed: %v", targetFolder, err))
				})
			} else {
				// Also refresh target folder if it's different
				if targetFolder != sourceFolder {
					mw.refreshMessagesDebounced(targetFolder, func(err error) {
						if err != nil {
							mw.logger.Error("Failed to refresh target folder after move: %v", err)
						}
					})
				}
				fyne.Do(func() {
					mw.statusBar.SetText(fmt.Sprintf("Message moved to '%s'", targetFolder))
				})
			}
		})
	}()
}

// refreshFolders refreshes the folder list using debounced refresh
func (mw *MainWindow) refreshFolders() {
	if mw.accountController.GetCurrentAccount() != nil && mw.imapClient != nil {
		// Use debounced refresh for folder refresh
		mw.refreshFoldersDebounced(true, func(err error) {
			if err != nil {
				mw.logger.Error("Failed to refresh folders: %v", err)
				fyne.Do(func() {
					mw.statusBar.SetText(fmt.Sprintf("Failed to refresh folders: %v", err))
				})
			} else {
				fyne.Do(func() {
					mw.statusBar.SetText("Folders refreshed successfully")
				})
			}
		})
	}
}

// reloadFolders reloads the folder list without invalidating cache (for use after folder creation)
func (mw *MainWindow) reloadFolders() {
	if mw.accountController.GetCurrentAccount() != nil && mw.imapClient != nil {
		go func() {
			// Load folders from current cache/server state
			folders, err := mw.imapClient.ListFolders()
			if err != nil {
				mw.logger.Warn("Failed to reload folders: %v", err)
				return
			}

			// Update UI with new folder list
			fyne.Do(func() {
				sortedFolders := mw.sortFolders(folders)
				mw.folderController.SetFolders(sortedFolders)
				if folderList := mw.folderController.GetFolderList(); folderList != nil {
					folderList.Refresh()
				}
				mw.statusBar.SetText(fmt.Sprintf("Reloaded %d folders", len(folders)))
			})
		}()
	}
}

// refreshAll refreshes both folders and messages using RefreshCoordinator
func (mw *MainWindow) refreshAll() {
	mw.logger.Info("Manual refresh requested")

	// Handle unified inbox refresh
	if mw.accountController.IsUnifiedInbox() {
		mw.logger.Info("Refreshing unified inbox")
		fyne.Do(func() {
			mw.statusBar.SetText("Refreshing unified inbox...")
		})
		// Invalidate cache to ensure fresh data is loaded
		mw.invalidateUnifiedInboxCache()
		mw.backgroundWg.Add(1)
		go func() {
			defer mw.backgroundWg.Done()
			mw.fetchFreshUnifiedInboxMessagesInBackground()
		}()
		return
	}

	// Use debounced refresh for complete refresh (folders + messages)
	mw.refreshFoldersDebounced(true, func(err error) {
		if err != nil {
			mw.logger.Error("Failed to refresh folders: %v", err)
			fyne.Do(func() {
				mw.statusBar.SetText(fmt.Sprintf("Folder refresh failed: %v", err))
			})
		} else {
			// After folders are refreshed, refresh messages for current folder
			currentFolder := mw.folderController.GetCurrentFolder()
			if currentFolder != "" {
				mw.refreshMessagesDebounced(currentFolder, func(err error) {
					if err != nil {
						mw.logger.Error("Failed to refresh messages: %v", err)
						fyne.Do(func() {
							mw.statusBar.SetText(fmt.Sprintf("Message refresh failed: %v", err))
						})
					} else {
						fyne.Do(func() {
							mw.statusBar.SetText("Complete refresh successful")
						})
					}
				})
			} else {
				fyne.Do(func() {
					mw.statusBar.SetText("Folders refreshed successfully")
				})
			}
		}
	})
}

// clearCache clears all cached data on disk and in memory, then reloads from server using RefreshCoordinator
func (mw *MainWindow) clearCache() {
	mw.logger.Info("Clear cache requested - clearing all cached data and reloading from server")

	// Show confirmation dialog
	dialog.ShowConfirm("Clear Cache",
		"This will clear all cached data and reload everything from the server. This may take a moment. Continue?",
		func(confirmed bool) {
			if !confirmed {
				fyne.Do(func() {
					mw.statusBar.SetText("Cache clear cancelled")
				})
				return
			}

			// Perform cache clear directly
			go func() {
				mw.performCacheClear()
				fyne.Do(func() {
					mw.statusBar.SetText("Cache cleared and data reloaded successfully")
				})
			}()
		}, mw.window)
}

// performCacheClear performs the actual cache clearing and data reloading
func (mw *MainWindow) performCacheClear() {
	mw.logger.Info("Starting cache clear operation")

	// Step 1: Clear disk cache
	if mw.cache != nil {
		mw.logger.Info("Clearing main disk cache")
		if err := mw.cache.Clear(); err != nil {
			mw.logger.Error("Failed to clear main disk cache: %v", err)
			fyne.Do(func() {
				mw.statusBar.SetText(fmt.Sprintf("Failed to clear cache: %v", err))
			})
			return
		}
		mw.logger.Info("Main disk cache cleared successfully")
	}

	// Step 1.5: Clear attachment cache
	if mw.attachmentManager != nil {
		mw.logger.Info("Clearing attachment cache")
		if err := mw.attachmentManager.ClearCache(); err != nil {
			mw.logger.Error("Failed to clear attachment cache: %v", err)
			fyne.Do(func() {
				mw.statusBar.SetText(fmt.Sprintf("Failed to clear attachment cache: %v", err))
			})
			return
		}
		mw.logger.Info("Attachment cache cleared successfully")
	}

	// Step 2: Clear in-memory data
	mw.logger.Info("Clearing in-memory data")
	fyne.Do(func() {
		mw.clearInMemoryData()
		mw.statusBar.SetText("Cache cleared - reconnecting to accounts...")
	})

	// Step 3: Reconnect to current account and reload data
	if mw.accountController.GetCurrentAccount() != nil {
		mw.logger.Info("Reconnecting to current account: %s", mw.accountController.GetCurrentAccount().Name)

		// Disconnect current IMAP client
		if mw.imapClient != nil {
			mw.imapClient.Disconnect()
		}

		// Clear account clients for unified inbox
		mw.accountController.CloseAllClients()

		// Reconnect to current account
		fyne.Do(func() {
			mw.selectAccount(mw.accountController.GetCurrentAccount())
		})
	} else if mw.accountController.IsUnifiedInbox() {
		// If unified inbox was selected, reload it
		mw.logger.Info("Reloading unified inbox after cache clear")
		fyne.Do(func() {
			mw.selectUnifiedInboxWithOptions(true)
		})
	} else {
		// No account selected, just update status
		fyne.Do(func() {
			mw.statusBar.SetText("Cache cleared - please select an account")
		})
	}
}

// clearInMemoryData clears all in-memory cached data
func (mw *MainWindow) clearInMemoryData() {
	mw.logger.Debug("Clearing in-memory data structures")

	// Clear message data
	mw.messagesMu.Lock()
	mw.messages = []email.MessageIndexItem{}
	mw.selectedMessage = nil
	mw.messagesMu.Unlock()

	// Clear folder data
	mw.folderController.SetFolders([]email.Folder{})
	mw.folderController.SelectFolder("") // Clear current folder

	// Reset unified inbox state
	mw.accountController.SetUnifiedInbox(false)

	// Reset auto-selection state
	mw.autoSelectionDone = false

	// Cancel any active read timer
	mw.cancelReadTimer()

	// Clear UI components
	mw.messageList.UnselectAll()
	if folderList := mw.folderController.GetFolderList(); folderList != nil {
		folderList.UnselectAll()
	}
	mw.accountList.UnselectAll()

	// Refresh UI to show cleared state
	mw.messageList.UnselectAll()
	mw.refreshMessageList()
	if folderList := mw.folderController.GetFolderList(); folderList != nil {
		folderList.Refresh()
	}
	mw.accountList.Refresh()
	mw.updateMessageViewer("")

	mw.logger.Debug("In-memory data cleared successfully")
}

// loadCachedFoldersDirectly loads folders directly from cache without going through the worker
// This provides instant UI responsiveness when switching accounts
func (mw *MainWindow) loadCachedFoldersDirectly(accountName string) ([]email.Folder, bool) {
	if mw.cache == nil {
		return nil, false
	}

	for _, accountKey := range accountCacheKeyCandidates(accountName) {
		cacheKey := fmt.Sprintf("%s:folders:subscribed", accountKey)
		data, found, err := mw.cache.Get(cacheKey)
		if err != nil {
			mw.logger.Debug("Failed loading cached folders for account %s from key %s: %v", accountName, cacheKey, err)
			continue
		}
		if !found {
			continue
		}

		var folders []email.Folder
		if err := json.Unmarshal(data, &folders); err != nil {
			mw.logger.Warn("Failed to unmarshal cached folders for account %s from key %s: %v", accountName, cacheKey, err)
			continue
		}

		mw.logger.Debug("Loaded %d folders directly from cache for account %s using key %s", len(folders), accountName, cacheKey)
		return folders, true
	}

	mw.logger.Debug("No cached folders found for account %s", accountName)
	return nil, false
}

// loadCachedMessagesDirectly loads messages directly from cache without going through the worker
// This provides instant UI responsiveness when switching folders
func (mw *MainWindow) loadCachedMessagesDirectly(accountName, folder string) ([]email.Message, bool) {
	if mw.cache == nil {
		return nil, false
	}

	for _, accountKey := range accountCacheKeyCandidates(accountName) {
		cacheKey := fmt.Sprintf("%s:messages:%s", accountKey, folder)
		data, found, err := mw.cache.Get(cacheKey)
		if err != nil {
			mw.logger.Debug("Failed loading cached messages for account %s, folder %s from key %s: %v", accountName, folder, cacheKey, err)
			continue
		}
		if !found {
			continue
		}

		var messages []email.Message
		if err := json.Unmarshal(data, &messages); err != nil {
			mw.logger.Warn("Failed to unmarshal cached messages for account %s, folder %s from key %s: %v", accountName, folder, cacheKey, err)
			continue
		}

		mw.logger.Debug("Loaded %d messages directly from cache for account %s, folder %s using key %s", len(messages), accountName, folder, cacheKey)
		return messages, true
	}

	mw.logger.Debug("No cached messages found for account %s, folder %s", accountName, folder)
	return nil, false
}

// preloadAccountCaches preloads folder and message caches for all accounts in background
// This improves responsiveness when switching between accounts
func (mw *MainWindow) preloadAccountCaches() {
	accounts := mw.config.GetAccounts()
	if len(accounts) == 0 {
		return
	}

	go func() {
		mw.logger.Debug("Preloading caches for %d accounts in background", len(accounts))

		for _, account := range accounts {
			// Check if we have cached folders for this account
			cachedFolders, cachedFoldersFound := mw.loadCachedFoldersDirectly(account.Name)
			if cachedFoldersFound {
				mw.logger.Debug("Account %s has %d cached folders", account.Name, len(cachedFolders))

				// Check if we have cached messages for INBOX
				cachedMessages, cachedMessagesFound := mw.loadCachedMessagesDirectly(account.Name, "INBOX")
				if cachedMessagesFound {
					mw.logger.Debug("Account %s has %d cached INBOX messages", account.Name, len(cachedMessages))
				}
			}
		}

		mw.logger.Debug("Cache preload check completed")
	}()
}

func (mw *MainWindow) toggleHTMLView() {
	// Delegate to MessageViewController
	mw.messageViewController.ToggleHTMLView()
	// Note: The callback set in setupUI will handle refreshing the display
}

func (mw *MainWindow) showSettings() {
	opts := SettingsOptions{
		AddressbookMgr: mw.addressbookMgr,
		OnSaved: func(updatedConfig *config.Config) {
			// Convert back to ConfigManager and save
			mw.config.SetAccounts(updatedConfig.Accounts)
			mw.config.SetUI(updatedConfig.UI)
			mw.config.SetCache(updatedConfig.Cache)
			mw.config.SetLogging(updatedConfig.Logging)
			mw.config.SetAddressbook(updatedConfig.Addressbook)

			// Apply theme changes immediately
			applyTheme(mw.app, updatedConfig.UI.Theme, mw.logger)

			// Update notification manager settings
			if mw.notificationMgr != nil {
				mw.notificationMgr.SetEnabled(updatedConfig.UI.Notifications.Enabled)
				mw.logger.Debug("Updated notification manager: enabled=%v", updatedConfig.UI.Notifications.Enabled)
			}

			// Save configuration
			err := mw.config.Save()
			if err != nil {
				mw.logger.Error("Failed to save configuration to preferences: %v", err)
				fyne.Do(func() {
					mw.statusBar.SetText(fmt.Sprintf("Settings saved to memory but failed to persist: %v", err))
				})
				return
			}

			// Use RefreshCoordinator for account refresh after settings changes
			mw.refreshAccountsDebounced(func(err error) {
				if err != nil {
					mw.logger.Error("Failed to refresh accounts after settings save: %v", err)
					fyne.Do(func() {
						mw.statusBar.SetText(fmt.Sprintf("Settings saved, but account refresh failed: %v", err))
					})
				} else {
					fyne.Do(func() {
						mw.statusBar.SetText("Settings saved successfully")
						// Refresh UI with new settings
						uiConfig := mw.config.GetUI()
						mw.window.Resize(fyne.NewSize(float32(uiConfig.WindowSize.Width), float32(uiConfig.WindowSize.Height)))

						// Update default message view preference
						showHTML := uiConfig.DefaultMessageView != "text"
						if showHTML != mw.messageViewController.IsShowingHTML() {
							mw.messageViewController.ToggleHTMLView()
						}

						// Refresh current message display if one is selected
						if mw.selectedMessage != nil {
							mw.displayMessage(&mw.selectedMessage.Message)
						}
					})
				}
			})
		},
		OnClosed: func() {
			mw.statusBar.SetText("Settings dialog closed")
		},
		OnClearCache: func() {
			mw.clearCache()
		},
		OnFolderManagement: func(account *config.Account) {
			mw.showFolderSubscriptionsForAccount(account)
		},
	}

	settingsWindow := NewSettingsWindow(mw.app, mw.configManagerToConfig(), opts)
	settingsWindow.Show()
}

// setupNewAccountAutomatically handles the complete setup process for a newly added account
// including connection, folder discovery, default folder subscription, and initial message retrieval
func (mw *MainWindow) setupNewAccountAutomatically(account *config.Account) {
	mw.logger.Info("Starting automatic setup for new account: %s (%s)", account.Name, account.Email)

	// Create progress dialog
	progressBar := widget.NewProgressBar()
	progressBar.SetValue(0)

	statusLabel := widget.NewLabel("Initializing account setup...")

	progressContent := container.NewVBox(
		widget.NewLabel(fmt.Sprintf("Setting up account: %s", account.Name)),
		widget.NewSeparator(),
		statusLabel,
		progressBar,
	)

	progressDialog := dialog.NewCustom("Account Setup", "Cancel", progressContent, mw.window)
	progressDialog.Resize(fyne.NewSize(400, 150))

	// Track if setup was cancelled
	cancelled := false

	// Handle cancel button
	progressDialog.SetOnClosed(func() {
		cancelled = true
		mw.logger.Info("Account setup cancelled by user for: %s", account.Name)
		mw.statusBar.SetText("Account setup cancelled")
	})

	progressDialog.Show()

	go func() {
		defer func() {
			// Close progress dialog when done
			fyne.Do(func() {
				if progressDialog != nil {
					progressDialog.Hide()
				}
			})
		}()

		// Step 1: Create and connect IMAP client (25% progress)
		if cancelled {
			return
		}

		fyne.Do(func() {
			statusLabel.SetText("Connecting to email server...")
			progressBar.SetValue(0.25)
			mw.statusBar.SetText(fmt.Sprintf("Connecting to %s...", account.Name))
		})

		// Retry connection up to 3 times
		var imapClient *imap.ClientWrapper
		var err error
		maxRetries := 3

		for attempt := 1; attempt <= maxRetries; attempt++ {
			if cancelled {
				return
			}

			if attempt > 1 {
				mw.logger.Info("Retrying connection to %s (attempt %d/%d)", account.Name, attempt, maxRetries)
				fyne.Do(func() {
					statusLabel.SetText(fmt.Sprintf("Retrying connection (attempt %d/%d)...", attempt, maxRetries))
				})
				// Brief delay before retry
				time.Sleep(time.Second * 2)
			}

			imapClient, err = mw.getOrCreateIMAPClient(account)
			if err == nil {
				break // Success!
			}

			mw.logger.Warn("Connection attempt %d failed for account %s: %v", attempt, account.Name, err)

			if attempt == maxRetries {
				// Final attempt failed
				mw.logger.Error("Failed to connect to account %s after %d attempts: %v", account.Name, maxRetries, err)
				fyne.Do(func() {
					mw.statusBar.SetText(fmt.Sprintf("Failed to connect to %s", account.Name))

					// Show detailed error dialog with suggestions
					errorMsg := fmt.Sprintf("Failed to connect to account '%s' after %d attempts.\n\nError: %v\n\nPlease check:\n• Your internet connection\n• Server settings (host, port, encryption)\n• Username and password\n• Firewall settings",
						account.Name, maxRetries, err)

					dialog.ShowError(fmt.Errorf("%s", errorMsg), mw.window)
				})
				return
			}
		}

		// Step 2: Discover and subscribe to default folders (50% progress)
		if cancelled {
			return
		}

		fyne.Do(func() {
			statusLabel.SetText("Discovering and setting up folders...")
			progressBar.SetValue(0.50)
			mw.statusBar.SetText(fmt.Sprintf("Setting up folders for %s...", account.Name))
		})

		// Try to subscribe to default folders with error handling
		err = imapClient.SubscribeToDefaultFolders(account.SentFolder, account.TrashFolder)
		if err != nil {
			mw.logger.Warn("Failed to subscribe to default folders for account %s: %v", account.Name, err)

			// Try to at least subscribe to INBOX manually as a fallback
			if subscribeErr := imapClient.SubscribeFolder("INBOX"); subscribeErr != nil {
				mw.logger.Error("Failed to subscribe to INBOX for account %s: %v", account.Name, subscribeErr)
				fyne.Do(func() {
					statusLabel.SetText("Warning: Folder setup incomplete")
				})
			} else {
				mw.logger.Info("Successfully subscribed to INBOX for account %s (other folders may need manual setup)", account.Name)
				fyne.Do(func() {
					statusLabel.SetText("Basic folder setup completed")
				})
			}
		} else {
			mw.logger.Info("Successfully subscribed to default folders for account %s", account.Name)
		}

		// Step 3: Retrieve initial messages from INBOX (75% progress)
		if cancelled {
			return
		}

		fyne.Do(func() {
			statusLabel.SetText("Loading recent messages...")
			progressBar.SetValue(0.75)
			mw.statusBar.SetText(fmt.Sprintf("Loading messages for %s...", account.Name))
		})

		// Try to fetch messages with fallback to cached messages
		messages, err := imapClient.FetchFreshMessages("INBOX", 0) // No limit - fetch all messages for initial load
		if err != nil {
			mw.logger.Warn("Failed to fetch fresh messages for account %s: %v", account.Name, err)

			// Try to fall back to cached messages
			cachedMessages, cacheErr := imapClient.FetchMessages("INBOX", 0)
			if cacheErr != nil {
				mw.logger.Error("Failed to fetch cached messages for account %s: %v", account.Name, cacheErr)
				fyne.Do(func() {
					statusLabel.SetText("Warning: Could not load messages")
				})
			} else {
				mw.logger.Info("Loaded %d cached messages from INBOX for account %s", len(cachedMessages), account.Name)
				messages = cachedMessages
				fyne.Do(func() {
					statusLabel.SetText("Loaded cached messages")
				})
			}
		} else {
			mw.logger.Info("Successfully loaded %d fresh messages from INBOX for account %s", len(messages), account.Name)
		}

		// Step 4: Update UI and refresh unified inbox if needed (100% progress)
		if cancelled {
			return
		}

		fyne.Do(func() {
			statusLabel.SetText("Finalizing setup...")
			progressBar.SetValue(1.0)

			// Refresh the account list to show the new account
			mw.accountList.Refresh()

			// If unified inbox is currently selected, refresh it to include the new account
			if mw.accountController.IsUnifiedInbox() {
				mw.logger.Info("Refreshing unified inbox to include new account %s", account.Name)
				mw.statusBar.SetText("Refreshing unified inbox...")
				// Invalidate cache to ensure fresh data is loaded
				mw.invalidateUnifiedInboxCache()
				go mw.loadUnifiedInboxMessages(true)
			} else {
				// Select the new account to show its folders and messages
				mw.logger.Info("Selecting new account %s", account.Name)
				mw.selectAccount(account)
			}

			// Show final success message
			mw.statusBar.SetText(fmt.Sprintf("Account %s setup completed successfully", account.Name))
		})

		mw.logger.Info("Automatic setup completed for account: %s", account.Name)
	}()
}

func (mw *MainWindow) showAddAccountWizard() {
	// Get current accounts to pass to wizard
	currentAccounts := mw.config.GetAccounts()

	// Create a temporary config for the wizard
	tempConfig := &config.Config{
		Accounts: currentAccounts,
		UI:       mw.config.GetUI(),
		Cache:    mw.config.GetCache(),
		Logging:  mw.config.GetLogging(),
	}

	// Create and show the new account wizard
	wizard := NewNewAccountWizard(mw.app, tempConfig, AddAccountMode)
	wizard.SetOnComplete(func(updatedConfig *config.Config) {
		if updatedConfig != nil {
			// Extract the new accounts from the wizard result
			newAccounts := updatedConfig.Accounts

			// Update our config manager with the new accounts
			mw.config.SetAccounts(newAccounts)

			// Save the updated configuration
			if err := mw.config.Save(); err != nil {
				dialog.ShowError(fmt.Errorf("failed to save configuration: %v", err), mw.window)
				return
			}

			// Find the newly added account (should be the last one)
			if len(newAccounts) > len(currentAccounts) {
				newAccount := &newAccounts[len(newAccounts)-1]
				mw.logger.Info("New account added: %s (%s)", newAccount.Name, newAccount.Email)

				// Automatically setup the new account
				mw.setupNewAccountAutomatically(newAccount)
			} else {
				// Fallback to old behavior if we can't identify the new account
				mw.accountList.Refresh()
				mw.statusBar.SetText("New account added successfully")

				if mw.accountController.IsUnifiedInbox() {
					mw.invalidateUnifiedInboxCache()
					go mw.loadUnifiedInboxMessages(true)
				}
			}
		}
	})

	wizard.Show()
}

func (mw *MainWindow) showSearch() {
	if mw.accountController.GetCurrentAccount() == nil {
		dialog.ShowInformation("No Account", "Please select an account first", mw.window)
		return
	}

	opts := SearchOptions{
		Account:    mw.accountController.GetCurrentAccount(),
		IMAPClient: mw.imapClient,
		OnMessageSelected: func(msg *email.Message) {
			// Create a MessageIndexItem for the selected message
			// We need to determine which folder this message came from
			// For now, we'll use the current folder, but ideally search results
			// should include folder information
			folderName := mw.folderController.GetCurrentFolder()
			if folderName == "" {
				folderName = "INBOX" // Default fallback
			}

			indexItem := &email.MessageIndexItem{
				Message:      *msg,
				AccountName:  mw.accountController.GetCurrentAccount().Name,
				AccountEmail: mw.accountController.GetCurrentAccount().Email,
				FolderName:   folderName,
				IMAPClient:   mw.imapClient,
				SMTPClient:   mw.smtpClient,
				AccountConfig: &email.AccountConfig{
					Name:        mw.accountController.GetCurrentAccount().Name,
					Email:       mw.accountController.GetCurrentAccount().Email,
					DisplayName: mw.accountController.GetCurrentAccount().DisplayName,
					IMAP: email.ServerConfig{
						Host:     mw.accountController.GetCurrentAccount().IMAP.Host,
						Port:     mw.accountController.GetCurrentAccount().IMAP.Port,
						Username: mw.accountController.GetCurrentAccount().IMAP.Username,
						Password: mw.accountController.GetCurrentAccount().IMAP.Password,
						TLS:      mw.accountController.GetCurrentAccount().IMAP.TLS,
					},
					SMTP: email.ServerConfig{
						Host:     mw.accountController.GetCurrentAccount().SMTP.Host,
						Port:     mw.accountController.GetCurrentAccount().SMTP.Port,
						Username: mw.accountController.GetCurrentAccount().SMTP.Username,
						Password: mw.accountController.GetCurrentAccount().SMTP.Password,
						TLS:      mw.accountController.GetCurrentAccount().SMTP.TLS,
					},
				},
			}

			// Set as selected message and fetch full content
			mw.selectedMessage = indexItem

			// Show loading message while fetching full content
			loadingContent := fmt.Sprintf("# %s\n\n> **From:** %s\n> **Date:** %s\n\n---\n\n**Loading message content...**",
				msg.Subject, mw.formatAddresses(msg.From), mw.getDisplayDate(msg).Format("January 2, 2006 at 3:04 PM"))
			mw.updateMessageViewer(loadingContent)

			// Fetch and display the full message content
			go mw.fetchAndDisplayMessage(indexItem)

			mw.statusBar.SetText("Message selected from search results")
		},
		OnClosed: func() {
			mw.statusBar.SetText("Search window closed")
		},
	}

	searchWindow := NewSearchWindow(mw.app, mw.configManagerToConfig(), opts)
	searchWindow.Show()
}

// cacheAttachmentIfNeeded caches an attachment if it's not already cached and returns the attachment ID
func (mw *MainWindow) cacheAttachmentIfNeeded(msg *email.Message, attachment email.Attachment) string {
	if mw.attachmentManager == nil {
		return ""
	}

	// Generate attachment ID
	attachmentID := mw.attachmentManager.GenerateAttachmentID(msg.ID, attachment.Filename)

	// Check if already cached
	if mw.attachmentManager.IsAttachmentCached(attachmentID) {
		return attachmentID
	}

	// Cache the attachment
	_, err := mw.attachmentManager.CacheAttachment(msg.ID, attachment)
	if err != nil {
		mw.statusBar.SetText(fmt.Sprintf("Failed to cache attachment: %s", err.Error()))
		return ""
	}

	return attachmentID
}

// createInlineAttachmentDisplay creates markdown content for displaying an attachment inline
func (mw *MainWindow) createInlineAttachmentDisplay(attachment email.Attachment, attachmentID string, index int) string {
	var content strings.Builder

	// Attachment header with icon
	icon := mw.getAttachmentIcon(attachment.ContentType)
	content.WriteString(fmt.Sprintf("### %s %s\n", icon, attachment.Filename))
	content.WriteString(fmt.Sprintf("**Type:** %s | **Size:** %s\n\n",
		attachment.ContentType, formatFileSize(attachment.Size)))

	// Preview content for supported types
	if mw.attachmentManager != nil && attachmentID != "" {
		previewContent := mw.generateAttachmentPreview(attachment, attachmentID)
		if previewContent != "" {
			content.WriteString(previewContent)
		}
	}

	// Status information
	if mw.isPreviewableType(attachment.ContentType) {
		content.WriteString("*This attachment can be previewed and saved using the toolbar or context menu*\n\n")
	} else {
		content.WriteString("*This attachment can be saved using the toolbar or context menu*\n\n")
	}

	return content.String()
}

// generateAttachmentPreview generates preview content for supported attachment types
func (mw *MainWindow) generateAttachmentPreview(attachment email.Attachment, attachmentID string) string {
	if mw.attachmentManager == nil || attachmentID == "" {
		return ""
	}

	// Get preview data
	previewData, err := mw.attachmentManager.GetAttachmentPreview(attachmentID, 500) // 500 chars max
	if err != nil {
		return ""
	}

	var content strings.Builder

	// Handle different content types
	if strings.HasPrefix(attachment.ContentType, "text/") {
		content.WriteString("**Preview:**\n```\n")
		preview := string(previewData)
		if len(preview) > 500 {
			preview = preview[:500] + "..."
		}
		content.WriteString(preview)
		content.WriteString("\n```\n\n")
	} else if strings.HasPrefix(attachment.ContentType, "image/") {
		content.WriteString("**Image Preview:** *Image preview available - click View Full Size to see the image*\n\n")
	} else {
		content.WriteString("**Binary Content:** *Preview not available for this file type*\n\n")
	}

	return content.String()
}

// getAttachmentIcon returns an emoji icon for the attachment content type
func (mw *MainWindow) getAttachmentIcon(contentType string) string {
	switch {
	case strings.HasPrefix(contentType, "image/"):
		return "🖼️"
	case strings.HasPrefix(contentType, "text/"):
		return "📄"
	case strings.HasPrefix(contentType, "video/"):
		return "🎥"
	case strings.HasPrefix(contentType, "audio/"):
		return "🎵"
	case strings.Contains(contentType, "pdf"):
		return "📕"
	case strings.Contains(contentType, "zip") || strings.Contains(contentType, "archive"):
		return "📦"
	case strings.Contains(contentType, "word") || strings.Contains(contentType, "document"):
		return "📝"
	case strings.Contains(contentType, "excel") || strings.Contains(contentType, "spreadsheet"):
		return "📊"
	case strings.Contains(contentType, "powerpoint") || strings.Contains(contentType, "presentation"):
		return "📈"
	default:
		return "📎"
	}
}

// isPreviewableType checks if an attachment type can be previewed
func (mw *MainWindow) isPreviewableType(contentType string) bool {
	previewableTypes := []string{
		"text/", "image/", "application/json", "application/xml", "application/pdf",
	}

	for _, previewable := range previewableTypes {
		if strings.HasPrefix(contentType, previewable) {
			return true
		}
	}
	return false
}

// saveAttachments allows the user to save all attachments from the current message
func (mw *MainWindow) saveAttachments() {
	if mw.selectedMessage == nil {
		dialog.ShowInformation("No Message", "Please select a message to save attachments", mw.window)
		return
	}

	if len(mw.selectedMessage.Message.Attachments) == 0 {
		dialog.ShowInformation("No Attachments", "The selected message has no attachments", mw.window)
		return
	}

	// If only one attachment, save it directly
	if len(mw.selectedMessage.Message.Attachments) == 1 {
		mw.saveAttachment(mw.selectedMessage.Message.Attachments[0])
		return
	}

	// Multiple attachments - show selection dialog
	attachmentNames := make([]string, len(mw.selectedMessage.Message.Attachments))
	for i, attachment := range mw.selectedMessage.Message.Attachments {
		attachmentNames[i] = fmt.Sprintf("%s (%s)", attachment.Filename, formatFileSize(attachment.Size))
	}

	// Create selection dialog
	attachmentList := widget.NewList(
		func() int { return len(attachmentNames) },
		func() fyne.CanvasObject {
			return container.NewHBox(
				widget.NewCheck("", nil),
				widget.NewLabel("Attachment name"),
			)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			hbox := obj.(*fyne.Container)
			check := hbox.Objects[0].(*widget.Check)
			label := hbox.Objects[1].(*widget.Label)

			label.SetText(attachmentNames[id])
			check.SetChecked(false) // Default unchecked
		},
	)

	// Track selected attachments
	selectedAttachments := make(map[int]bool)

	// Update selection tracking when items are tapped
	attachmentList.OnSelected = func(id widget.ListItemID) {
		selectedAttachments[id] = !selectedAttachments[id]
		attachmentList.Refresh()
	}

	// Add save button
	saveButton := widget.NewButton("Save Selected", func() {
		for id, selected := range selectedAttachments {
			if selected && id < len(mw.selectedMessage.Message.Attachments) {
				mw.saveAttachment(mw.selectedMessage.Message.Attachments[id])
			}
		}
	})

	saveAllButton := widget.NewButton("Save All", func() {
		for _, attachment := range mw.selectedMessage.Message.Attachments {
			mw.saveAttachment(attachment)
		}
	})

	buttons := container.NewHBox(saveButton, saveAllButton)
	content := container.NewVBox(attachmentList, buttons)

	// Create dialog with the complete content
	saveDialog := dialog.NewCustom("Save Attachments", "Cancel", content, mw.window)
	saveDialog.Resize(fyne.NewSize(400, 300))
	saveDialog.Show()
}

// saveAttachment saves a single attachment to disk
func (mw *MainWindow) saveAttachment(attachment email.Attachment) {
	// Create a file save dialog with the attachment filename pre-populated
	saveDialog := dialog.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
		if err != nil {
			dialog.ShowError(err, mw.window)
			return
		}
		if writer == nil {
			return
		}
		defer writer.Close()

		_, err = writer.Write(attachment.Data)
		if err != nil {
			dialog.ShowError(fmt.Errorf("failed to save attachment: %w", err), mw.window)
			return
		}

		mw.statusBar.SetText(fmt.Sprintf("Saved: %s", attachment.Filename))
	}, mw.window)

	// Sanitize the filename to prevent path traversal and other filesystem issues
	// Characters like / \ .. : * ? " < > | are replaced with _
	safeFilename := email.SanitizeFilename(attachment.Filename)
	saveDialog.SetFileName(safeFilename)
	saveDialog.Show()
}

// openAttachment saves an attachment to a temporary location and opens it with the system's default application
func (mw *MainWindow) openAttachment(attachment email.Attachment) {
	// Validate attachment safety before opening (defense-in-depth)
	// This prevents opening executable files and scripts that could be dangerous
	if err := email.ValidateAttachmentSafety(attachment.Filename); err != nil {
		dialog.ShowError(fmt.Errorf("cannot open attachment: %w", err), mw.window)
		mw.logger.Warn("Blocked attempt to open dangerous attachment: %s (%v)", attachment.Filename, err)
		return
	}

	// Create a temporary directory for the attachment
	tempDir := filepath.Join(os.TempDir(), "gommail-attachments")
	err := os.MkdirAll(tempDir, 0755)
	if err != nil {
		dialog.ShowError(fmt.Errorf("failed to create temporary directory: %w", err), mw.window)
		return
	}

	// Sanitize the filename to prevent path traversal
	safeFilename := email.SanitizeFilename(attachment.Filename)
	tempFilePath := filepath.Join(tempDir, safeFilename)

	// Write the attachment to the temporary file
	err = os.WriteFile(tempFilePath, attachment.Data, 0644)
	if err != nil {
		dialog.ShowError(fmt.Errorf("failed to save attachment to temporary file: %w", err), mw.window)
		return
	}

	// Open the file with the system's default application
	err = open.Start(tempFilePath)
	if err != nil {
		dialog.ShowError(fmt.Errorf("failed to open attachment: %w", err), mw.window)
		return
	}

	mw.statusBar.SetText(fmt.Sprintf("Opened: %s", attachment.Filename))
}

func (mw *MainWindow) showAttachments() {
	if mw.selectedMessage == nil {
		dialog.ShowInformation("No Message", "Please select a message to view attachments", mw.window)
		return
	}

	if len(mw.selectedMessage.Message.Attachments) == 0 {
		dialog.ShowInformation("No Attachments", "The selected message has no attachments", mw.window)
		return
	}

	// Show information that attachments are now displayed inline
	dialog.ShowInformation("Attachments",
		"Attachments are now displayed inline within the message. Scroll down to see attachment previews and save options.",
		mw.window)
}

// startReadTimer starts a 5-second timer to mark the message as read
func (mw *MainWindow) startReadTimer(msg *email.Message) {
	// Cancel any existing timer
	if mw.readTimer != nil {
		mw.readTimer.Stop()
		mw.readTimer = nil
	}

	// Check if message is already read
	isRead := false
	for _, flag := range msg.Flags {
		if flag == "\\Seen" {
			isRead = true
			break
		}
	}

	// Don't start timer for already read messages
	if isRead {
		mw.logger.Debug("Message %s (UID: %d) is already read, not starting timer", msg.Subject, msg.UID)
		return
	}

	mw.logger.Debug("Starting 5-second read timer for message: %s (UID: %d)", msg.Subject, msg.UID)

	// Start 5-second timer
	mw.readTimer = time.AfterFunc(5*time.Second, func() {
		mw.logger.Debug("5-second timer expired, marking message as read: %s (UID: %d)", msg.Subject, msg.UID)

		// Mark message as read on server and update UI
		err := mw.markMessageAsRead(msg)
		if err != nil {
			mw.logger.Error("Failed to mark message as read: %v", err)
			fyne.Do(func() {
				mw.statusBar.SetText("Message marked as read locally (server unavailable)")
			})
		} else {
			mw.logger.Debug("Successfully marked message as read: %s (UID: %d)", msg.Subject, msg.UID)
			fyne.Do(func() {
				mw.statusBar.SetText("Message marked as read")
			})
		}

		// Clear timer state
		mw.readTimer = nil
	})
}

// cancelReadTimer cancels the current read timer if one is active
func (mw *MainWindow) cancelReadTimer() {
	if mw.readTimer != nil {
		mw.logger.Debug("Cancelling read timer")
		mw.readTimer.Stop()
		mw.readTimer = nil
	}
}

// markMessageAsRead marks a message as read both on the server and in the local UI
func (mw *MainWindow) markMessageAsRead(msg *email.Message) error {
	var imapClient *imap.ClientWrapper
	var folder string

	if mw.accountController.IsUnifiedInbox() {
		// For unified inbox, get the IMAP client for the message's account
		account, _, err := mw.getAccountForMessage(msg)
		if err != nil {
			return fmt.Errorf("cannot get account for message: %v", err)
		}

		// Get IMAP client for this account
		client, err := mw.getOrCreateIMAPClient(account)
		if err != nil {
			return fmt.Errorf("cannot connect to account %s: %v", account.Name, err)
		}

		imapClient = client
		folder = "INBOX" // Unified inbox only shows INBOX messages
	} else {
		// Regular mode
		currentFolder := mw.folderController.GetCurrentFolder()
		if mw.imapClient == nil || currentFolder == "" {
			return fmt.Errorf("no IMAP client or folder selected")
		}
		imapClient = mw.imapClient
		folder = currentFolder
	}

	// Try to mark as read on the server with retry logic
	var err error
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		err = imapClient.MarkAsRead(folder, msg.UID)
		if err == nil {
			break // Success
		}

		// If not connected, try to reconnect
		if strings.Contains(err.Error(), "not connected") && i < maxRetries-1 {
			mw.logger.Debug("IMAP not connected, attempting to reconnect (attempt %d/%d)", i+1, maxRetries)

			// Try to reconnect
			if connectErr := imapClient.Connect(); connectErr != nil {
				mw.logger.Warn("Failed to reconnect IMAP client: %v", connectErr)
				time.Sleep(time.Second) // Brief delay before retry
				continue
			}

			// Retry selecting the folder
			if selectErr := imapClient.SelectFolder(folder); selectErr != nil {
				mw.logger.Warn("Failed to reselect folder %s: %v", folder, selectErr)
				time.Sleep(time.Second) // Brief delay before retry
				continue
			}

			mw.logger.Debug("Successfully reconnected and reselected folder")
		} else {
			// For other errors or final retry, just log and continue
			mw.logger.Warn("Attempt %d/%d to mark message as read failed: %v", i+1, maxRetries, err)
			if i < maxRetries-1 {
				time.Sleep(time.Second) // Brief delay before retry
			}
		}
	}

	// If all retries failed, update local state anyway and log the issue
	if err != nil {
		mw.logger.Warn("Failed to mark message as read on server after %d attempts: %v. Updating local state only.", maxRetries, err)
		// Don't return error - we'll still update local state
	} else {
		mw.logger.Debug("Successfully marked message as read on server: %s (UID: %d)", msg.Subject, msg.UID)
	}

	// Update local message flags and cache
	fyne.Do(func() {
		var updatedMessage *email.Message

		// Find and update the message in our local list
		for i := range mw.messages {
			if mw.messages[i].Message.UID == msg.UID {
				// Add \Seen flag if not already present
				hasSeenFlag := false
				for _, flag := range mw.messages[i].Message.Flags {
					if flag == "\\Seen" {
						hasSeenFlag = true
						break
					}
				}

				if !hasSeenFlag {
					mw.messages[i].Message.Flags = append(mw.messages[i].Message.Flags, "\\Seen")
					mw.logger.Debug("Added \\Seen flag to local message: %s (UID: %d)", msg.Subject, msg.UID)
					updatedMessage = &mw.messages[i].Message
				}
				break
			}
		}

		// Update the selected message if it's the same one
		if mw.selectedMessage != nil && mw.selectedMessage.Message.UID == msg.UID {
			hasSeenFlag := false
			for _, flag := range mw.selectedMessage.Message.Flags {
				if flag == "\\Seen" {
					hasSeenFlag = true
					break
				}
			}

			if !hasSeenFlag {
				mw.selectedMessage.Message.Flags = append(mw.selectedMessage.Message.Flags, "\\Seen")
				if updatedMessage == nil {
					updatedMessage = &mw.selectedMessage.Message
				}
			}
		}

		// Update cache with the new read status
		if updatedMessage != nil {
			go mw.updateMessageInCache(updatedMessage, folder)
		}

		// Refresh the message list to update the bold text styling
		mw.refreshMessageList()
	})

	return nil
}

// updateMessageInCache updates a specific message in the cache with new flag status
func (mw *MainWindow) updateMessageInCache(updatedMessage *email.Message, folder string) {
	if mw.cache == nil {
		mw.logger.Debug("No cache available for updating message")
		return
	}

	// Determine which account this message belongs to
	var accountName string
	for _, msg := range mw.messages {
		if msg.Message.UID == updatedMessage.UID {
			accountName = msg.AccountName
			break
		}
	}

	if accountName == "" {
		mw.logger.Warn("Could not determine account for message UID %d", updatedMessage.UID)
		return
	}

	// Update cache for both the account-specific cache and the legacy cache format
	accountKey := fmt.Sprintf("account_%s", accountName)
	cacheKeys := []string{
		fmt.Sprintf("%s:messages:%s", accountKey, folder),
		fmt.Sprintf("%s:messages:%s", accountName, folder), // Legacy format
	}

	for _, cacheKey := range cacheKeys {
		if cachedData, found, err := mw.cache.Get(cacheKey); err == nil && found {
			var messages []email.Message
			if err := json.Unmarshal(cachedData, &messages); err == nil {
				// Find and update the message in cache
				updated := false
				for i := range messages {
					if messages[i].UID == updatedMessage.UID {
						messages[i].Flags = updatedMessage.Flags
						updated = true
						mw.logger.Debug("Updated message UID %d flags in cache key %s", updatedMessage.UID, cacheKey)
						break
					}
				}

				if updated {
					// Save updated messages back to cache
					if data, err := json.Marshal(messages); err == nil {
						if err := mw.cache.Set(cacheKey, data, 6*time.Hour); err != nil {
							mw.logger.Warn("Failed to update cache key %s: %v", cacheKey, err)
						} else {
							mw.logger.Debug("Successfully updated cache key %s with new message flags", cacheKey)
						}
					} else {
						mw.logger.Warn("Failed to marshal updated messages for cache key %s: %v", cacheKey, err)
					}
				}
			} else {
				mw.logger.Warn("Failed to unmarshal cached messages for key %s: %v", cacheKey, err)
			}
		}
	}
}

// registerKeyboardShortcuts registers keyboard shortcuts with the window canvas
func (mw *MainWindow) registerKeyboardShortcuts() {
	canvas := mw.window.Canvas()

	// File shortcuts
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyN, Modifier: fyne.KeyModifierControl}, func(shortcut fyne.Shortcut) {
		mw.composeMessage()
	})
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyF5}, func(shortcut fyne.Shortcut) {
		mw.refreshAll()
	})
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyF5, Modifier: fyne.KeyModifierControl}, func(shortcut fyne.Shortcut) {
		mw.clearCache()
	})

	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyQ, Modifier: fyne.KeyModifierControl}, func(shortcut fyne.Shortcut) {
		mw.app.Quit()
	})

	// Message shortcuts
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyR, Modifier: fyne.KeyModifierControl}, func(shortcut fyne.Shortcut) {
		mw.replyToMessage()
	})
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyR, Modifier: fyne.KeyModifierControl | fyne.KeyModifierShift}, func(shortcut fyne.Shortcut) {
		mw.replyAllToMessage()
	})
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyL, Modifier: fyne.KeyModifierControl}, func(shortcut fyne.Shortcut) {
		mw.forwardMessage()
	})

	// Delete shortcuts - using DEL key (not letter "D")
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyDelete}, func(shortcut fyne.Shortcut) {
		mw.moveToTrash()
	})
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyDelete, Modifier: fyne.KeyModifierControl}, func(shortcut fyne.Shortcut) {
		mw.deleteMessage()
	})

	// View shortcuts
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyH, Modifier: fyne.KeyModifierControl}, func(shortcut fyne.Shortcut) {
		mw.toggleHTMLView()
	})

	// Edit shortcuts
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyF, Modifier: fyne.KeyModifierControl | fyne.KeyModifierShift}, func(shortcut fyne.Shortcut) {
		mw.showSearch()
	})

	// Selection shortcuts
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyA, Modifier: fyne.KeyModifierControl}, func(shortcut fyne.Shortcut) {
		mw.selectAllMessages()
	})
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyEscape}, func(shortcut fyne.Shortcut) {
		mw.clearSelection()
	})

	// Tools shortcuts
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyComma, Modifier: fyne.KeyModifierControl}, func(shortcut fyne.Shortcut) {
		mw.showSettings()
	})

	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyB, Modifier: fyne.KeyModifierControl | fyne.KeyModifierShift}, func(shortcut fyne.Shortcut) {
		mw.showAddressbook()
	})

}

// showKeyboardShortcuts displays a dialog with keyboard shortcuts
func (mw *MainWindow) showKeyboardShortcuts() {
	shortcuts := `Keyboard Shortcuts:

File:
  Ctrl+N         New Message
  F5             Refresh All
  Ctrl+F5        Clear Cache
  Ctrl+Q         Exit

Message:
  Ctrl+R         Reply
  Ctrl+Shift+R   Reply All
  Ctrl+L         Forward
  Delete         Move to Trash
  Ctrl+Delete    Delete Permanently
  Ctrl+Shift+S   Save Attachments
  Ctrl+Shift+A   View Attachments (Inline)
  Right-click    Context Menu
  Shift+F10      Context Menu
  Ctrl+M         Context Menu

View:
  Ctrl+H         Toggle HTML View

Edit:
  Ctrl+C         Copy
  Ctrl+A         Select All Messages
  Ctrl+F         Find in Message
  Ctrl+Shift+F   Search Messages
  Escape         Clear Selection

Tools:
  Ctrl+,         Settings
  Ctrl+Shift+B   Address Book`

	dialog.ShowInformation("Keyboard Shortcuts", shortcuts, mw.window)
}

// showAbout displays the about dialog
func (mw *MainWindow) showAbout() {
	about := `gommail client
Version 1.0.0

A high-performance email client written in Go using the Fyne UI toolkit.

Features:
• Advanced IMAP/SMTP Support
• Multi-Account Management
• Attachment Management
• Filesystem Caching
• Cross-Platform Support

Built with ❤️ using Go and Fyne`

	dialog.ShowInformation("About gommail client", about, mw.window)
}

// handleDeletedFolder handles the case where the currently selected folder has been deleted
// Delegates to FolderController
func (mw *MainWindow) handleDeletedFolder(deletedFolder string) {
	currentFolder := mw.folderController.GetCurrentFolder()
	mw.logger.Info("handleDeletedFolder called for folder: %s (current folder: %s)", deletedFolder, currentFolder)

	// If the deleted folder is currently selected, we need to select a different folder
	if currentFolder == deletedFolder {
		mw.logger.Info("Currently selected folder %s was deleted, selecting alternative", deletedFolder)

		// Clear current selection
		mw.messagesMu.Lock()
		mw.messages = []email.MessageIndexItem{}
		mw.selectedMessage = nil
		mw.messagesMu.Unlock()

		// Update UI
		fyne.Do(func() {
			mw.refreshMessageList()
			mw.updateMessageViewer("")

			// Delegate to FolderController to handle folder selection
			mw.folderController.HandleDeletedFolder(deletedFolder)
		})
	}
}

// startFolderMonitoring starts real-time monitoring for the specified folder
func (mw *MainWindow) startFolderMonitoring(folder string) {
	if mw.imapClient == nil {
		mw.logger.Debug("Cannot start monitoring: no IMAP client")
		return
	}

	// Set up new message callback for monitoring
	mw.imapClient.SetNewMessageCallback(func(updatedFolder string, messages []email.Message) {
		// On new message callback: refresh messages when changes are detected
		mw.logger.Info("Real-time update detected in folder: %s (%d messages)", updatedFolder, len(messages))

		// Only refresh if it's the currently selected folder
		currentFolder := mw.folderController.GetCurrentFolder()
		if updatedFolder == currentFolder {
			fyne.Do(func() {
				mw.statusBar.SetText(fmt.Sprintf("New messages detected in %s, refreshing...", updatedFolder))
			})

			// Use RefreshCoordinator to refresh messages
			mw.refreshMessagesDebounced(updatedFolder, func(err error) {
				if err != nil {
					mw.logger.Error("Failed to refresh messages after real-time update: %v", err)
					fyne.Do(func() {
						mw.statusBar.SetText(fmt.Sprintf("Failed to refresh %s: %v", updatedFolder, err))
					})
				} else {
					fyne.Do(func() {
						mw.statusBar.SetText(fmt.Sprintf("Refreshed %s with new messages", updatedFolder))
					})
				}
			})
		}
	})

	// Set up connection state callback for error handling
	mw.imapClient.SetConnectionStateCallback(func(event email.ConnectionEvent) {
		// Handle connection errors during monitoring
		if event.Error != nil {
			mw.logger.Error("Connection error during monitoring of folder %s: %v", folder, event.Error)
			fyne.Do(func() {
				mw.statusBar.SetText(fmt.Sprintf("Connection error: %v", event.Error))
			})
		}
	})

	// Start monitoring
	if err := mw.imapClient.StartMonitoring(folder); err != nil {
		mw.logger.Error("Failed to start monitoring folder %s: %v", folder, err)
		fyne.Do(func() {
			mw.statusBar.SetText(fmt.Sprintf("Failed to start monitoring: %v", err))
		})
	} else {
		mode := mw.imapClient.GetMonitoringMode()
		mw.logger.Debug("Started monitoring folder '%s' using %s mode", folder, mode.String())

		// Show monitoring status in UI
		fyne.Do(func() {
			currentStatus := mw.statusBar.Text
			if !strings.Contains(currentStatus, "monitoring") {
				mw.statusBar.SetText(fmt.Sprintf("%s (monitoring: %s)", currentStatus, mode.String()))
			}
		})
	}
}

// stopFolderMonitoring stops real-time monitoring
func (mw *MainWindow) stopFolderMonitoring() {
	if mw.imapClient == nil {
		return
	}

	if mw.imapClient.IsMonitoring() {
		folder := mw.imapClient.GetMonitoredFolder()
		mw.logger.Info("Stopping monitoring for folder: %s", folder)
		mw.imapClient.StopMonitoring()
	}
}

// stopFolderMonitoringFast stops real-time monitoring without graceful cleanup
func (mw *MainWindow) stopFolderMonitoringFast() {
	if mw.imapClient == nil {
		return
	}

	if mw.imapClient.IsMonitoring() {
		folder := mw.imapClient.GetMonitoredFolder()
		mw.logger.Debug("Stopping monitoring for folder: %s", folder)
		mw.imapClient.StopMonitoring()
	}
}

// startUnifiedInboxMonitoring starts monitoring for all accounts in unified inbox mode
func (mw *MainWindow) startUnifiedInboxMonitoring() {
	if !mw.accountController.IsUnifiedInbox() {
		mw.logger.Debug("Not in unified inbox mode, skipping unified monitoring")
		return
	}

	mw.logger.Info("Starting unified inbox monitoring for all accounts")

	// Check if monitoring is already active AND healthy for all accounts before stopping
	allAccountsHealthyMonitoring := true
	activeMonitoringCount := 0
	healthyMonitoringCount := 0
	connectedClientCount := 0

	mw.accountController.ForEachClient(func(accountName string, client *imap.ClientWrapper) {
		if client == nil || !client.IsConnected() {
			allAccountsHealthyMonitoring = false
			return
		}

		connectedClientCount++
		isMonitoring := client.IsMonitoring()
		if isMonitoring {
			activeMonitoringCount++

			// Check if the connection is actually healthy
			isHealthy := mw.isClientConnectionHealthy(client, accountName)
			if isHealthy {
				healthyMonitoringCount++
			} else {
				allAccountsHealthyMonitoring = false
				mw.logger.Warn("Account %s is monitoring but connection is unhealthy", accountName)
			}
		} else {
			allAccountsHealthyMonitoring = false
		}
	})
	// Unlock handled by AccountController

	if connectedClientCount == 0 {
		mw.logger.Debug("No connected account clients available for unified inbox monitoring")
		return
	}

	// Only skip restart if all accounts are monitoring AND all connections are healthy
	if allAccountsHealthyMonitoring && healthyMonitoringCount > 0 {
		mw.logger.Info("All accounts (%d) are monitoring INBOX with healthy connections, skipping restart", healthyMonitoringCount)
		return
	}

	// Log the reason for restart
	if activeMonitoringCount > 0 {
		mw.logger.Info("Restarting monitoring: %d accounts monitoring, %d with healthy connections", activeMonitoringCount, healthyMonitoringCount)
	}

	// Only stop existing monitoring if we need to start new monitoring
	if activeMonitoringCount > 0 {
		mw.logger.Info("Stopping existing monitoring for %d accounts before restart (some accounts not monitoring)", activeMonitoringCount)
		mw.stopUnifiedInboxMonitoring()
	} else {
		mw.logger.Info("No existing monitoring detected, starting fresh monitoring for all accounts")
	}

	// Start monitoring for each account's INBOX
	monitoringCount := 0
	mw.accountController.ForEachClient(func(accountName string, client *imap.ClientWrapper) {
		if client == nil || !client.IsConnected() {
			mw.logger.Debug("Skipping unified inbox monitoring for disconnected account %s", accountName)
			return
		}

		// Set up connection state callback for error handling (capture accountName properly)
		func(capturedAccountName string) {
			client.SetConnectionStateCallback(func(event email.ConnectionEvent) {
				// Handle connection errors during monitoring
				if event.Error != nil {
					mw.logger.Error("Connection error for account %s: %v", capturedAccountName, event.Error)
					fyne.Do(func() {
						mw.statusBar.SetText(fmt.Sprintf("Connection error for %s: %v", capturedAccountName, event.Error))
					})
				}
			})
		}(accountName)

		// Set up combined new message callback for both unified inbox refresh AND notifications (capture accountName properly)
		func(capturedAccountName string) {
			client.SetNewMessageCallback(func(updatedFolder string, messages []email.Message) {
				mw.logger.Info("Real-time update detected in %s for account %s (%d messages)", updatedFolder, capturedAccountName, len(messages))

				// PART 1: Handle unified inbox refresh
				// Check if we're in the middle of a unified inbox delete operation for this account
				mw.deleteOperationMutex.RLock()
				deleteInProgress := false
				if mw.unifiedInboxDeleteInProgress != nil {
					deleteInProgress = mw.unifiedInboxDeleteInProgress[capturedAccountName]
				}
				mw.deleteOperationMutex.RUnlock()

				if !deleteInProgress {
					fyne.Do(func() {
						mw.statusBar.SetText(fmt.Sprintf("New messages detected in %s, refreshing...", capturedAccountName))
					})

					// Fetch fresh messages from just this account and merge them
					mw.backgroundWg.Add(1)
					go func() {
						defer mw.backgroundWg.Done()
						mw.refreshSingleAccountInUnifiedInbox(capturedAccountName)
					}()
				} else {
					mw.logger.Debug("Skipping unified inbox refresh due to delete operation in progress (account: %s, folder: %s)", capturedAccountName, updatedFolder)
				}

				// PART 2: Handle desktop notifications for new messages
				if mw.notificationMgr != nil && mw.notificationMgr.IsEnabled() {
					// Convert messages to notification format
					var notificationMessages []notification.MessageInfo
					for _, message := range messages {
						// Extract sender name from the From field
						sender := "Unknown Sender"
						if len(message.From) > 0 {
							if message.From[0].Name != "" {
								sender = message.From[0].Name
							} else {
								sender = message.From[0].Email
							}
						}

						// Extract subject
						subject := message.Subject
						if subject == "" {
							subject = "(No Subject)"
						}

						notificationMessages = append(notificationMessages, notification.MessageInfo{
							Sender:  sender,
							Subject: subject,
						})
					}

					// Show batched notification with account name instead of folder name
					// This makes it clear which account received the message
					err := mw.notificationMgr.ShowNewMessages(notificationMessages, capturedAccountName)
					if err != nil {
						mw.logger.Error("Failed to show notification: %v", err)
					}
				}
			})
		}(accountName)

		// Start monitoring INBOX for this account (capture accountName properly)
		func(capturedAccountName string) {
			if err := client.StartMonitoring("INBOX"); err != nil {
				mw.logger.Error("Failed to start monitoring INBOX for account %s: %v", capturedAccountName, err)
			} else {
				mode := client.GetMonitoringMode()
				mw.logger.Info("Started monitoring INBOX for account '%s' using %s mode", capturedAccountName, mode.String())
				monitoringCount++
			}
		}(accountName)
	})

	if monitoringCount > 0 {
		// Show monitoring status in UI
		fyne.Do(func() {
			currentStatus := mw.statusBar.Text
			if !strings.Contains(currentStatus, "monitoring") {
				mw.statusBar.SetText(fmt.Sprintf("%s (monitoring %d accounts)", currentStatus, monitoringCount))
			}
		})

		// Start a fresh unified inbox health monitor. Repeated calls to
		// startUnifiedInboxMonitoring() should not leave multiple monitors running.
		mw.restartUnifiedInboxHealthMonitoring()
	}
}

// monitorConnectionHealth periodically checks connection health for all accounts
func (mw *MainWindow) monitorConnectionHealth(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second) // Check every 30 seconds
	defer ticker.Stop()

	mw.logger.Debug("Starting connection health monitoring")

	for {
		select {
		case <-ctx.Done():
			mw.logger.Debug("Connection health monitoring stopped")
			return
		case <-ticker.C:
			mw.performHealthChecks()
		}
	}
}

// startGlobalHealthMonitoring starts health monitoring that works in both unified and single account modes
func (mw *MainWindow) startGlobalHealthMonitoring() {
	// Stop any existing global health monitoring
	mw.stopGlobalHealthMonitoring()

	// Create a new context for global health monitoring
	mw.globalHealthCtx, mw.globalHealthCancel = context.WithCancel(context.Background())

	// Start the global health monitoring goroutine
	go mw.monitorConnectionHealth(mw.globalHealthCtx)

	mw.logger.Debug("Started global connection health monitoring")
}

// stopGlobalHealthMonitoring stops the global health monitoring
func (mw *MainWindow) stopGlobalHealthMonitoring() {
	if mw.globalHealthCancel != nil {
		mw.globalHealthCancel()
		mw.globalHealthCancel = nil
		mw.globalHealthCtx = nil
		mw.logger.Debug("Stopped global connection health monitoring")
	}
}

func (mw *MainWindow) restartUnifiedInboxHealthMonitoring() {
	mw.unifiedInboxHealthMu.Lock()
	defer mw.unifiedInboxHealthMu.Unlock()

	if mw.unifiedInboxCancel != nil {
		mw.unifiedInboxCancel()
	}

	ctx, cancel := context.WithCancel(context.Background())
	mw.unifiedInboxCtx = ctx
	mw.unifiedInboxCancel = cancel

	go mw.monitorConnectionHealth(ctx)
}

func (mw *MainWindow) stopUnifiedInboxHealthMonitoring() {
	mw.unifiedInboxHealthMu.Lock()
	defer mw.unifiedInboxHealthMu.Unlock()

	if mw.unifiedInboxCancel != nil {
		mw.unifiedInboxCancel()
		mw.unifiedInboxCancel = nil
		mw.unifiedInboxCtx = nil
		mw.logger.Debug("Stopped unified inbox connection health monitoring")
	}
}

func (mw *MainWindow) setCurrentAccountConnectInProgress(inProgress bool) {
	mw.connectionLifecycleMu.Lock()
	defer mw.connectionLifecycleMu.Unlock()
	mw.currentAccountConnectInProgress = inProgress
}

func (mw *MainWindow) isCurrentAccountConnectInProgress() bool {
	mw.connectionLifecycleMu.Lock()
	defer mw.connectionLifecycleMu.Unlock()
	return mw.currentAccountConnectInProgress
}

func (mw *MainWindow) beginCurrentAccountReconnect(accountName string) bool {
	mw.connectionLifecycleMu.Lock()
	defer mw.connectionLifecycleMu.Unlock()

	if mw.currentAccountConnectInProgress {
		mw.logger.Debug("Skipping reconnect for current account %s because account connection is already in progress", accountName)
		return false
	}

	if mw.reconnectInProgress {
		mw.logger.Debug("Reconnect already in progress for current account %s, skipping duplicate attempt", accountName)
		return false
	}

	mw.reconnectInProgress = true
	return true
}

func (mw *MainWindow) endCurrentAccountReconnect() {
	mw.connectionLifecycleMu.Lock()
	defer mw.connectionLifecycleMu.Unlock()
	mw.reconnectInProgress = false
}

// performHealthChecks checks the health of all account connections.
// This is observation-only: the worker owns reconnection via its own health
// ticker and attemptAutomaticReconnection. The UI monitor only updates the
// status bar to keep the user informed.
func (mw *MainWindow) performHealthChecks() {
	unhealthyAccounts := 0
	totalAccounts := 0

	// In unified inbox mode, check all accounts
	if mw.accountController.IsUnifiedInbox() {
		mw.accountController.ForEachClient(func(accountName string, client *imap.ClientWrapper) {
			totalAccounts++

			// Check if connection is healthy (observation only)
			isHealthy := mw.isClientConnectionHealthy(client, accountName)

			if !isHealthy {
				unhealthyAccounts++
				mw.logger.Warn("Health check: account %s connection unhealthy (worker will handle reconnection)", accountName)
			}
		})
	} else {
		// In single account mode, check the current account
		if mw.accountController.GetCurrentAccount() != nil && mw.imapClient != nil {
			if mw.isCurrentAccountConnectInProgress() {
				accountName := mw.accountController.GetCurrentAccount().Name
				mw.logger.Debug("Skipping health check for current account %s while account connection is in progress", accountName)
				return
			}

			totalAccounts = 1
			accountName := mw.accountController.GetCurrentAccount().Name

			// Check if connection is healthy (observation only)
			isHealthy := mw.isClientConnectionHealthy(mw.imapClient, accountName)

			if !isHealthy {
				unhealthyAccounts++
				mw.logger.Warn("Health check: current account %s connection unhealthy (worker will handle reconnection)", accountName)
			}
		}
	}

	if unhealthyAccounts > 0 {
		mw.logger.Warn("Health check completed: %d/%d accounts have unhealthy connections", unhealthyAccounts, totalAccounts)
		fyne.Do(func() {
			mw.statusBar.SetText(fmt.Sprintf("Connection issues detected on %d account(s) — reconnecting...", unhealthyAccounts))
		})
	} else if totalAccounts > 0 {
		mw.logger.Debug("Health check completed: all %d accounts have healthy connections", totalAccounts)
	}
}

// restartAccountMonitoring restarts monitoring for a specific account
func (mw *MainWindow) restartAccountMonitoring(accountName string, client *imap.ClientWrapper) {
	// Stop monitoring first
	client.StopMonitoring()

	// Wait a moment for cleanup
	time.Sleep(1 * time.Second)

	// Restart monitoring
	if err := client.StartMonitoring("INBOX"); err != nil {
		mw.logger.Error("Failed to restart monitoring for account %s: %v", accountName, err)
	} else {
		mw.logger.Info("Successfully restarted monitoring for account %s", accountName)
	}
}

// reconnectCurrentAccount attempts to reconnect the current account
func (mw *MainWindow) reconnectCurrentAccount() {
	account := mw.accountController.GetCurrentAccount()
	if account == nil {
		return
	}

	accountName := account.Name
	if !mw.beginCurrentAccountReconnect(accountName) {
		return
	}
	defer mw.endCurrentAccountReconnect()

	mw.logger.Info("Reconnecting current account: %s", accountName)

	// Update status bar
	fyne.Do(func() {
		mw.statusBar.SetText(fmt.Sprintf("Reconnecting to %s...", accountName))
	})

	// Force the cached per-account client to be recreated so we don't accidentally
	// reuse a stale connection and then disconnect the same client we just selected.
	mw.accountController.CloseClientForAccount(accountName)

	// Create new IMAP client
	newClient, err := mw.getOrCreateIMAPClient(account)
	if err != nil {
		mw.logger.Error("Failed to reconnect account %s: %v", accountName, err)
		fyne.Do(func() {
			mw.statusBar.SetText(fmt.Sprintf("Failed to reconnect to %s: %v", accountName, err))
		})
		return
	}

	// Replace the old client
	oldClient := mw.imapClient
	mw.imapClient = newClient

	// Clean up old client
	if oldClient != nil && oldClient != newClient {
		if err := oldClient.Disconnect(); err != nil {
			mw.logger.Warn("Failed to disconnect old IMAP client during reconnect for %s: %v", accountName, err)
		}
		oldClient.Stop()
	}

	// Update status bar
	fyne.Do(func() {
		mw.statusBar.SetText(fmt.Sprintf("Reconnected to %s", accountName))
	})

	// Refresh the current folder
	currentFolder := mw.folderController.GetCurrentFolder()
	if currentFolder != "" {
		mw.logger.Info("Refreshing folder %s after reconnection", currentFolder)
		fyne.Do(func() {
			mw.selectFolderWithOptions(currentFolder, true)
		})
	}

	mw.logger.Info("Successfully reconnected current account: %s", accountName)
}

// isClientConnectionHealthy checks if an IMAP client connection is healthy
func (mw *MainWindow) isClientConnectionHealthy(client *imap.ClientWrapper, accountName string) bool {
	if client == nil {
		mw.logger.Debug("Account %s: client is nil", accountName)
		return false
	}

	// Check basic connection status first
	if !client.IsConnected() {
		mw.logger.Debug("Account %s: not connected", accountName)
		return false
	}

	// Perform actual health check by sending a command to the worker
	healthStatus := client.GetHealthStatus()
	if healthStatus == nil {
		mw.logger.Debug("Account %s: health status unavailable", accountName)
		return false
	}

	// Check the connection state
	state, ok := healthStatus["state"].(string)
	if !ok {
		mw.logger.Debug("Account %s: invalid health status format", accountName)
		return false
	}

	// Check if the connection is actually healthy (not just connected)
	connectionHealthy, hasHealthCheck := healthStatus["connection_healthy"].(bool)
	if hasHealthCheck && !connectionHealthy {
		mw.logger.Debug("Account %s: connection health check failed (state: %s)", accountName, state)
		return false
	}

	// Consider connection healthy if it's connected and not in a failed state
	isHealthy := (state == "Connected" || state == "Connecting") && (!hasHealthCheck || connectionHealthy)

	if !isHealthy {
		mw.logger.Debug("Account %s: connection state is %s, healthy=%v (unhealthy)", accountName, state, connectionHealthy)
		return false
	}

	mw.logger.Debug("Account %s: connection is healthy (state: %s, health_check: %v)", accountName, state, connectionHealthy)
	return true
}

// stopUnifiedInboxMonitoring stops monitoring for all accounts in unified inbox mode
func (mw *MainWindow) stopUnifiedInboxMonitoring() {
	mw.logger.Info("Stopping unified inbox monitoring for all accounts")
	mw.stopUnifiedInboxHealthMonitoring()

	mw.accountController.ForEachClient(func(accountName string, client *imap.ClientWrapper) {
		if client.IsMonitoring() {
			folder := client.GetMonitoredFolder()
			mw.logger.Info("Stopping monitoring for account %s, folder: %s", accountName, folder)
			client.StopMonitoring()
		}
	})
}

// stopUnifiedInboxMonitoringFast stops monitoring for all accounts without graceful cleanup
func (mw *MainWindow) stopUnifiedInboxMonitoringFast() {
	mw.logger.Debug("Force stopping unified inbox monitoring for all accounts")
	mw.stopUnifiedInboxHealthMonitoring()

	mw.accountController.ForEachClient(func(accountName string, client *imap.ClientWrapper) {
		if client.IsMonitoring() {
			folder := client.GetMonitoredFolder()
			mw.logger.Debug("Stopping monitoring for account %s, folder: %s", accountName, folder)
			client.StopMonitoring()
		}
	})
}

// ============================================================================
// Debounced Refresh Methods (replacing RefreshCoordinator)
// ============================================================================

// refreshFoldersDebounced refreshes the folder list with debouncing.
// Multiple rapid calls will be coalesced into a single refresh operation.
func (mw *MainWindow) refreshFoldersDebounced(invalidateCache bool, callback func(error)) {
	mw.folderRefreshDebouncer.Debounce(func() error {
		mw.logger.Debug("Executing debounced folder refresh (invalidateCache=%v)", invalidateCache)

		// Check if we have an account and IMAP client
		if mw.accountController.GetCurrentAccount() == nil || mw.imapClient == nil {
			return fmt.Errorf("no account or IMAP client available")
		}

		// Invalidate cache if requested
		if invalidateCache && mw.imapClient != nil {
			mw.imapClient.InvalidateFolderCache()
			mw.imapClient.InvalidateSubscribedFolderCache()
		}

		// Load folders from IMAP
		folders, err := mw.imapClient.ListFolders()
		if err != nil {
			mw.logger.Warn("Failed to reload folders: %v", err)
			return err
		}

		// Update UI with new folder list
		fyne.Do(func() {
			sortedFolders := mw.sortFolders(folders)
			mw.folderController.SetFolders(sortedFolders)
			if folderList := mw.folderController.GetFolderList(); folderList != nil {
				folderList.Refresh()
			}
			mw.statusBar.SetText(fmt.Sprintf("Refreshed %d folders", len(folders)))
		})

		return nil
	}, callback)
}

// refreshMessagesDebounced refreshes messages for a specific folder with debouncing.
// Multiple rapid calls will be coalesced into a single refresh operation.
func (mw *MainWindow) refreshMessagesDebounced(folderName string, callback func(error)) {
	mw.messageRefreshDebouncer.Debounce(func() error {
		mw.logger.Debug("Executing debounced message refresh for folder: %s", folderName)

		// Check if we have an account
		if mw.accountController.GetCurrentAccount() == nil {
			return fmt.Errorf("no account selected")
		}

		// Check if this is the currently selected folder
		currentFolder := mw.folderController.GetCurrentFolder()
		if folderName != "" && currentFolder == folderName {
			// Re-select the current folder to trigger a refresh
			fyne.Do(func() {
				mw.selectFolderWithOptions(folderName, true)
			})
		}

		return nil
	}, callback)
}

// refreshAccountsDebounced refreshes the account list with debouncing.
// Multiple rapid calls will be coalesced into a single refresh operation.
func (mw *MainWindow) refreshAccountsDebounced(callback func(error)) {
	mw.accountRefreshDebouncer.Debounce(func() error {
		mw.logger.Debug("Executing debounced account refresh")

		// Refresh account list
		fyne.Do(func() {
			if mw.accountList != nil {
				mw.accountList.Refresh()
			}
		})

		return nil
	}, callback)
}
