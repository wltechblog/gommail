// Package controllers provides UI controller implementations for managing
// different aspects of the main window functionality.
package controllers

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/wltechblog/gommail/internal/email"
	"github.com/wltechblog/gommail/pkg/imap"
	"github.com/wltechblog/gommail/pkg/smtp"
)

// MessageListControllerImpl implements the MessageListController interface.
// It manages message list display, sorting, and message-related operations.
type MessageListControllerImpl struct {
	// UI components
	messageList      *widget.List
	sortByButton     *widget.Button
	sortOrderButton  *widget.Button
	dateHeaderBtn    *widget.Button
	senderHeaderBtn  *widget.Button
	subjectHeaderBtn *widget.Button

	// Data
	messages []email.MessageIndexItem

	// Sorting state
	sortBy    SortBy
	sortOrder SortOrder

	// Dependencies
	accountController AccountController
	imapClient        *imap.ClientWrapper
	smtpClient        *smtp.Client
	statusBar         *widget.Label
	window            fyne.Window

	// Callbacks
	onMessagesChanged  func()
	onRefreshRequested func()
}

// NewMessageListController creates a new MessageListController.
func NewMessageListController(
	accountController AccountController,
	statusBar *widget.Label,
	window fyne.Window,
) *MessageListControllerImpl {
	return &MessageListControllerImpl{
		messages:          make([]email.MessageIndexItem, 0),
		sortBy:            SortByDate,
		sortOrder:         SortDescending,
		accountController: accountController,
		statusBar:         statusBar,
		window:            window,
	}
}

// SetIMAPClient sets the IMAP client for message operations.
func (mlc *MessageListControllerImpl) SetIMAPClient(client *imap.ClientWrapper) {
	mlc.imapClient = client
}

// SetSMTPClient sets the SMTP client for message operations.
func (mlc *MessageListControllerImpl) SetSMTPClient(client *smtp.Client) {
	mlc.smtpClient = client
}

// SetMessageList sets the message list widget.
func (mlc *MessageListControllerImpl) SetMessageList(list *widget.List) {
	mlc.messageList = list
}

// SetSortButtons sets the sort control buttons.
func (mlc *MessageListControllerImpl) SetSortButtons(sortByButton, sortOrderButton *widget.Button) {
	mlc.sortByButton = sortByButton
	mlc.sortOrderButton = sortOrderButton
}

// SetHeaderButtons sets the column header buttons.
func (mlc *MessageListControllerImpl) SetHeaderButtons(dateBtn, senderBtn, subjectBtn *widget.Button) {
	mlc.dateHeaderBtn = dateBtn
	mlc.senderHeaderBtn = senderBtn
	mlc.subjectHeaderBtn = subjectBtn
}

// SetOnMessagesChanged sets the callback for when messages change.
func (mlc *MessageListControllerImpl) SetOnMessagesChanged(callback func()) {
	mlc.onMessagesChanged = callback
}

// SetOnRefreshRequested sets the callback used to repaint the visible message list widget.
func (mlc *MessageListControllerImpl) SetOnRefreshRequested(callback func()) {
	mlc.onRefreshRequested = callback
}

// GetMessages returns the current message list.
func (mlc *MessageListControllerImpl) GetMessages() []email.MessageIndexItem {
	return mlc.messages
}

// SetMessages sets the message list.
func (mlc *MessageListControllerImpl) SetMessages(messages []email.MessageIndexItem) {
	mlc.messages = messages
	if mlc.onMessagesChanged != nil {
		mlc.onMessagesChanged()
	}
}

// RefreshMessageList refreshes the message list display.
func (mlc *MessageListControllerImpl) RefreshMessageList() {
	if mlc.onRefreshRequested != nil {
		mlc.onRefreshRequested()
		return
	}
	if mlc.messageList != nil {
		mlc.messageList.Refresh()
	}
}

