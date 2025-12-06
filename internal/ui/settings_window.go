package ui

import (
	"fmt"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/wltechblog/gommail/internal/addressbook"
	"github.com/wltechblog/gommail/internal/config"
)

// SettingsWindow represents the settings configuration window
type SettingsWindow struct {
	app            fyne.App
	window         fyne.Window
	config         *config.Config
	addressbookMgr *addressbook.Manager

	// UI components
	tabs            *container.AppTabs
	accountList     *widget.List
	selectedAccount *config.Account

	// Account settings
	accountNameEntry    *widget.Entry
	accountEmailEntry   *widget.Entry
	accountDisplayEntry *widget.Entry

	// IMAP settings
	imapHostEntry     *widget.Entry
	imapPortEntry     *widget.Entry
	imapUsernameEntry *widget.Entry
	imapPasswordEntry *widget.Entry
	imapTLSCheck      *widget.Check

	// SMTP settings
	smtpHostEntry     *widget.Entry
	smtpPortEntry     *widget.Entry
	smtpUsernameEntry *widget.Entry
	smtpPasswordEntry *widget.Entry
	smtpTLSCheck      *widget.Check

	// Special folder settings
	sentFolderSelect  *widget.SelectEntry
	trashFolderSelect *widget.SelectEntry

	// Personality settings
	personalityAccountSelect  *widget.Select
	personalityList           *widget.List
	selectedPersonality       *config.Personality
	personalityNameEntry      *widget.Entry
	personalityEmailEntry     *widget.Entry
	personalityDisplayEntry   *widget.Entry
	personalitySignatureEntry *widget.Entry
	personalityDefaultCheck   *widget.Check

	// UI settings
	themeSelect              *widget.Select
	defaultMessageViewSelect *widget.Select
	windowWidthEntry         *widget.Entry
	windowHeightEntry        *widget.Entry

	// Notification settings
	notificationsEnabledCheck *widget.Check
	notificationTimeoutEntry  *widget.Entry

	// Cache settings
	cacheDirEntry     *widget.Entry
	maxCacheSizeEntry *widget.Entry
	compressionCheck  *widget.Check

	// Tooltip layer enabled
	tooltipLayerEnabled bool

	// Callbacks
	onSaved            func(*config.Config)
	onClosed           func()
	onClearCache       func()
	onFolderManagement func(*config.Account)
}

// SettingsOptions contains options for creating a settings window
type SettingsOptions struct {
	AddressbookMgr     *addressbook.Manager
	OnSaved            func(*config.Config)
	OnClosed           func()
	OnClearCache       func()                // Callback to clear cache from main window
	OnFolderManagement func(*config.Account) // Callback to open folder management for an account
}

// NewSettingsWindow creates a new settings window
func NewSettingsWindow(app fyne.App, cfg *config.Config, opts SettingsOptions) *SettingsWindow {
	window := app.NewWindow("Settings")
	window.Resize(fyne.NewSize(1000, 700)) // Increased size for better usability

	sw := &SettingsWindow{
		app:                 app,
		window:              window,
		config:              cfg,
		addressbookMgr:      opts.AddressbookMgr,
		tooltipLayerEnabled: true,
		onSaved:             opts.OnSaved,
		onClosed:            opts.OnClosed,
		onClearCache:        opts.OnClearCache,
		onFolderManagement:  opts.OnFolderManagement,
	}

	sw.setupUI()
	sw.loadSettings()

	// Handle window close
	window.SetCloseIntercept(func() {
		if sw.onClosed != nil {
			sw.onClosed()
		}
		window.Close()
	})

	return sw
}

// setupUI initializes the user interface
func (sw *SettingsWindow) setupUI() {
	// Create tabs
	sw.tabs = container.NewAppTabs()

	// Accounts tab
	accountsTab := sw.createAccountsTab()
	sw.tabs.Append(container.NewTabItem("Accounts", accountsTab))

	// Personas tab
	personasTab := sw.createPersonasTab()
	sw.tabs.Append(container.NewTabItem("Personas", personasTab))

	// UI tab
	uiTab := sw.createUITab()
	sw.tabs.Append(container.NewTabItem("Interface", uiTab))

	// Cache tab
	cacheTab := sw.createCacheTab()
	sw.tabs.Append(container.NewTabItem("Cache", cacheTab))

	// Addressbook tab
	addressbookTab := sw.createAddressbookTab()
	sw.tabs.Append(container.NewTabItem("Addressbook", addressbookTab))

	// Create buttons with tooltips

	// Create tooltip-enabled buttons
	saveWithTooltip := CreateTooltipButton("Save", "Save all settings changes", func() {
		sw.saveSettings()
	})
	cancelWithTooltip := CreateTooltipButton("Cancel", "Cancel changes and close", func() {
		sw.window.Close()
	})

	buttons := container.NewHBox(
		widget.NewSeparator(),
		saveWithTooltip,
		cancelWithTooltip,
	)

	// Layout
	content := container.NewBorder(
		nil, buttons, nil, nil,
		sw.tabs,
	)

	// Wrap content with tooltip layer
	contentWithTooltips := AddTooltipLayer(content, sw.window.Canvas())
	sw.window.SetContent(contentWithTooltips)
}

