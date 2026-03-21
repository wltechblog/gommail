package controllers

import (
	"context"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/wltechblog/gommail/internal/config"
	"github.com/wltechblog/gommail/internal/email"
	"github.com/wltechblog/gommail/pkg/imap"
)

// mockAccountController is a mock implementation of AccountController for testing.
type mockAccountController struct {
	currentAccount *config.Account
	isUnifiedInbox bool
}

func (m *mockAccountController) GetCurrentAccount() *config.Account {
	return m.currentAccount
}

func (m *mockAccountController) SetCurrentAccount(account *config.Account) {
	m.currentAccount = account
}

func (m *mockAccountController) ClearCurrentAccount() {
	m.currentAccount = nil
}

func (m *mockAccountController) IsUnifiedInbox() bool {
	return m.isUnifiedInbox
}

func (m *mockAccountController) SetUnifiedInbox(enabled bool) {
	m.isUnifiedInbox = enabled
}

// Stub implementations for other methods
func (m *mockAccountController) GetOrCreateIMAPClient(account *config.Account) (*imap.ClientWrapper, error) {
	return nil, nil
}
func (m *mockAccountController) GetIMAPClientForAccount(accountName string) (*imap.ClientWrapper, bool) {
	return nil, false
}
func (m *mockAccountController) StoreIMAPClient(accountName string, client *imap.ClientWrapper) {}
func (m *mockAccountController) CloseAllClients()                                               {}
func (m *mockAccountController) CloseClientForAccount(accountName string)                       {}
func (m *mockAccountController) ForEachClient(fn func(accountName string, client *imap.ClientWrapper)) {
}
func (m *mockAccountController) StartUnifiedInboxMonitoring(ctx context.Context) {}
func (m *mockAccountController) StopUnifiedInboxMonitoring()                     {}
func (m *mockAccountController) IsMonitoringUnifiedInbox() bool                  { return false }
func (m *mockAccountController) InvalidateUnifiedInboxCache()                    {}
func (m *mockAccountController) CacheAccountMessages(accountName string, messages []email.Message) {
}

func TestNewMessageListController(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	window := test.NewWindow(nil)
	defer window.Close()

	statusBar := widget.NewLabel("Ready")
	accountController := &mockAccountController{}

	mlc := NewMessageListController(accountController, statusBar, window)

	if mlc == nil {
		t.Fatal("NewMessageListController returned nil")
	}

	if mlc.sortBy != SortByDate {
		t.Errorf("Expected default sortBy to be SortByDate, got %v", mlc.sortBy)
	}

	if mlc.sortOrder != SortDescending {
		t.Errorf("Expected default sortOrder to be SortDescending, got %v", mlc.sortOrder)
	}

	if len(mlc.messages) != 0 {
		t.Errorf("Expected empty messages array, got %d messages", len(mlc.messages))
	}
}

func TestGetSetMessages(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	window := test.NewWindow(nil)
	defer window.Close()

	statusBar := widget.NewLabel("Ready")
	accountController := &mockAccountController{}

	mlc := NewMessageListController(accountController, statusBar, window)

	// Create test messages
	messages := []email.MessageIndexItem{
		{
			Message: email.Message{
				UID:     1,
				Subject: "Test Message 1",
			},
		},
		{
			Message: email.Message{
				UID:     2,
				Subject: "Test Message 2",
			},
		},
	}

	// Test SetMessages
	mlc.SetMessages(messages)

	// Test GetMessages
	retrieved := mlc.GetMessages()
	if len(retrieved) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(retrieved))
	}

	if retrieved[0].Message.Subject != "Test Message 1" {
		t.Errorf("Expected first message subject 'Test Message 1', got '%s'", retrieved[0].Message.Subject)
	}
}

func TestRefreshMessageListUsesCallbackWhenConfigured(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	window := test.NewWindow(nil)
	defer window.Close()

	statusBar := widget.NewLabel("Ready")
	accountController := &mockAccountController{}

	mlc := NewMessageListController(accountController, statusBar, window)
	called := false
	mlc.SetOnRefreshRequested(func() {
		called = true
	})

	mlc.RefreshMessageList()

	if !called {
		t.Fatal("expected refresh callback to be invoked")
	}
}