// SortMessages sorts the messages based on current sort criteria.
func (mlc *MessageListControllerImpl) SortMessages() {
	if len(mlc.messages) == 0 {
		return
	}

	// Sort the messages array
	sort.Slice(mlc.messages, func(i, j int) bool {
		msgI := &mlc.messages[i]
		msgJ := &mlc.messages[j]

		var result bool

		switch mlc.sortBy {
		case SortByDate:
			// Use UTC-normalized dates for consistent sorting across timezones
			dateI := mlc.getSortDate(&msgI.Message)
			dateJ := mlc.getSortDate(&msgJ.Message)
			result = dateI.Before(dateJ)
		case SortBySender:
			senderI := mlc.GetSenderNameFromIndexItem(*msgI)
			senderJ := mlc.GetSenderNameFromIndexItem(*msgJ)
			result = strings.ToLower(senderI) < strings.ToLower(senderJ)
		case SortBySubject:
			result = strings.ToLower(msgI.Message.Subject) < strings.ToLower(msgJ.Message.Subject)
		default:
			// Default to date sorting using UTC-normalized dates
			dateI := mlc.getSortDate(&msgI.Message)
			dateJ := mlc.getSortDate(&msgJ.Message)
			result = dateI.Before(dateJ)
		}

		if mlc.sortOrder == SortDescending {
			result = !result
		}

		return result
	})
}

// getSortDate returns the date for sorting purposes, normalized to UTC.
func (mlc *MessageListControllerImpl) getSortDate(msg *email.Message) time.Time {
	var sortDate time.Time

	// Use InternalDate if available (actual arrival date from IMAP server)
	if !msg.InternalDate.IsZero() {
		sortDate = msg.InternalDate
	} else {
		// Fallback to Date header
		sortDate = msg.Date
	}

	// Normalize to UTC for consistent sorting
	return sortDate.UTC()
}

// SetSortBy changes the sort criteria and refreshes the message list.
func (mlc *MessageListControllerImpl) SetSortBy(sortBy SortBy) {
	// If clicking the same sort criteria, toggle the order
	if mlc.sortBy == sortBy {
		if mlc.sortOrder == SortAscending {
			mlc.sortOrder = SortDescending
		} else {
			mlc.sortOrder = SortAscending
		}
	} else {
		// New sort criteria, use default order
		mlc.sortBy = sortBy
		switch sortBy {
		case SortByDate:
			mlc.sortOrder = SortDescending // Newest first for dates
		case SortBySender, SortBySubject:
			mlc.sortOrder = SortAscending // A-Z for text
		}
	}

	mlc.SortMessages()
	mlc.RefreshMessageList()

	// Force the list to update by clearing selection and refreshing again
	if mlc.messageList != nil {
		mlc.messageList.UnselectAll()
		mlc.messageList.Refresh()
	}

	mlc.UpdateSortButtons()
	mlc.UpdateColumnHeaders()

	// Update status bar
	sortName := mlc.GetSortName()
	orderName := "ascending"
	if mlc.sortOrder == SortDescending {
		orderName = "descending"
	}
	if mlc.statusBar != nil {
		mlc.statusBar.SetText(fmt.Sprintf("Messages sorted by %s (%s)", sortName, orderName))
	}
}

// GetSortBy returns the current sort criteria.
func (mlc *MessageListControllerImpl) GetSortBy() SortBy {
	return mlc.sortBy
}

// SetSortOrder sets the sort order.
func (mlc *MessageListControllerImpl) SetSortOrder(order SortOrder) {
	mlc.sortOrder = order
}

// GetSortOrder returns the current sort order.
func (mlc *MessageListControllerImpl) GetSortOrder() SortOrder {
	return mlc.sortOrder
}

// SetSortCriteria sets the sort criteria without triggering UI updates.
// This is useful for programmatic sorting without user interaction.
func (mlc *MessageListControllerImpl) SetSortCriteria(sortBy SortBy, sortOrder SortOrder) {
	mlc.sortBy = sortBy
	mlc.sortOrder = sortOrder
}

// ToggleSortOrder toggles between ascending and descending sort order.
func (mlc *MessageListControllerImpl) ToggleSortOrder() {
	if mlc.sortOrder == SortAscending {
		mlc.sortOrder = SortDescending
	} else {
		mlc.sortOrder = SortAscending
	}

	mlc.SortMessages()
	// Force the list to update by clearing selection and refreshing
	if mlc.messageList != nil {
		mlc.messageList.UnselectAll()
		mlc.messageList.Refresh()
	}
	mlc.UpdateSortButtons()
	mlc.UpdateColumnHeaders()

	orderName := "ascending"
	if mlc.sortOrder == SortDescending {
		orderName = "descending"
	}
	if mlc.statusBar != nil {
		mlc.statusBar.SetText(fmt.Sprintf("Sort order changed to %s", orderName))
	}
}

