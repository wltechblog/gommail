package ui

import (
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/wltechblog/gommail/internal/config"
	"github.com/wltechblog/gommail/internal/email"
)

// SearchWindow represents the search interface window
type SearchWindow struct {
	app    fyne.App
	window fyne.Window
	config *config.Config

	// Search components
	searchEntry    *widget.Entry
	fromEntry      *widget.Entry
	toEntry        *widget.Entry
	subjectEntry   *widget.Entry
	dateFromEntry  *widget.Entry
	dateToEntry    *widget.Entry
	folderSelect   *widget.Select
	hasAttachments *widget.Check
	unreadOnly     *widget.Check
	serverSearch   *widget.Check

	// Results
	resultsList *widget.List
	statusBar   *widget.Label

	// Data
	searchResults   []email.Message
	selectedMessage *email.Message

	// IMAP client for search operations
	imapClient email.IMAPClient

	// Callbacks
	onMessageSelected func(*email.Message)
	onClosed          func()
}

// SearchOptions contains options for creating a search window
type SearchOptions struct {
	Account           *config.Account
	IMAPClient        email.IMAPClient
	OnMessageSelected func(*email.Message)
	OnClosed          func()
}

// NewSearchWindow creates a new search window
func NewSearchWindow(app fyne.App, cfg *config.Config, opts SearchOptions) *SearchWindow {
	window := app.NewWindow("Search Messages")
	window.Resize(fyne.NewSize(900, 600))

	sw := &SearchWindow{
		app:               app,
		window:            window,
		config:            cfg,
		searchResults:     make([]email.Message, 0),
		imapClient:        opts.IMAPClient,
		onMessageSelected: opts.OnMessageSelected,
		onClosed:          opts.OnClosed,
	}

	sw.setupUI()
	sw.setupKeyboardShortcuts()

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
func (sw *SearchWindow) setupUI() {
	// Search form
	sw.searchEntry = widget.NewEntry()
	sw.searchEntry.SetPlaceHolder("Search in message content...")
	sw.searchEntry.OnSubmitted = func(text string) {
		sw.performSearch()
	}

	sw.fromEntry = widget.NewEntry()
	sw.fromEntry.SetPlaceHolder("sender@example.com")

	sw.toEntry = widget.NewEntry()
	sw.toEntry.SetPlaceHolder("recipient@example.com")

	sw.subjectEntry = widget.NewEntry()
	sw.subjectEntry.SetPlaceHolder("Subject contains...")

	sw.dateFromEntry = widget.NewEntry()
	sw.dateFromEntry.SetPlaceHolder("YYYY-MM-DD")

	sw.dateToEntry = widget.NewEntry()
	sw.dateToEntry.SetPlaceHolder("YYYY-MM-DD")

	// Initialize folder selection with dynamic folder list
	sw.folderSelect = widget.NewSelect([]string{"All Folders"}, nil)
	sw.folderSelect.SetSelected("All Folders")
	sw.populateFolderList()

	sw.hasAttachments = widget.NewCheck("Has attachments", nil)
	sw.unreadOnly = widget.NewCheck("Unread only", nil)
	sw.serverSearch = widget.NewCheck("Search server (slower but more complete)", nil)

	// Search form layout
	searchForm := container.NewVBox(
		widget.NewLabel("Search Criteria"),
		container.NewGridWithColumns(2,
			widget.NewLabel("Content:"), sw.searchEntry,
			widget.NewLabel("From:"), sw.fromEntry,
			widget.NewLabel("To:"), sw.toEntry,
			widget.NewLabel("Subject:"), sw.subjectEntry,
			widget.NewLabel("Date From:"), sw.dateFromEntry,
			widget.NewLabel("Date To:"), sw.dateToEntry,
			widget.NewLabel("Folder:"), sw.folderSelect,
		),
		container.NewHBox(sw.hasAttachments, sw.unreadOnly),
		sw.serverSearch,
	)

	// Search buttons
	searchButton := widget.NewButtonWithIcon("Search", theme.SearchIcon(), func() {
		sw.performSearch()
	})
	clearButton := widget.NewButtonWithIcon("Clear", theme.ContentClearIcon(), func() {
		sw.clearSearch()
	})

	searchButtons := container.NewHBox(searchButton, clearButton)

	// Results list
	sw.resultsList = widget.NewList(
		func() int {
			return len(sw.searchResults)
		},
		func() fyne.CanvasObject {
			return container.NewVBox(
				container.NewHBox(
					widget.NewIcon(theme.MailComposeIcon()),
					widget.NewLabel("Subject"),
					widget.NewLabel(""), // Unread indicator
				),
				container.NewHBox(
					widget.NewLabel("From:"),
					widget.NewLabel("sender@example.com"),
				),
				container.NewHBox(
					widget.NewLabel("Date:"),
					widget.NewLabel("2024-01-01"),
				),
			)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id < len(sw.searchResults) {
				sw.updateResultItem(id, obj)
			}
		},
	)

	sw.resultsList.OnSelected = func(id widget.ListItemID) {
		if id < len(sw.searchResults) {
			sw.selectedMessage = &sw.searchResults[id]

			if sw.onMessageSelected != nil {
				sw.onMessageSelected(sw.selectedMessage)
			}

			// Close the search window after selection
			sw.window.Close()
		}
	}

	// Status bar
	sw.statusBar = widget.NewLabel("Ready to search")

	// Layout - single panel without preview
	mainPanel := container.NewVBox(
		searchForm,
		searchButtons,
		widget.NewSeparator(),
		widget.NewLabel("Search Results (double-click to open in main window)"),
		sw.resultsList,
	)

	content := container.NewBorder(
		nil, sw.statusBar, nil, nil,
		mainPanel,
	)

	sw.window.SetContent(content)
}

// performSearch executes the search with current criteria
func (sw *SearchWindow) performSearch() {
	sw.statusBar.SetText("Searching...")

	// Build search criteria
	criteria := email.SearchCriteria{
		Content:        sw.searchEntry.Text,
		From:           sw.fromEntry.Text,
		To:             sw.toEntry.Text,
		Subject:        sw.subjectEntry.Text,
		DateFrom:       sw.parseDate(sw.dateFromEntry.Text),
		DateTo:         sw.parseDate(sw.dateToEntry.Text),
		Folder:         sw.folderSelect.Selected,
		HasAttachments: sw.hasAttachments.Checked,
		UnreadOnly:     sw.unreadOnly.Checked,
		SearchServer:   sw.serverSearch.Checked,
		MaxResults:     100, // Limit results to avoid overwhelming the UI
	}

	// Validate search criteria
	if !sw.hasValidSearchCriteria(criteria) {
		sw.statusBar.SetText("Please enter at least one search criterion")
		return
	}

	// Perform search asynchronously to avoid blocking the UI
	go func() {
		var results []email.Message
		var err error

		if sw.imapClient == nil {
			sw.statusBar.SetText("Error: No IMAP client available")
			return
		}

		// Update status to show search type
		searchType := "cache"
		if criteria.SearchServer {
			searchType = "server"
		}
		sw.statusBar.SetText(fmt.Sprintf("Searching %s...", searchType))

		// Perform the search
		if criteria.Folder != "" && criteria.Folder != "All Folders" {
			results, err = sw.imapClient.SearchMessagesInFolder(criteria.Folder, criteria)
		} else {
			results, err = sw.imapClient.SearchMessages(criteria)
		}

		// Update UI on main thread
		if err != nil {
			sw.statusBar.SetText(fmt.Sprintf("Search failed: %v", err))
			return
		}

		// Sort results by date (newest first)
		sw.sortResultsByDate(results)

		sw.searchResults = results
		sw.resultsList.Refresh()

		// Update status with more detailed information
		if len(results) == 0 {
			sw.statusBar.SetText("No messages found matching your criteria")
		} else if len(results) == criteria.MaxResults {
			sw.statusBar.SetText(fmt.Sprintf("Found %d messages (limited to %d results)", len(results), criteria.MaxResults))
		} else {
			sw.statusBar.SetText(fmt.Sprintf("Found %d messages", len(results)))
		}
	}()
}

// clearSearch clears all search criteria and results
func (sw *SearchWindow) clearSearch() {
	sw.searchEntry.SetText("")
	sw.fromEntry.SetText("")
	sw.toEntry.SetText("")
	sw.subjectEntry.SetText("")
	sw.dateFromEntry.SetText("")
	sw.dateToEntry.SetText("")
	sw.folderSelect.SetSelected("All Folders")
	sw.hasAttachments.SetChecked(false)
	sw.unreadOnly.SetChecked(false)
	sw.serverSearch.SetChecked(false)

	sw.searchResults = make([]email.Message, 0)
	sw.selectedMessage = nil
	sw.resultsList.Refresh()
	sw.statusBar.SetText("Search cleared")
}

// Show displays the search window
func (sw *SearchWindow) Show() {
	sw.window.Show()
}

// updateResultItem updates a search result list item
func (sw *SearchWindow) updateResultItem(id widget.ListItemID, obj fyne.CanvasObject) {
	if id >= len(sw.searchResults) {
		return
	}

	msg := sw.searchResults[id]
	vbox := obj.(*fyne.Container)

	// Update subject line
	subjectBox := vbox.Objects[0].(*fyne.Container)
	subjectLabel := subjectBox.Objects[1].(*widget.Label)
	subjectLabel.SetText(msg.Subject)

	// Update unread indicator
	unreadLabel := subjectBox.Objects[2].(*widget.Label)
	isRead := false
	for _, flag := range msg.Flags {
		if flag == "\\Seen" {
			isRead = true
			break
		}
	}
	if !isRead {
		unreadLabel.SetText("●")
	} else {
		unreadLabel.SetText("")
	}

	// Update from line
	fromBox := vbox.Objects[1].(*fyne.Container)
	fromLabel := fromBox.Objects[1].(*widget.Label)
	if len(msg.From) > 0 {
		fromLabel.SetText(msg.From[0].Name)
	}

	// Update date
	dateBox := vbox.Objects[2].(*fyne.Container)
	dateLabel := dateBox.Objects[1].(*widget.Label)
	dateLabel.SetText(msg.Date.Format("Jan 2, 15:04"))
}

// formatAddresses formats a slice of addresses for display
func (sw *SearchWindow) formatAddresses(addresses []email.Address) string {
	if len(addresses) == 0 {
		return ""
	}

	var formatted []string
	for _, addr := range addresses {
		if addr.Name != "" {
			formatted = append(formatted, fmt.Sprintf("%s <%s>", addr.Name, addr.Email))
		} else {
			formatted = append(formatted, addr.Email)
		}
	}

	return strings.Join(formatted, ", ")
}

// parseDate parses a date string in YYYY-MM-DD format
func (sw *SearchWindow) parseDate(dateStr string) *time.Time {
	if strings.TrimSpace(dateStr) == "" {
		return nil
	}

	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return nil
	}

	return &date
}