func TestSortMessages(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	window := test.NewWindow(nil)
	defer window.Close()

	statusBar := widget.NewLabel("Ready")
	accountController := &mockAccountController{}

	mlc := NewMessageListController(accountController, statusBar, window)

	// Create test messages with different dates
	now := time.Now()
	messages := []email.MessageIndexItem{
		{
			Message: email.Message{
				UID:          1,
				Subject:      "Oldest",
				InternalDate: now.Add(-2 * time.Hour),
				From: []email.Address{
					{Name: "Charlie", Email: "charlie@example.com"},
				},
			},
		},
		{
			Message: email.Message{
				UID:          2,
				Subject:      "Newest",
				InternalDate: now,
				From: []email.Address{
					{Name: "Alice", Email: "alice@example.com"},
				},
			},
		},
		{
			Message: email.Message{
				UID:          3,
				Subject:      "Middle",
				InternalDate: now.Add(-1 * time.Hour),
				From: []email.Address{
					{Name: "Bob", Email: "bob@example.com"},
				},
			},
		},
	}

	mlc.SetMessages(messages)

	// Test sorting by date (descending - newest first)
	mlc.sortBy = SortByDate
	mlc.sortOrder = SortDescending
	mlc.SortMessages()

	if mlc.messages[0].Message.Subject != "Newest" {
		t.Errorf("Expected first message to be 'Newest', got '%s'", mlc.messages[0].Message.Subject)
	}
	if mlc.messages[2].Message.Subject != "Oldest" {
		t.Errorf("Expected last message to be 'Oldest', got '%s'", mlc.messages[2].Message.Subject)
	}

	// Test sorting by date (ascending - oldest first)
	mlc.sortOrder = SortAscending
	mlc.SortMessages()

	if mlc.messages[0].Message.Subject != "Oldest" {
		t.Errorf("Expected first message to be 'Oldest', got '%s'", mlc.messages[0].Message.Subject)
	}
	if mlc.messages[2].Message.Subject != "Newest" {
		t.Errorf("Expected last message to be 'Newest', got '%s'", mlc.messages[2].Message.Subject)
	}

	// Test sorting by sender (ascending)
	mlc.sortBy = SortBySender
	mlc.sortOrder = SortAscending
	mlc.SortMessages()

	if mlc.GetSenderNameFromIndexItem(mlc.messages[0]) != "Alice" {
		t.Errorf("Expected first sender to be 'Alice', got '%s'", mlc.GetSenderNameFromIndexItem(mlc.messages[0]))
	}
	if mlc.GetSenderNameFromIndexItem(mlc.messages[2]) != "Charlie" {
		t.Errorf("Expected last sender to be 'Charlie', got '%s'", mlc.GetSenderNameFromIndexItem(mlc.messages[2]))
	}

	// Test sorting by subject (ascending)
	mlc.sortBy = SortBySubject
	mlc.sortOrder = SortAscending
	mlc.SortMessages()

	if mlc.messages[0].Message.Subject != "Middle" {
		t.Errorf("Expected first subject to be 'Middle', got '%s'", mlc.messages[0].Message.Subject)
	}
	if mlc.messages[2].Message.Subject != "Oldest" {
		t.Errorf("Expected last subject to be 'Oldest', got '%s'", mlc.messages[2].Message.Subject)
	}
}

func TestSetSortBy(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	window := test.NewWindow(nil)
	defer window.Close()

	statusBar := widget.NewLabel("Ready")
	accountController := &mockAccountController{}

	mlc := NewMessageListController(accountController, statusBar, window)

	// Test changing sort criteria
	mlc.SetSortBy(SortBySender)

	if mlc.sortBy != SortBySender {
		t.Errorf("Expected sortBy to be SortBySender, got %v", mlc.sortBy)
	}

	if mlc.sortOrder != SortAscending {
		t.Errorf("Expected sortOrder to be SortAscending for sender, got %v", mlc.sortOrder)
	}

	// Test toggling sort order by clicking same criteria
	mlc.SetSortBy(SortBySender)

	if mlc.sortOrder != SortDescending {
		t.Errorf("Expected sortOrder to toggle to SortDescending, got %v", mlc.sortOrder)
	}
}