// createAccountsTab creates the accounts configuration tab
func (sw *SettingsWindow) createAccountsTab() fyne.CanvasObject {
	// Account list
	sw.accountList = widget.NewList(
		func() int {
			return len(sw.config.Accounts)
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("Account")
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id < len(sw.config.Accounts) {
				account := &sw.config.Accounts[id]
				label := obj.(*widget.Label)
				label.SetText(account.Name)
			}
		},
	)

	sw.accountList.OnSelected = func(id widget.ListItemID) {
		if id < len(sw.config.Accounts) {
			sw.selectedAccount = &sw.config.Accounts[id]
			sw.loadAccountSettings()
		}
	}

	// Account settings form
	sw.accountNameEntry = widget.NewEntry()
	sw.accountEmailEntry = widget.NewEntry()
	sw.accountDisplayEntry = widget.NewEntry()

	// IMAP settings
	sw.imapHostEntry = widget.NewEntry()
	sw.imapPortEntry = widget.NewEntry()
	sw.imapUsernameEntry = widget.NewEntry()
	sw.imapPasswordEntry = widget.NewPasswordEntry()
	sw.imapTLSCheck = widget.NewCheck("Use TLS", nil)

	// SMTP settings
	sw.smtpHostEntry = widget.NewEntry()
	sw.smtpPortEntry = widget.NewEntry()
	sw.smtpUsernameEntry = widget.NewEntry()
	sw.smtpPasswordEntry = widget.NewPasswordEntry()
	sw.smtpTLSCheck = widget.NewCheck("Use TLS", nil)

	// Special folder settings - use SelectEntry for editable dropdowns
	sw.sentFolderSelect = widget.NewSelectEntry([]string{})
	sw.trashFolderSelect = widget.NewSelectEntry([]string{})

	// Set placeholder text
	sw.sentFolderSelect.PlaceHolder = "Auto-detect or enter folder name"
	sw.trashFolderSelect.PlaceHolder = "Auto-detect or enter folder name"

	// Add refresh buttons for folder lists
	refreshFoldersWithTooltip := CreateTooltipButtonWithIcon("Refresh Folders", "Refresh folder list from server", theme.ViewRefreshIcon(), func() {
		sw.refreshFolderOptions()
	})

	// Create form with better spacing and organization
	accountInfoSection := container.NewVBox(
		widget.NewCard("Account Information", "", container.NewVBox(
			container.NewGridWithColumns(2,
				widget.NewLabel("Name:"), sw.accountNameEntry,
				widget.NewLabel("Email:"), sw.accountEmailEntry,
				widget.NewLabel("Display Name:"), sw.accountDisplayEntry,
			),
		)),
	)

	imapSection := container.NewVBox(
		widget.NewCard("IMAP Settings", "", container.NewVBox(
			container.NewGridWithColumns(2,
				widget.NewLabel("Host:"), sw.imapHostEntry,
				widget.NewLabel("Port:"), sw.imapPortEntry,
				widget.NewLabel("Username:"), sw.imapUsernameEntry,
				widget.NewLabel("Password:"), sw.imapPasswordEntry,
			),
			sw.imapTLSCheck,
		)),
	)

	smtpSection := container.NewVBox(
		widget.NewCard("SMTP Settings", "", container.NewVBox(
			container.NewGridWithColumns(2,
				widget.NewLabel("Host:"), sw.smtpHostEntry,
				widget.NewLabel("Port:"), sw.smtpPortEntry,
				widget.NewLabel("Username:"), sw.smtpUsernameEntry,
				widget.NewLabel("Password:"), sw.smtpPasswordEntry,
			),
			sw.smtpTLSCheck,
		)),
	)

	specialFoldersSection := container.NewVBox(
		widget.NewCard("Special Folders", "Configure which folders to use for sent messages and deleted messages. Leave empty for auto-detection.", container.NewVBox(
			container.NewHBox(
				widget.NewLabel(""),
				refreshFoldersWithTooltip,
			),
			container.NewGridWithColumns(2,
				widget.NewLabel("Sent Folder:"), sw.sentFolderSelect,
				widget.NewLabel("Trash Folder:"), sw.trashFolderSelect,
			),
		)),
	)

	accountForm := container.NewVBox(
		accountInfoSection,
		imapSection,
		smtpSection,
		specialFoldersSection,
	)

	// Account management buttons - use more compact layout

	// Create tooltip-enabled buttons
	addWithTooltip := CreateTooltipButtonWithIcon("Add Account", "Add new email account", theme.ContentAddIcon(), func() {
		sw.addAccount()
	})
	removeWithTooltip := CreateTooltipButtonWithIcon("Remove Account", "Remove selected account", theme.ContentRemoveIcon(), func() {
		sw.removeAccount()
	})
	testWithTooltip := CreateTooltipButtonWithIcon("Test Connection", "Test connection to selected account", theme.ConfirmIcon(), func() {
		sw.testConnection()
	})
	folderManagementWithTooltip := CreateTooltipButtonWithIcon("Manage Folders", "Manage folder subscriptions for selected account", theme.FolderIcon(), func() {
		sw.manageFolders()
	})

	// Use vertical layout for buttons to save horizontal space
	accountButtons := container.NewVBox(addWithTooltip, removeWithTooltip, testWithTooltip, folderManagementWithTooltip)

	// Create a more compact left panel
	accountsHeader := widget.NewRichTextFromMarkdown("**Accounts**")
	leftPanel := container.NewBorder(
		accountsHeader, accountButtons, nil, nil,
		sw.accountList,
	)

	// Add padding around the form for better visual appearance
	paddedForm := container.NewPadded(accountForm)
	rightPanel := container.NewScroll(paddedForm)

	// Create HSplit with custom sizing - left panel gets less space
	hsplit := container.NewHSplit(leftPanel, rightPanel)
	hsplit.SetOffset(0.25) // Left panel gets 25%, right panel gets 75%

	// Set minimum size for left panel to prevent it from being too cramped
	leftPanel.Resize(fyne.NewSize(200, 0)) // Minimum width of 200 pixels

	return hsplit
}