// GetSortName returns a human-readable name for the current sort criteria.
func (mlc *MessageListControllerImpl) GetSortName() string {
	switch mlc.sortBy {
	case SortByDate:
		return "date"
	case SortBySender:
		return "sender"
	case SortBySubject:
		return "subject"
	default:
		return "date"
	}
}

// UpdateSortButtons updates the sort button text to reflect current state.
func (mlc *MessageListControllerImpl) UpdateSortButtons() {
	if mlc.sortByButton != nil {
		sortName := mlc.GetSortName()
		mlc.sortByButton.SetText(fmt.Sprintf("Sort: %s", strings.Title(sortName)))
	}

	if mlc.sortOrderButton != nil {
		orderIcon := "↑"
		if mlc.sortOrder == SortDescending {
			orderIcon = "↓"
		}
		mlc.sortOrderButton.SetText(orderIcon)
	}
}

// UpdateColumnHeaders updates the column header buttons to show current sort state.
func (mlc *MessageListControllerImpl) UpdateColumnHeaders() {
	if mlc.dateHeaderBtn == nil || mlc.senderHeaderBtn == nil || mlc.subjectHeaderBtn == nil {
		return
	}

	// Reset all headers to normal state
	mlc.dateHeaderBtn.SetText("Date")
	mlc.senderHeaderBtn.SetText("Sender")
	mlc.subjectHeaderBtn.SetText("Subject")

	// Add sort indicator to the active column
	orderIcon := "↑"
	if mlc.sortOrder == SortDescending {
		orderIcon = "↓"
	}

	switch mlc.sortBy {
	case SortByDate:
		mlc.dateHeaderBtn.SetText(fmt.Sprintf("Date %s", orderIcon))
	case SortBySender:
		mlc.senderHeaderBtn.SetText(fmt.Sprintf("Sender %s", orderIcon))
	case SortBySubject:
		mlc.subjectHeaderBtn.SetText(fmt.Sprintf("Subject %s", orderIcon))
	}
}

// ShowSortMenu displays a menu to select sort criteria.
func (mlc *MessageListControllerImpl) ShowSortMenu() {
	dateButton := widget.NewButton("Date", func() {
		mlc.SetSortBy(SortByDate)
	})
	senderButton := widget.NewButton("Sender", func() {
		mlc.SetSortBy(SortBySender)
	})
	subjectButton := widget.NewButton("Subject", func() {
		mlc.SetSortBy(SortBySubject)
	})

	content := container.NewVBox(
		widget.NewLabel("Sort messages by:"),
		dateButton,
		senderButton,
		subjectButton,
	)

	popup := widget.NewPopUp(content, mlc.window.Canvas())
	popup.ShowAtPosition(fyne.NewPos(100, 100))

	// Auto-close after selection
	dateButton.OnTapped = func() {
		mlc.SetSortBy(SortByDate)
		popup.Hide()
	}
	senderButton.OnTapped = func() {
		mlc.SetSortBy(SortBySender)
		popup.Hide()
	}
	subjectButton.OnTapped = func() {
		mlc.SetSortBy(SortBySubject)
		popup.Hide()
	}
}

// GetSenderName extracts the sender name from a message.
func (mlc *MessageListControllerImpl) GetSenderName(msg email.Message) string {
	if len(msg.From) > 0 {
		if msg.From[0].Name != "" {
			return msg.From[0].Name
		}
		return msg.From[0].Email
	}
	return "Unknown Sender"
}

// GetSenderNameFromIndexItem extracts the sender name from a MessageIndexItem.
func (mlc *MessageListControllerImpl) GetSenderNameFromIndexItem(item email.MessageIndexItem) string {
	if len(item.Message.From) > 0 {
		if item.Message.From[0].Name != "" {
			return item.Message.From[0].Name
		}
		return item.Message.From[0].Email
	}
	return "Unknown Sender"
}

