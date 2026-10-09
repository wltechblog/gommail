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
	"encoding/json"
	"fmt"
	_ "image/gif"  // Register GIF format
	_ "image/jpeg" // Register JPEG format
	_ "image/png"  // Register PNG format
	"io"
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

// truncateRunes shortens s to at most max runes without splitting a
// multi-byte character.
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

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
	// freshFetchMu guards freshFetchInProgress, which is touched by
	// concurrent folder-load goroutines.
	freshFetchMu         sync.Mutex
	freshFetchInProgress bool

	// Read timer state
	// readTimerMu guards readTimer: it is armed/cancelled on the UI thread
	// and cleared from the timer's own goroutine.
	readTimerMu sync.Mutex
	readTimer   *time.Timer

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
			mw.imapClient.Stop() // Stops the worker and closes all connections
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

// getSortName returns a human-readable name for the current sort criteria
// Delegates to MessageListController
func (mw *MainWindow) getSortName() string {
	return mw.messageListController.GetSortName()
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
		mw.freshFetchMu.Lock()
		if mw.freshFetchInProgress {
			mw.freshFetchMu.Unlock()
			mw.logger.Debug("Fresh fetch already in progress for folder %s, skipping", folder)
			return
		}
		mw.freshFetchInProgress = true
		mw.freshFetchMu.Unlock()
		defer func() {
			mw.freshFetchMu.Lock()
			mw.freshFetchInProgress = false
			mw.freshFetchMu.Unlock()
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
				mw.clearMessageSelection()
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
						mw.clearMessageSelection()
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
				mw.clearMessageSelection()
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

// updateFolderMessageCount updates the message count for a folder in the folder list
// Delegates to FolderController
func (mw *MainWindow) updateFolderMessageCount(folderName string, newCount int) {
	mw.folderController.UpdateFolderMessageCount(folderName, newCount)
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

	// A single click replaces any prior multi-selection: drop the manager's
	// stale indices and highlights so it agrees with mw.selectedMessage.
	if mw.selectionManager != nil && mw.selectionManager.GetSelectionCount() > 0 {
		mw.selectionManager.ClearSelection()
		if mw.messageList != nil {
			mw.messageList.UnselectAll()
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

// copyMessageText copies the current in-widget text selection to the
// clipboard, falling back to the full text of the displayed message when
// nothing is selected. The selectable RichText handles Ctrl+C itself while
// it holds focus; this covers every other focus location.
func (mw *MainWindow) copyMessageText() {
	if selected := mw.messageViewController.GetSelectedText(); selected != "" {
		mw.window.Clipboard().SetContent(selected)
		mw.statusBar.SetText("Copied selected text to clipboard")
		return
	}
	if text := mw.messageViewController.CopyableMessageText(); text != "" {
		mw.window.Clipboard().SetContent(text)
		mw.statusBar.SetText("Copied message text to clipboard")
		return
	}
	mw.statusBar.SetText("No message text to copy")
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

// clearSelection clears all message selections
// clearMessageSelection drops all selection state: the manager's indices,
// the single selected message, and the widget highlights. Every code path
// that reloads or replaces mw.messages must call this, otherwise the
// manager keeps indices into the old slice.
func (mw *MainWindow) clearMessageSelection() {
	if mw.selectionManager != nil {
		mw.selectionManager.ClearSelection()
	}
	mw.messagesMu.Lock()
	mw.selectedMessage = nil
	mw.messagesMu.Unlock()
	if mw.messageList != nil {
		mw.clearMessageSelection()
	}
}

func (mw *MainWindow) clearSelection() {
	mw.selectionManager.ClearSelection()
	mw.selectedMessage = nil
	mw.updateMessageViewer("Select a message to view")
}

// Multiple message operations

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
			if accountClient, exists := mw.accountController.GetIMAPClientForAccount(selMsg.AccountName); exists {
				imapClient = accountClient
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
	mw.clearMessageSelection()
	if folderList := mw.folderController.GetFolderList(); folderList != nil {
		folderList.UnselectAll()
	}
	mw.accountList.UnselectAll()

	// Refresh UI to show cleared state
	mw.clearMessageSelection()
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

	// Guard against typed-nil wrappers: a nil *imap.ClientWrapper stored in
	// an interface field is not == nil, defeating the search window's checks.
	var searchClient email.IMAPClient
	if mw.imapClient != nil {
		searchClient = mw.imapClient
	}

	opts := SearchOptions{
		Account:    mw.accountController.GetCurrentAccount(),
		IMAPClient: searchClient,
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
				IMAPClient:   searchClient,
				SMTPClient:   mw.smtpClientForAccount(mw.accountController.GetCurrentAccount()),
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

// startReadTimer starts a 5-second timer to mark the message as read
func (mw *MainWindow) startReadTimer(msg *email.Message) {
	// Cancel any existing timer
	mw.readTimerMu.Lock()
	if mw.readTimer != nil {
		mw.readTimer.Stop()
		mw.readTimer = nil
	}
	mw.readTimerMu.Unlock()

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
	mw.readTimerMu.Lock()
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
		mw.readTimerMu.Lock()
		mw.readTimer = nil
		mw.readTimerMu.Unlock()
	})
	mw.readTimerMu.Unlock()
}

// cancelReadTimer cancels the current read timer if one is active
func (mw *MainWindow) cancelReadTimer() {
	mw.readTimerMu.Lock()
	defer mw.readTimerMu.Unlock()
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

	// Determine which account this message belongs to. Runs on a background
	// goroutine, so the message list must be read under its mutex.
	var accountName string
	mw.messagesMu.RLock()
	for _, msg := range mw.messages {
		if msg.Message.UID == updatedMessage.UID {
			accountName = msg.AccountName
			break
		}
	}
	mw.messagesMu.RUnlock()

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
		// On new message callback: inject new messages directly to avoid a full mailbox re-fetch.
		mw.logger.Info("Real-time update detected in folder: %s (%d messages)", updatedFolder, len(messages))

		// Only update if it's the currently selected folder
		currentFolder := mw.folderController.GetCurrentFolder()
		if updatedFolder == currentFolder {
			if len(messages) > 0 {
				// Fast path: inject only the newly arrived messages without re-fetching everything.
				fyne.Do(func() {
					indexItems := mw.convertMessagesToIndexItems(messages, updatedFolder)
					mw.mergeNewMessagesIntoUnifiedInbox(indexItems)
					mw.sortMessages()
					mw.clearMessageSelection()
					mw.refreshMessageList()
					mw.statusBar.SetText(fmt.Sprintf("New messages in %s (%d)", updatedFolder, len(messages)))
				})
				// If the delivery was capped (too many messages at once), schedule a background
				// full refresh to pick up any that were not included in the notification.
				if len(messages) >= imap.MaxNewMessageNotifications {
					mw.logger.Info("Notification cap reached for folder %s, scheduling follow-up full refresh", updatedFolder)
					mw.refreshMessagesDebounced(updatedFolder, func(err error) {
						if err != nil {
							mw.logger.Error("Failed to do follow-up refresh for folder %s: %v", updatedFolder, err)
						}
					})
				}
			} else {
				// Fallback: server signalled a change but no messages were available yet;
				// do a full refresh so the UI stays consistent.
				fyne.Do(func() {
					mw.statusBar.SetText(fmt.Sprintf("New messages detected in %s, refreshing...", updatedFolder))
				})
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
		if folderName == "" || currentFolder != folderName {
			// The user moved on to another folder; refreshing would clobber
			// it. Report the skip so callers' error handling can react.
			return fmt.Errorf("skipped refresh of %s: folder no longer selected", folderName)
		}

		// Re-select the current folder to trigger a refresh
		fyne.Do(func() {
			mw.selectFolderWithOptions(folderName, true)
		})

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