// createPersonalitiesSection creates the personalities management section
func (sw *SettingsWindow) createPersonalitiesSection() fyne.CanvasObject {
	// Create personality list
	sw.personalityList = widget.NewList(
		func() int {
			if sw.selectedAccount == nil {
				return 0
			}
			return len(sw.selectedAccount.Personalities)
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("Persona")
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if sw.selectedAccount == nil || id >= len(sw.selectedAccount.Personalities) {
				return
			}
			personality := &sw.selectedAccount.Personalities[id]
			label := obj.(*widget.Label)
			defaultMarker := ""
			if personality.IsDefault {
				defaultMarker = " (Default)"
			}
			label.SetText(personality.Name + defaultMarker)
		},
	)

	sw.personalityList.OnSelected = func(id widget.ListItemID) {
		if sw.selectedAccount == nil || id >= len(sw.selectedAccount.Personalities) {
			return
		}
		sw.selectedPersonality = &sw.selectedAccount.Personalities[id]
		sw.loadPersonalitySettings()
	}

	// Personality form
	personalityForm := container.NewGridWithColumns(2,
		widget.NewLabel("Name:"), sw.personalityNameEntry,
		widget.NewLabel("Email:"), sw.personalityEmailEntry,
		widget.NewLabel("Display Name:"), sw.personalityDisplayEntry,
	)

	// Create a scrollable container for the signature to handle long signatures
	signatureScroll := container.NewScroll(sw.personalitySignatureEntry)
	signatureScroll.SetMinSize(fyne.NewSize(400, 100))

	signatureContainer := container.NewVBox(
		widget.NewLabel("Signature:"),
		signatureScroll,
	)

	// Personality management buttons
	addPersonalityBtn := widget.NewButtonWithIcon("Add", theme.ContentAddIcon(), func() {
		sw.addPersonality()
	})
	removePersonalityBtn := widget.NewButtonWithIcon("Remove", theme.ContentRemoveIcon(), func() {
		sw.removePersonality()
	})
	savePersonalityBtn := widget.NewButtonWithIcon("Save", theme.DocumentSaveIcon(), func() {
		sw.savePersonalitySettings()
	})

	personalityButtons := container.NewHBox(addPersonalityBtn, removePersonalityBtn, savePersonalityBtn)

	// Add helpful instructions
	instructionText := widget.NewRichTextFromMarkdown("*Select a persona from the list to edit, or click **Add** to create a new one. The default persona will be automatically selected when composing new messages.*")
	instructionText.Wrapping = fyne.TextWrapWord

	personalityFormContainer := container.NewVBox(
		instructionText,
		widget.NewSeparator(),
		personalityForm,
		sw.personalityDefaultCheck,
		signatureContainer,
		personalityButtons,
	)

	// Create left and right panels with minimum sizes
	sw.personalityList.Resize(fyne.NewSize(200, 150))
	leftPanel := container.NewBorder(
		widget.NewLabel("Personas"), nil, nil, nil,
		sw.personalityList,
	)

	rightPanel := container.NewBorder(
		widget.NewLabel("Persona Details"), nil, nil, nil,
		container.NewScroll(personalityFormContainer),
	)

	// Create HSplit with better proportions
	hsplit := container.NewHSplit(leftPanel, rightPanel)
	hsplit.SetOffset(0.35) // Left panel gets 35%, right panel gets 65%

	// Set minimum size for the entire personas section
	personasCard := widget.NewCard("Personas", "Manage different identities for this account. Click 'Add' to create a new persona, select one from the list to edit it.", hsplit)
	personasCard.Resize(fyne.NewSize(800, 300))

	return personasCard
}

// createPersonasTab creates the dedicated personas management tab
func (sw *SettingsWindow) createPersonasTab() fyne.CanvasObject {
	// Initialize personality components if not already done
	if sw.personalityNameEntry == nil {
		sw.personalityNameEntry = widget.NewEntry()
		sw.personalityNameEntry.SetPlaceHolder("e.g., Professional, Support, Personal")
		sw.personalityEmailEntry = widget.NewEntry()
		sw.personalityEmailEntry.SetPlaceHolder("persona@example.com")
		sw.personalityDisplayEntry = widget.NewEntry()
		sw.personalityDisplayEntry.SetPlaceHolder("Display name for this persona")
		sw.personalitySignatureEntry = widget.NewEntry()
		sw.personalitySignatureEntry.MultiLine = true
		sw.personalitySignatureEntry.SetPlaceHolder("Optional signature for this persona...")
		sw.personalityDefaultCheck = widget.NewCheck("Set as default persona", nil)
	}

	// Create account selection dropdown
	accountOptions := make([]string, len(sw.config.Accounts))
	for i, account := range sw.config.Accounts {
		accountOptions[i] = account.Name
	}

	sw.personalityAccountSelect = widget.NewSelect(accountOptions, func(selected string) {
		// Find the selected account
		for i, account := range sw.config.Accounts {
			if account.Name == selected {
				sw.selectedAccount = &sw.config.Accounts[i]
				sw.selectedPersonality = nil
				sw.loadPersonalitySettings()
				if sw.personalityList != nil {
					sw.personalityList.Refresh()
					sw.personalityList.UnselectAll()
				}
				break
			}
		}
	})

	// Set placeholder text for when no accounts exist
	if len(accountOptions) == 0 {
		sw.personalityAccountSelect.PlaceHolder = "No accounts configured"
	} else {
		sw.personalityAccountSelect.PlaceHolder = "Select an account..."
	}

	// Create personality list
	sw.personalityList = widget.NewList(
		func() int {
			if sw.selectedAccount == nil {
				return 0
			}
			return len(sw.selectedAccount.Personalities)
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("Persona")
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if sw.selectedAccount == nil || id >= len(sw.selectedAccount.Personalities) {
				return
			}
			personality := &sw.selectedAccount.Personalities[id]
			label := obj.(*widget.Label)
			defaultMarker := ""
			if personality.IsDefault {
				defaultMarker = " (Default)"
			}
			label.SetText(personality.Name + defaultMarker)
		},
	)

	sw.personalityList.OnSelected = func(id widget.ListItemID) {
		if sw.selectedAccount == nil || id >= len(sw.selectedAccount.Personalities) {
			return
		}
		sw.selectedPersonality = &sw.selectedAccount.Personalities[id]
		sw.loadPersonalitySettings()
	}

	// Create a more compact account selection section
	accountSelectionSection := container.NewBorder(
		nil, nil, widget.NewLabel("Account:"), nil,
		sw.personalityAccountSelect,
	)

	var content fyne.CanvasObject
	if len(sw.config.Accounts) == 0 {
		// Show message when no accounts are configured
		noAccountsMessage := widget.NewRichTextFromMarkdown("## No Accounts Configured\n\nYou need to configure at least one email account before you can manage personas.\n\nGo to the **Accounts** tab to add an email account first.")
		noAccountsMessage.Wrapping = fyne.TextWrapWord

		content = container.NewVBox(
			widget.NewCard("Account Selection", "Select the account you want to manage personas for", accountSelectionSection),
			widget.NewCard("", "", noAccountsMessage),
		)
	} else {
		// Create the personas management interface
		personasInterface := sw.createPersonasInterface()

		// Use border layout to give maximum space to personas interface
		content = container.NewBorder(
			widget.NewCard("Account Selection", "Select the account you want to manage personas for", accountSelectionSection),
			nil, nil, nil,
			personasInterface,
		)

		// Select first account by default
		sw.personalityAccountSelect.SetSelected(sw.config.Accounts[0].Name)
	}

	return content
}