// ConvertMessagesToIndexItems converts regular messages to MessageIndexItem for the current account.
func (mlc *MessageListControllerImpl) ConvertMessagesToIndexItems(messages []email.Message, folderName string) []email.MessageIndexItem {
	currentAccount := mlc.accountController.GetCurrentAccount()
	if currentAccount == nil {
		return []email.MessageIndexItem{}
	}

	indexItems := make([]email.MessageIndexItem, len(messages))
	for i, msg := range messages {
		indexItems[i] = email.MessageIndexItem{
			Message:      msg,
			AccountName:  currentAccount.Name,
			AccountEmail: currentAccount.Email,
			FolderName:   folderName,
			IMAPClient:   mlc.imapClient,
			SMTPClient:   mlc.smtpClient,
			AccountConfig: &email.AccountConfig{
				Name:        currentAccount.Name,
				Email:       currentAccount.Email,
				DisplayName: currentAccount.DisplayName,
				IMAP: email.ServerConfig{
					Host:     currentAccount.IMAP.Host,
					Port:     currentAccount.IMAP.Port,
					Username: currentAccount.IMAP.Username,
					Password: currentAccount.IMAP.Password,
					TLS:      currentAccount.IMAP.TLS,
				},
				SMTP: email.ServerConfig{
					Host:     currentAccount.SMTP.Host,
					Port:     currentAccount.SMTP.Port,
					Username: currentAccount.SMTP.Username,
					Password: currentAccount.SMTP.Password,
					TLS:      currentAccount.SMTP.TLS,
				},
			},
		}
	}
	return indexItems
}

// GetMessageList returns the message list widget.
func (mlc *MessageListControllerImpl) GetMessageList() *widget.List {
	return mlc.messageList
}

// GetSortByButton returns the sort by button.
func (mlc *MessageListControllerImpl) GetSortByButton() *widget.Button {
	return mlc.sortByButton
}

// GetSortOrderButton returns the sort order button.
func (mlc *MessageListControllerImpl) GetSortOrderButton() *widget.Button {
	return mlc.sortOrderButton
}

// Note: Message operation methods (MoveToTrash, Delete, etc.) and compose methods
// will be implemented in a follow-up as they require more complex integration with
// MainWindow state and dialogs. For now, these are stub implementations that will
// be filled in during integration.

// MoveToTrash moves the selected message to trash (stub).
func (mlc *MessageListControllerImpl) MoveToTrash() {
	// TODO: Implement during integration with MainWindow
}

// MoveToTrashMultiple moves multiple selected messages to trash (stub).
func (mlc *MessageListControllerImpl) MoveToTrashMultiple() {
	// TODO: Implement during integration with MainWindow
}

// DeleteMessage permanently deletes the selected message (stub).
func (mlc *MessageListControllerImpl) DeleteMessage() {
	// TODO: Implement during integration with MainWindow
}

// DeleteMessagesMultiple permanently deletes multiple selected messages (stub).
func (mlc *MessageListControllerImpl) DeleteMessagesMultiple() {
	// TODO: Implement during integration with MainWindow
}

// MoveToFolder moves the selected message to a folder (stub).
func (mlc *MessageListControllerImpl) MoveToFolder(targetFolder string) {
	// TODO: Implement during integration with MainWindow
}

// ShowMoveToFolderDialog shows a dialog to select a folder (stub).
func (mlc *MessageListControllerImpl) ShowMoveToFolderDialog() {
	// TODO: Implement during integration with MainWindow
}

// ShowMoveToFolderDialogMultiple shows a dialog for multiple messages (stub).
func (mlc *MessageListControllerImpl) ShowMoveToFolderDialogMultiple() {
	// TODO: Implement during integration with MainWindow
}

// ComposeMessage opens the compose dialog (stub).
func (mlc *MessageListControllerImpl) ComposeMessage() {
	// TODO: Implement during integration with MainWindow
}

// ReplyToMessage replies to the selected message (stub).
func (mlc *MessageListControllerImpl) ReplyToMessage() {
	// TODO: Implement during integration with MainWindow
}

// ReplyAllToMessage replies to all recipients (stub).
func (mlc *MessageListControllerImpl) ReplyAllToMessage() {
	// TODO: Implement during integration with MainWindow
}

// ForwardMessage forwards the selected message (stub).
func (mlc *MessageListControllerImpl) ForwardMessage() {
	// TODO: Implement during integration with MainWindow
}