// hasValidSearchCriteria checks if at least one search criterion is provided
func (sw *SearchWindow) hasValidSearchCriteria(criteria email.SearchCriteria) bool {
	return criteria.Content != "" ||
		criteria.Subject != "" ||
		criteria.From != "" ||
		criteria.To != "" ||
		criteria.DateFrom != nil ||
		criteria.DateTo != nil ||
		criteria.HasAttachments ||
		criteria.UnreadOnly ||
		len(criteria.Keywords) > 0
}

// sortResultsByDate sorts search results by date (newest first)
func (sw *SearchWindow) sortResultsByDate(results []email.Message) {
	// Sort by date in descending order (newest first)
	// Use UTC normalized dates for consistent sorting across timezones
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			// Get the appropriate date for each message (prefer InternalDate over Date)
			dateI := results[i].InternalDate
			if dateI.IsZero() {
				dateI = results[i].Date
			}
			dateJ := results[j].InternalDate
			if dateJ.IsZero() {
				dateJ = results[j].Date
			}

			// Compare in UTC to handle timezone differences properly
			if dateI.UTC().Before(dateJ.UTC()) {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
}

// populateFolderList populates the folder selection dropdown with available folders
func (sw *SearchWindow) populateFolderList() {
	if sw.imapClient == nil {
		return
	}

	// Get folders asynchronously to avoid blocking UI
	go func() {
		folders, err := sw.imapClient.ListSubscribedFolders()
		if err != nil {
			// If we can't get folders, just use the default list
			return
		}

		// Build folder list
		folderNames := []string{"All Folders"}
		for _, folder := range folders {
			folderNames = append(folderNames, folder.Name)
		}

		// Update UI on main thread
		sw.folderSelect.Options = folderNames
		sw.folderSelect.Refresh()
	}()
}

// setupKeyboardShortcuts sets up keyboard shortcuts for the search window
func (sw *SearchWindow) setupKeyboardShortcuts() {
	// Enter key to perform search
	sw.window.Canvas().SetOnTypedKey(func(key *fyne.KeyEvent) {
		if key.Name == fyne.KeyReturn || key.Name == fyne.KeyEnter {
			sw.performSearch()
		} else if key.Name == fyne.KeyEscape {
			sw.clearSearch()
		}
	})
}