// openAddressbookDialog opens the addressbook management dialog
func (sw *SettingsWindow) openAddressbookDialog() {
	opts := AddressbookDialogOptions{
		OnClosed: func() {
			// Dialog closed, no action needed
		},
	}

	// Get the ConfigManager from the addressbook manager
	configMgr := sw.addressbookMgr.GetConfigManager()
	dialog := NewAddressbookDialog(sw.app, sw.addressbookMgr, configMgr, opts)
	dialog.Show()
}

// createPersonasInterface creates the personas management interface without the card wrapper
func (sw *SettingsWindow) createPersonasInterface() fyne.CanvasObject {
	// Personality form with better spacing
	personalityForm := container.NewVBox(
		container.NewGridWithColumns(2,
			widget.NewLabel("Name:"), sw.personalityNameEntry,
		),
		container.NewGridWithColumns(2,
			widget.NewLabel("Email:"), sw.personalityEmailEntry,
		),
		container.NewGridWithColumns(2,
			widget.NewLabel("Display Name:"), sw.personalityDisplayEntry,
		),
	)

	// Create a larger signature field for better usability
	sw.personalitySignatureEntry.Resize(fyne.NewSize(500, 120))

	signatureContainer := container.NewVBox(
		widget.NewLabel("Signature:"),
		sw.personalitySignatureEntry,
	)

	// Personality management buttons
	addPersonalityBtn := widget.NewButtonWithIcon("Add", theme.ContentAddIcon(), func() {
		sw.addPersonality()
	})
	removePersonalityBtn := widget.NewButtonWithIcon("Remove", theme.ContentRemoveIcon(), func() {
		sw.removePersonality()
	})
	savePersonalityBtn := widget.NewButtonWithIcon("Save", theme.DocumentSaveIcon(), func() {
		sw.savePersonalitySettings()
	})

	// Create button layout with better spacing
	personalityButtons := container.NewGridWithColumns(3, addPersonalityBtn, removePersonalityBtn, savePersonalityBtn)

	// Add helpful instructions
	instructionText := widget.NewRichTextFromMarkdown("*Select a persona from the list to edit, or click **Add** to create a new one. The default persona will be automatically selected when composing new messages.*")
	instructionText.Wrapping = fyne.TextWrapWord

	personalityFormContainer := container.NewVBox(
		instructionText,
		widget.NewSeparator(),
		personalityForm,
		widget.NewSeparator(),
		sw.personalityDefaultCheck,
		widget.NewSeparator(),
		signatureContainer,
		widget.NewSeparator(),
		personalityButtons,
	)

	// Create left and right panels with better sizing
	leftPanel := container.NewBorder(
		widget.NewLabel("Personas"), nil, nil, nil,
		container.NewScroll(sw.personalityList),
	)

	rightPanel := container.NewBorder(
		widget.NewLabel("Persona Details"), nil, nil, nil,
		personalityFormContainer, // Remove scroll wrapper to give more space
	)

	// Create HSplit with better proportions for the dedicated tab
	hsplit := container.NewHSplit(leftPanel, rightPanel)
	hsplit.SetOffset(0.25) // Left panel gets 25%, right panel gets 75% for more space

	personasCard := widget.NewCard("Persona Management", "Manage different identities for the selected account", hsplit)

	return personasCard
}

// refreshPersonasTab refreshes the personas tab when accounts change
func (sw *SettingsWindow) refreshPersonasTab() {
	if sw.personalityAccountSelect == nil {
		return
	}

	// Update account options
	accountOptions := make([]string, len(sw.config.Accounts))
	for i, account := range sw.config.Accounts {
		accountOptions[i] = account.Name
	}

	sw.personalityAccountSelect.Options = accountOptions

	// Update placeholder
	if len(accountOptions) == 0 {
		sw.personalityAccountSelect.PlaceHolder = "No accounts configured"
		sw.personalityAccountSelect.SetSelected("")
		sw.selectedAccount = nil
		sw.selectedPersonality = nil
		sw.loadPersonalitySettings()
	} else {
		sw.personalityAccountSelect.PlaceHolder = "Select an account..."
		// If current selection is no longer valid, select first account
		currentSelection := sw.personalityAccountSelect.Selected
		validSelection := false
		for _, option := range accountOptions {
			if option == currentSelection {
				validSelection = true
				break
			}
		}
		if !validSelection && len(accountOptions) > 0 {
			sw.personalityAccountSelect.SetSelected(accountOptions[0])
		}
	}

	sw.personalityAccountSelect.Refresh()
	if sw.personalityList != nil {
		sw.personalityList.Refresh()
	}
}