func TestToggleSortOrder(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	window := test.NewWindow(nil)
	defer window.Close()

	statusBar := widget.NewLabel("Ready")
	accountController := &mockAccountController{}

	mlc := NewMessageListController(accountController, statusBar, window)

	// Initial state is descending
	if mlc.sortOrder != SortDescending {
		t.Errorf("Expected initial sortOrder to be SortDescending, got %v", mlc.sortOrder)
	}

	// Toggle to ascending
	mlc.ToggleSortOrder()

	if mlc.sortOrder != SortAscending {
		t.Errorf("Expected sortOrder to be SortAscending after toggle, got %v", mlc.sortOrder)
	}

	// Toggle back to descending
	mlc.ToggleSortOrder()

	if mlc.sortOrder != SortDescending {
		t.Errorf("Expected sortOrder to be SortDescending after second toggle, got %v", mlc.sortOrder)
	}
}

func TestGetSortName(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	window := test.NewWindow(nil)
	defer window.Close()

	statusBar := widget.NewLabel("Ready")
	accountController := &mockAccountController{}

	mlc := NewMessageListController(accountController, statusBar, window)

	tests := []struct {
		sortBy   SortBy
		expected string
	}{
		{SortByDate, "date"},
		{SortBySender, "sender"},
		{SortBySubject, "subject"},
	}

	for _, tt := range tests {
		mlc.sortBy = tt.sortBy
		result := mlc.GetSortName()
		if result != tt.expected {
			t.Errorf("For sortBy %v, expected '%s', got '%s'", tt.sortBy, tt.expected, result)
		}
	}
}