// createUITab creates the UI configuration tab
func (sw *SettingsWindow) createUITab() fyne.CanvasObject {
	sw.themeSelect = widget.NewSelect([]string{"auto", "light", "dark"}, nil)
	sw.defaultMessageViewSelect = widget.NewSelect([]string{"html", "text"}, nil)
	sw.windowWidthEntry = widget.NewEntry()
	sw.windowHeightEntry = widget.NewEntry()

	// Notification settings
	sw.notificationsEnabledCheck = widget.NewCheck("Enable desktop notifications", nil)
	sw.notificationTimeoutEntry = widget.NewEntry()
	sw.notificationTimeoutEntry.SetPlaceHolder("5")

	form := container.NewVBox(
		widget.NewLabel("Appearance"),
		container.NewGridWithColumns(2,
			widget.NewLabel("Theme:"), sw.themeSelect,
		),
		widget.NewSeparator(),
		widget.NewLabel("Message Display"),
		container.NewGridWithColumns(2,
			widget.NewLabel("Default View:"), sw.defaultMessageViewSelect,
		),
		widget.NewRichTextFromMarkdown("*Choose whether to display HTML emails as formatted text or plain text by default. You can always toggle between views using Ctrl+H or the toolbar button.*"),
		widget.NewSeparator(),
		widget.NewLabel("Notifications"),
		sw.notificationsEnabledCheck,
		container.NewGridWithColumns(2,
			widget.NewLabel("Timeout (seconds):"), sw.notificationTimeoutEntry,
		),
		widget.NewRichTextFromMarkdown("*Desktop notifications will appear when new messages arrive. Notifications only appear after the initial sync is complete.*"),
		widget.NewSeparator(),
		widget.NewLabel("Window Settings"),
		container.NewGridWithColumns(2,
			widget.NewLabel("Default Width:"), sw.windowWidthEntry,
			widget.NewLabel("Default Height:"), sw.windowHeightEntry,
		),
	)

	return container.NewScroll(form)
}

// createCacheTab creates the cache configuration tab
func (sw *SettingsWindow) createCacheTab() fyne.CanvasObject {
	sw.cacheDirEntry = widget.NewEntry()
	sw.maxCacheSizeEntry = widget.NewEntry()
	sw.compressionCheck = widget.NewCheck("Enable Compression", nil)

	// Create tooltip-enabled buttons
	browseWithTooltip := CreateTooltipButtonWithIcon("Browse", "Browse for cache directory", theme.FolderOpenIcon(), func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err == nil && uri != nil {
				sw.cacheDirEntry.SetText(uri.Path())
			}
		}, sw.window)
	})
	clearWithTooltip := CreateTooltipButtonWithIcon("Clear Cache", "Clear all cached data", theme.DeleteIcon(), func() {
		dialog.ShowConfirm("Clear Cache",
			"Are you sure you want to clear the cache? This will remove all cached messages and attachments.",
			func(confirmed bool) {
				if confirmed {
					sw.clearCache()
				}
			}, sw.window)
	})

	form := container.NewVBox(
		widget.NewLabel("Cache Settings"),
		container.NewGridWithColumns(3,
			widget.NewLabel("Cache Directory:"), sw.cacheDirEntry, browseWithTooltip,
			widget.NewLabel("Max Size (MB):"), sw.maxCacheSizeEntry, widget.NewLabel(""),
		),
		sw.compressionCheck,
		widget.NewSeparator(),
		widget.NewLabel("Cache Management"),
		clearWithTooltip,
	)

	return container.NewScroll(form)
}

// createAddressbookTab creates the addressbook configuration tab
func (sw *SettingsWindow) createAddressbookTab() fyne.CanvasObject {
	// Auto-collection settings
	autoCollectCheck := widget.NewCheck("Automatically add contacts when sending emails", nil)

	// Addressbook management buttons
	openAddressbookWithTooltip := CreateTooltipButtonWithIcon("Open Addressbook", "Open the addressbook dialog to manage contacts", theme.AccountIcon(), func() {
		if sw.addressbookMgr != nil {
			sw.openAddressbookDialog()
		} else {
			dialog.ShowInformation("Addressbook", "Addressbook manager not available.", sw.window)
		}
	})

	// Import/Export buttons
	importContactsWithTooltip := CreateTooltipButtonWithIcon("Import Contacts", "Import contacts from CSV file", theme.FolderOpenIcon(), func() {
		sw.importContacts()
	})

	exportContactsWithTooltip := CreateTooltipButtonWithIcon("Export Contacts", "Export contacts to CSV file", theme.DocumentSaveIcon(), func() {
		sw.exportContacts()
	})

	form := container.NewVBox(
		widget.NewLabel("Contact Collection"),
		autoCollectCheck,
		widget.NewRichTextFromMarkdown("*When enabled, email addresses will be automatically added to your contacts when you send messages.*"),
		widget.NewSeparator(),
		widget.NewLabel("Addressbook Management"),
		container.NewHBox(
			openAddressbookWithTooltip,
		),
		widget.NewSeparator(),
		widget.NewLabel("Import/Export"),
		container.NewHBox(
			importContactsWithTooltip,
			exportContactsWithTooltip,
		),
		widget.NewRichTextFromMarkdown("*Import and export your contacts to/from CSV files for backup or migration purposes.*"),
	)

	return container.NewScroll(form)
}

// loadSettings loads current configuration into the UI
func (sw *SettingsWindow) loadSettings() {
	// Load UI settings
	sw.themeSelect.SetSelected(sw.config.UI.Theme)

	// Set default message view, defaulting to "html" if not set
	defaultView := sw.config.UI.DefaultMessageView
	if defaultView == "" {
		defaultView = "html"
	}
	sw.defaultMessageViewSelect.SetSelected(defaultView)

	sw.windowWidthEntry.SetText(strconv.Itoa(sw.config.UI.WindowSize.Width))
	sw.windowHeightEntry.SetText(strconv.Itoa(sw.config.UI.WindowSize.Height))

	// Load notification settings
	sw.notificationsEnabledCheck.SetChecked(sw.config.UI.Notifications.Enabled)
	sw.notificationTimeoutEntry.SetText(strconv.Itoa(sw.config.UI.Notifications.TimeoutSeconds))

	// Load cache settings
	sw.cacheDirEntry.SetText(sw.config.Cache.Directory)
	sw.maxCacheSizeEntry.SetText(strconv.Itoa(sw.config.Cache.MaxSizeMB))
	sw.compressionCheck.SetChecked(sw.config.Cache.Compression)

	// Select first account if available
	if len(sw.config.Accounts) > 0 {
		sw.accountList.Select(0)
	}
}

// loadAccountSettings loads the selected account settings into the form
func (sw *SettingsWindow) loadAccountSettings() {
	if sw.selectedAccount == nil {
		return
	}

	account := sw.selectedAccount

	// Account info
	sw.accountNameEntry.SetText(account.Name)
	sw.accountEmailEntry.SetText(account.Email)
	sw.accountDisplayEntry.SetText(account.DisplayName)

	// IMAP settings
	sw.imapHostEntry.SetText(account.IMAP.Host)
	sw.imapPortEntry.SetText(strconv.Itoa(account.IMAP.Port))
	sw.imapUsernameEntry.SetText(account.IMAP.Username)
	sw.imapPasswordEntry.SetText(account.IMAP.Password)
	sw.imapTLSCheck.SetChecked(account.IMAP.TLS)

	// SMTP settings
	sw.smtpHostEntry.SetText(account.SMTP.Host)
	sw.smtpPortEntry.SetText(strconv.Itoa(account.SMTP.Port))
	sw.smtpUsernameEntry.SetText(account.SMTP.Username)
	sw.smtpPasswordEntry.SetText(account.SMTP.Password)
	sw.smtpTLSCheck.SetChecked(account.SMTP.TLS)

	// Special folder settings
	sw.refreshFolderOptions() // Ensure options are available
	sw.sentFolderSelect.SetText(account.SentFolder)
	sw.trashFolderSelect.SetText(account.TrashFolder)
}

// saveSettings saves the current form data to configuration
func (sw *SettingsWindow) saveSettings() {
	// Save UI settings
	sw.config.UI.Theme = sw.themeSelect.Selected
	sw.config.UI.DefaultMessageView = sw.defaultMessageViewSelect.Selected

	if width, err := strconv.Atoi(sw.windowWidthEntry.Text); err == nil {
		sw.config.UI.WindowSize.Width = width
	}
	if height, err := strconv.Atoi(sw.windowHeightEntry.Text); err == nil {
		sw.config.UI.WindowSize.Height = height
	}

	// Save notification settings
	sw.config.UI.Notifications.Enabled = sw.notificationsEnabledCheck.Checked
	if timeout, err := strconv.Atoi(sw.notificationTimeoutEntry.Text); err == nil && timeout > 0 {
		sw.config.UI.Notifications.TimeoutSeconds = timeout
	}

	// Save cache settings
	sw.config.Cache.Directory = sw.cacheDirEntry.Text
	if maxSize, err := strconv.Atoi(sw.maxCacheSizeEntry.Text); err == nil {
		sw.config.Cache.MaxSizeMB = maxSize
	}
	sw.config.Cache.Compression = sw.compressionCheck.Checked

	// Save current account settings if one is selected
	if sw.selectedAccount != nil {
		sw.saveCurrentAccountSettings()
	}

	dialog.ShowInformation("Settings Saved", "Settings have been saved successfully", sw.window)

	if sw.onSaved != nil {
		sw.onSaved(sw.config)
	}

	sw.window.Close()
}

// saveCurrentAccountSettings saves the current account form data
func (sw *SettingsWindow) saveCurrentAccountSettings() {
	if sw.selectedAccount == nil {
		return
	}

	account := sw.selectedAccount

	// Account info
	account.Name = sw.accountNameEntry.Text
	account.Email = sw.accountEmailEntry.Text
	account.DisplayName = sw.accountDisplayEntry.Text

	// IMAP settings
	account.IMAP.Host = sw.imapHostEntry.Text
	if port, err := strconv.Atoi(sw.imapPortEntry.Text); err == nil {
		account.IMAP.Port = port
	}
	account.IMAP.Username = sw.imapUsernameEntry.Text
	account.IMAP.Password = sw.imapPasswordEntry.Text
	account.IMAP.TLS = sw.imapTLSCheck.Checked

	// SMTP settings
	account.SMTP.Host = sw.smtpHostEntry.Text
	if port, err := strconv.Atoi(sw.smtpPortEntry.Text); err == nil {
		account.SMTP.Port = port
	}
	account.SMTP.Username = sw.smtpUsernameEntry.Text
	account.SMTP.Password = sw.smtpPasswordEntry.Text
	account.SMTP.TLS = sw.smtpTLSCheck.Checked

	// Special folder settings
	account.SentFolder = sw.sentFolderSelect.Text
	account.TrashFolder = sw.trashFolderSelect.Text
}

// addAccount adds a new account using the wizard
func (sw *SettingsWindow) addAccount() {
	// Create and show the new account wizard
	wizard := NewNewAccountWizard(sw.app, sw.config, AddAccountMode)
	wizard.SetOnComplete(func(updatedConfig *config.Config) {
		if updatedConfig != nil {
			// Update our config reference
			sw.config = updatedConfig

			// Refresh the account list to show the new account
			sw.accountList.Refresh()

			// Refresh the personas tab
			sw.refreshPersonasTab()

			// Select the newly added account (last in the list)
			if len(sw.config.Accounts) > 0 {
				sw.accountList.Select(len(sw.config.Accounts) - 1)
			}
		}
	})

	wizard.Show()
}

// removeAccount removes the selected account
func (sw *SettingsWindow) removeAccount() {
	if sw.selectedAccount == nil {
		dialog.ShowInformation("No Selection", "Please select an account to remove", sw.window)
		return
	}

	dialog.ShowConfirm("Remove Account",
		fmt.Sprintf("Are you sure you want to remove the account '%s'?", sw.selectedAccount.Name),
		func(confirmed bool) {
			if confirmed {
				// Find and remove the account by comparing pointers to slice elements
				for i := range sw.config.Accounts {
					if &sw.config.Accounts[i] == sw.selectedAccount {
						sw.config.Accounts = append(sw.config.Accounts[:i], sw.config.Accounts[i+1:]...)
						break
					}
				}
				sw.selectedAccount = nil
				sw.accountList.Refresh()

				// Refresh the personas tab
				sw.refreshPersonasTab()

				// Clear form
				sw.clearAccountForm()

				// Immediately notify the main window about the account removal
				// This ensures the unified inbox and status bar are updated right away
				if sw.onSaved != nil {
					sw.onSaved(sw.config)
				}
			}
		}, sw.window)
}