func TestGetSenderName(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	window := test.NewWindow(nil)
	defer window.Close()

	statusBar := widget.NewLabel("Ready")
	accountController := &mockAccountController{}

	mlc := NewMessageListController(accountController, statusBar, window)

	tests := []struct {
		name     string
		message  email.Message
		expected string
	}{
		{
			name: "With name and email",
			message: email.Message{
				From: []email.Address{
					{Name: "John Doe", Email: "john@example.com"},
				},
			},
			expected: "John Doe",
		},
		{
			name: "With email only",
			message: email.Message{
				From: []email.Address{
					{Email: "jane@example.com"},
				},
			},
			expected: "jane@example.com",
		},
		{
			name:     "No sender",
			message:  email.Message{},
			expected: "Unknown Sender",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mlc.GetSenderName(tt.message)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestGetSenderNameFromIndexItem(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	window := test.NewWindow(nil)
	defer window.Close()

	statusBar := widget.NewLabel("Ready")
	accountController := &mockAccountController{}

	mlc := NewMessageListController(accountController, statusBar, window)

	tests := []struct {
		name     string
		item     email.MessageIndexItem
		expected string
	}{
		{
			name: "With name and email",
			item: email.MessageIndexItem{
				Message: email.Message{
					From: []email.Address{
						{Name: "Alice Smith", Email: "alice@example.com"},
					},
				},
			},
			expected: "Alice Smith",
		},
		{
			name: "With email only",
			item: email.MessageIndexItem{
				Message: email.Message{
					From: []email.Address{
						{Email: "bob@example.com"},
					},
				},
			},
			expected: "bob@example.com",
		},
		{
			name: "No sender",
			item: email.MessageIndexItem{
				Message: email.Message{},
			},
			expected: "Unknown Sender",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mlc.GetSenderNameFromIndexItem(tt.item)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestConvertMessagesToIndexItems(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	window := test.NewWindow(nil)
	defer window.Close()

	statusBar := widget.NewLabel("Ready")
	accountController := &mockAccountController{
		currentAccount: &config.Account{
			Name:        "Test Account",
			Email:       "test@example.com",
			DisplayName: "Test User",
			IMAP: config.ServerConfig{
				Host:     "imap.example.com",
				Port:     993,
				Username: "test",
				Password: "pass",
				TLS:      true,
			},
			SMTP: config.ServerConfig{
				Host:     "smtp.example.com",
				Port:     587,
				Username: "test",
				Password: "pass",
				TLS:      true,
			},
		},
	}

	mlc := NewMessageListController(accountController, statusBar, window)

	messages := []email.Message{
		{
			UID:     1,
			Subject: "Test Message 1",
		},
		{
			UID:     2,
			Subject: "Test Message 2",
		},
	}

	indexItems := mlc.ConvertMessagesToIndexItems(messages, "INBOX")

	if len(indexItems) != 2 {
		t.Errorf("Expected 2 index items, got %d", len(indexItems))
	}

	if indexItems[0].AccountName != "Test Account" {
		t.Errorf("Expected account name 'Test Account', got '%s'", indexItems[0].AccountName)
	}

	if indexItems[0].FolderName != "INBOX" {
		t.Errorf("Expected folder name 'INBOX', got '%s'", indexItems[0].FolderName)
	}

	if indexItems[0].Message.Subject != "Test Message 1" {
		t.Errorf("Expected subject 'Test Message 1', got '%s'", indexItems[0].Message.Subject)
	}
}

func TestConvertMessagesToIndexItems_NoAccount(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	window := test.NewWindow(nil)
	defer window.Close()

	statusBar := widget.NewLabel("Ready")
	accountController := &mockAccountController{
		currentAccount: nil, // No account set
	}

	mlc := NewMessageListController(accountController, statusBar, window)

	messages := []email.Message{
		{UID: 1, Subject: "Test"},
	}

	indexItems := mlc.ConvertMessagesToIndexItems(messages, "INBOX")

	if len(indexItems) != 0 {
		t.Errorf("Expected empty array when no account is set, got %d items", len(indexItems))
	}
}

func TestUpdateSortButtons(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	window := test.NewWindow(nil)
	defer window.Close()

	statusBar := widget.NewLabel("Ready")
	accountController := &mockAccountController{}

	mlc := NewMessageListController(accountController, statusBar, window)

	sortByButton := widget.NewButton("Sort", func() {})
	sortOrderButton := widget.NewButton("Order", func() {})

	mlc.SetSortButtons(sortByButton, sortOrderButton)

	// Test with date sorting, descending
	mlc.sortBy = SortByDate
	mlc.sortOrder = SortDescending
	mlc.UpdateSortButtons()

	if sortByButton.Text != "Sort: Date" {
		t.Errorf("Expected sortByButton text 'Sort: Date', got '%s'", sortByButton.Text)
	}

	if sortOrderButton.Text != "↓" {
		t.Errorf("Expected sortOrderButton text '↓', got '%s'", sortOrderButton.Text)
	}

	// Test with sender sorting, ascending
	mlc.sortBy = SortBySender
	mlc.sortOrder = SortAscending
	mlc.UpdateSortButtons()

	if sortByButton.Text != "Sort: Sender" {
		t.Errorf("Expected sortByButton text 'Sort: Sender', got '%s'", sortByButton.Text)
	}

	if sortOrderButton.Text != "↑" {
		t.Errorf("Expected sortOrderButton text '↑', got '%s'", sortOrderButton.Text)
	}
}

func TestUpdateColumnHeaders(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	window := test.NewWindow(nil)
	defer window.Close()

	statusBar := widget.NewLabel("Ready")
	accountController := &mockAccountController{}

	mlc := NewMessageListController(accountController, statusBar, window)

	dateBtn := widget.NewButton("Date", func() {})
	senderBtn := widget.NewButton("Sender", func() {})
	subjectBtn := widget.NewButton("Subject", func() {})

	mlc.SetHeaderButtons(dateBtn, senderBtn, subjectBtn)

	// Test with date sorting, descending
	mlc.sortBy = SortByDate
	mlc.sortOrder = SortDescending
	mlc.UpdateColumnHeaders()

	if dateBtn.Text != "Date ↓" {
		t.Errorf("Expected date button text 'Date ↓', got '%s'", dateBtn.Text)
	}

	if senderBtn.Text != "Sender" {
		t.Errorf("Expected sender button text 'Sender', got '%s'", senderBtn.Text)
	}

	// Test with sender sorting, ascending
	mlc.sortBy = SortBySender
	mlc.sortOrder = SortAscending
	mlc.UpdateColumnHeaders()

	if senderBtn.Text != "Sender ↑" {
		t.Errorf("Expected sender button text 'Sender ↑', got '%s'", senderBtn.Text)
	}

	if dateBtn.Text != "Date" {
		t.Errorf("Expected date button text 'Date', got '%s'", dateBtn.Text)
	}
}

func TestOnMessagesChangedCallback(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	window := test.NewWindow(nil)
	defer window.Close()

	statusBar := widget.NewLabel("Ready")
	accountController := &mockAccountController{}

	mlc := NewMessageListController(accountController, statusBar, window)

	callbackCalled := false
	mlc.SetOnMessagesChanged(func() {
		callbackCalled = true
	})

	messages := []email.MessageIndexItem{
		{Message: email.Message{UID: 1, Subject: "Test"}},
	}

	mlc.SetMessages(messages)

	if !callbackCalled {
		t.Error("Expected onMessagesChanged callback to be called")
	}
}