// testConnection tests the connection to the selected account
func (sw *SettingsWindow) testConnection() {
	if sw.selectedAccount == nil {
		dialog.ShowInformation("No Selection", "Please select an account to test", sw.window)
		return
	}

	// Save current form data to account temporarily for testing
	sw.saveCurrentAccountSettings()

	dialog.ShowInformation("Test Connection",
		"Connection testing is not implemented yet. This would test IMAP and SMTP connections.",
		sw.window)
}

// clearCache clears the application cache
func (sw *SettingsWindow) clearCache() {
	// Use the callback to call the main window's clear cache functionality
	if sw.onClearCache != nil {
		// Close settings window first to avoid UI conflicts
		sw.window.Close()
		// Call the main window's clear cache method via callback
		sw.onClearCache()
	} else {
		dialog.ShowError(fmt.Errorf("unable to clear cache: clear cache callback not available"), sw.window)
	}
}

// refreshFolderOptions refreshes the folder options for the special folder selects
func (sw *SettingsWindow) refreshFolderOptions() {
	if sw.selectedAccount == nil {
		return
	}

	// For now, provide common folder options and allow custom entry
	// In a future enhancement, this could connect to IMAP to get actual folders
	commonFolders := []string{
		"", // Empty option for auto-detection
		"INBOX",
		"Sent",
		"Sent Items",
		"[Gmail]/Sent Mail",
		"Drafts",
		"Trash",
		"Deleted Items",
		"[Gmail]/Trash",
		"Archive",
		"Spam",
		"Junk",
	}

	sw.sentFolderSelect.SetOptions(commonFolders)
	sw.trashFolderSelect.SetOptions(commonFolders)
}

// clearAccountForm clears the account settings form
func (sw *SettingsWindow) clearAccountForm() {
	sw.accountNameEntry.SetText("")
	sw.accountEmailEntry.SetText("")
	sw.accountDisplayEntry.SetText("")
	sw.imapHostEntry.SetText("")
	sw.imapPortEntry.SetText("")
	sw.imapUsernameEntry.SetText("")
	sw.imapPasswordEntry.SetText("")
	sw.imapTLSCheck.SetChecked(false)
	sw.smtpHostEntry.SetText("")
	sw.smtpPortEntry.SetText("")
	sw.smtpUsernameEntry.SetText("")
	sw.smtpPasswordEntry.SetText("")
	sw.smtpTLSCheck.SetChecked(false)
	sw.sentFolderSelect.SetText("")
	sw.trashFolderSelect.SetText("")
}

// manageFolders opens the folder management dialog for the selected account
func (sw *SettingsWindow) manageFolders() {
	if sw.selectedAccount == nil {
		dialog.ShowInformation("No Selection", "Please select an account to manage folders", sw.window)
		return
	}

	if sw.onFolderManagement == nil {
		dialog.ShowError(fmt.Errorf("folder management not available"), sw.window)
		return
	}

	// Save current form data to account temporarily before opening folder management
	sw.saveCurrentAccountSettings()

	// Call the folder management callback with the selected account
	sw.onFolderManagement(sw.selectedAccount)
}

// loadPersonalitySettings loads the selected personality into the form
func (sw *SettingsWindow) loadPersonalitySettings() {
	if sw.selectedPersonality == nil {
		sw.personalityNameEntry.SetText("")
		sw.personalityEmailEntry.SetText("")
		sw.personalityDisplayEntry.SetText("")
		sw.personalitySignatureEntry.SetText("")
		sw.personalityDefaultCheck.SetChecked(false)

		// Disable form fields when no personality is selected
		sw.personalityNameEntry.Disable()
		sw.personalityEmailEntry.Disable()
		sw.personalityDisplayEntry.Disable()
		sw.personalitySignatureEntry.Disable()
		sw.personalityDefaultCheck.Disable()
		return
	}

	// Enable form fields when a personality is selected
	sw.personalityNameEntry.Enable()
	sw.personalityEmailEntry.Enable()
	sw.personalityDisplayEntry.Enable()
	sw.personalitySignatureEntry.Enable()
	sw.personalityDefaultCheck.Enable()

	sw.personalityNameEntry.SetText(sw.selectedPersonality.Name)
	sw.personalityEmailEntry.SetText(sw.selectedPersonality.Email)
	sw.personalityDisplayEntry.SetText(sw.selectedPersonality.DisplayName)
	sw.personalitySignatureEntry.SetText(sw.selectedPersonality.Signature)
	sw.personalityDefaultCheck.SetChecked(sw.selectedPersonality.IsDefault)
}

// savePersonalitySettings saves the form data to the selected personality
func (sw *SettingsWindow) savePersonalitySettings() {
	if sw.selectedPersonality == nil {
		dialog.ShowInformation("No Selection", "Please select a persona to save", sw.window)
		return
	}

	sw.selectedPersonality.Name = sw.personalityNameEntry.Text
	sw.selectedPersonality.Email = sw.personalityEmailEntry.Text
	sw.selectedPersonality.DisplayName = sw.personalityDisplayEntry.Text
	sw.selectedPersonality.Signature = sw.personalitySignatureEntry.Text

	// Handle default persona logic
	if sw.personalityDefaultCheck.Checked {
		// Set this as default and clear others
		sw.selectedAccount.SetDefaultPersonality(sw.selectedPersonality.Email)
	} else {
		sw.selectedPersonality.IsDefault = false
	}

	// Refresh the personality list to show updated names and default marker
	sw.personalityList.Refresh()

	dialog.ShowInformation("Saved", "Persona settings saved successfully", sw.window)
}

// addPersonality adds a new personality to the current account
func (sw *SettingsWindow) addPersonality() {
	if sw.selectedAccount == nil {
		dialog.ShowInformation("No Account", "Please select an account first", sw.window)
		return
	}

	// Create a new personality with default values
	newPersonality := config.Personality{
		Name:        "New Persona",
		Email:       sw.selectedAccount.Email, // Default to account email
		DisplayName: sw.selectedAccount.DisplayName,
		Signature:   "",
		IsDefault:   false,
	}

	// Add to account
	sw.selectedAccount.Personalities = append(sw.selectedAccount.Personalities, newPersonality)

	// Refresh list and select the new personality
	sw.personalityList.Refresh()
	newIndex := len(sw.selectedAccount.Personalities) - 1
	sw.personalityList.Select(newIndex)
}

// removePersonality removes the selected personality
func (sw *SettingsWindow) removePersonality() {
	if sw.selectedAccount == nil || sw.selectedPersonality == nil {
		dialog.ShowInformation("No Selection", "Please select a persona to remove", sw.window)
		return
	}

	// Find the index of the selected personality
	selectedIndex := -1
	for i, personality := range sw.selectedAccount.Personalities {
		if &personality == sw.selectedPersonality {
			selectedIndex = i
			break
		}
	}

	if selectedIndex == -1 {
		dialog.ShowError(fmt.Errorf("selected persona not found"), sw.window)
		return
	}

	// Confirm deletion
	dialog.ShowConfirm("Remove Persona",
		fmt.Sprintf("Are you sure you want to remove the persona '%s'?", sw.selectedPersonality.Name),
		func(confirmed bool) {
			if confirmed {
				// Remove from slice
				sw.selectedAccount.Personalities = append(
					sw.selectedAccount.Personalities[:selectedIndex],
					sw.selectedAccount.Personalities[selectedIndex+1:]...)

				// Clear selection
				sw.selectedPersonality = nil
				sw.loadPersonalitySettings()

				// Refresh list
				sw.personalityList.Refresh()
				sw.personalityList.UnselectAll()
			}
		}, sw.window)
}

// importContacts handles contact import from CSV file
func (sw *SettingsWindow) importContacts() {
	if sw.addressbookMgr == nil {
		dialog.ShowError(fmt.Errorf("addressbook manager not available"), sw.window)
		return
	}

	// Show file dialog to select CSV file
	fileDialog := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil {
			dialog.ShowError(fmt.Errorf("failed to open file: %w", err), sw.window)
			return
		}
		if reader == nil {
			return // User cancelled
		}
		defer reader.Close()

		// Show account selection dialog
		accounts := sw.config.Accounts
		if len(accounts) == 0 {
			dialog.ShowError(fmt.Errorf("no accounts configured"), sw.window)
			return
		}

		accountNames := make([]string, len(accounts))
		for i, account := range accounts {
			accountNames[i] = account.Name
		}

		accountSelect := widget.NewSelect(accountNames, nil)
		accountSelect.SetSelected(accountNames[0])

		replaceCheck := widget.NewCheck("Replace existing contacts", nil)

		form := container.NewVBox(
			widget.NewLabel("Select account for imported contacts:"),
			accountSelect,
			replaceCheck,
		)

		dialog.ShowCustomConfirm("Import Contacts", "Import", "Cancel", form, func(confirmed bool) {
			if !confirmed {
				return
			}

			selectedAccount := accountSelect.Selected
			replaceExisting := replaceCheck.Checked

			// Import contacts
			imported, err := sw.addressbookMgr.ImportContactsFromCSV(reader, selectedAccount, replaceExisting)
			if err != nil {
				dialog.ShowError(fmt.Errorf("failed to import contacts: %w", err), sw.window)
				return
			}

			dialog.ShowInformation("Import Complete",
				fmt.Sprintf("Successfully imported %d contacts", imported), sw.window)
		}, sw.window)

	}, sw.window)

	fileDialog.Show()
}

// exportContacts handles contact export to CSV file
func (sw *SettingsWindow) exportContacts() {
	if sw.addressbookMgr == nil {
		dialog.ShowError(fmt.Errorf("addressbook manager not available"), sw.window)
		return
	}

	// Show account selection dialog
	accounts := sw.config.Accounts
	if len(accounts) == 0 {
		dialog.ShowError(fmt.Errorf("no accounts configured"), sw.window)
		return
	}

	accountNames := make([]string, len(accounts))
	accountNames = append(accountNames, "All Accounts") // Add option for all accounts
	for i, account := range accounts {
		accountNames[i+1] = account.Name
	}

	accountSelect := widget.NewSelect(accountNames, nil)
	accountSelect.SetSelected(accountNames[0])

	form := container.NewVBox(
		widget.NewLabel("Select account to export:"),
		accountSelect,
	)

	dialog.ShowCustomConfirm("Export Contacts", "Export", "Cancel", form, func(confirmed bool) {
		if !confirmed {
			return
		}

		selectedAccount := ""
		if accountSelect.Selected != "All Accounts" {
			selectedAccount = accountSelect.Selected
		}

		// Show file save dialog
		fileDialog := dialog.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
			if err != nil {
				dialog.ShowError(fmt.Errorf("failed to create file: %w", err), sw.window)
				return
			}
			if writer == nil {
				return // User cancelled
			}
			defer writer.Close()

			// Export contacts
			exportErr := sw.addressbookMgr.ExportContactsToCSV(writer, selectedAccount)
			if exportErr != nil {
				dialog.ShowError(fmt.Errorf("failed to export contacts: %w", exportErr), sw.window)
				return
			}

			dialog.ShowInformation("Export Complete", "Contacts exported successfully", sw.window)
		}, sw.window)

		// Set default filename
		filename := "contacts.csv"
		if selectedAccount != "" {
			filename = fmt.Sprintf("contacts_%s.csv", selectedAccount)
		}
		fileDialog.SetFileName(filename)
		fileDialog.Show()

	}, sw.window)
}

// Show displays the settings window
func (sw *SettingsWindow) Show() {
	sw.window.Show()
}
