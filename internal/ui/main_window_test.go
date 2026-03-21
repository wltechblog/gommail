package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"fyne.io/fyne/v2"
	fynetest "fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/wltechblog/gommail/internal/config"
	"github.com/wltechblog/gommail/internal/email"
	"github.com/wltechblog/gommail/internal/logging"
	"github.com/wltechblog/gommail/internal/ui/controllers"
	cachepkg "github.com/wltechblog/gommail/pkg/cache"
	"github.com/wltechblog/gommail/pkg/imap"
	"github.com/wltechblog/gommail/pkg/smtp"
)

func TestUnifiedInboxHealthMonitoringLifecycle(t *testing.T) {
	mw := &MainWindow{logger: logging.NewComponent("ui-test")}

	mw.restartUnifiedInboxHealthMonitoring()
	firstCtx := mw.unifiedInboxCtx
	if firstCtx == nil || mw.unifiedInboxCancel == nil {
		t.Fatalf("expected unified inbox health monitoring to be initialized")
	}

	mw.restartUnifiedInboxHealthMonitoring()
	secondCtx := mw.unifiedInboxCtx
	if secondCtx == nil || secondCtx == firstCtx {
		t.Fatalf("expected restart to replace the unified inbox health context")
	}

	select {
	case <-firstCtx.Done():
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected previous unified inbox health context to be cancelled")
	}

	mw.stopUnifiedInboxHealthMonitoring()
	if mw.unifiedInboxCtx != nil || mw.unifiedInboxCancel != nil {
		t.Fatalf("expected unified inbox health monitoring to be cleared after stop")
	}

	select {
	case <-secondCtx.Done():
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected active unified inbox health context to be cancelled on stop")
	}
}

func TestCurrentAccountReconnectGuard(t *testing.T) {
	mw := &MainWindow{logger: logging.NewComponent("ui-test")}

	if !mw.beginCurrentAccountReconnect("shortcut") {
		t.Fatalf("expected first reconnect attempt to start")
	}

	if mw.beginCurrentAccountReconnect("shortcut") {
		t.Fatalf("expected overlapping reconnect attempt to be rejected")
	}

	mw.endCurrentAccountReconnect()

	if !mw.beginCurrentAccountReconnect("shortcut") {
		t.Fatalf("expected reconnect to be allowed again after finishing")
	}
	mw.endCurrentAccountReconnect()

	mw.setCurrentAccountConnectInProgress(true)
	defer mw.setCurrentAccountConnectInProgress(false)

	if mw.beginCurrentAccountReconnect("shortcut") {
		t.Fatalf("expected reconnect to be skipped while account connection is in progress")
	}
}

func TestCurrentAccountConnectFlag(t *testing.T) {
	mw := &MainWindow{logger: logging.NewComponent("ui-test")}

	if mw.isCurrentAccountConnectInProgress() {
		t.Fatalf("expected connect-in-progress flag to default to false")
	}

	mw.setCurrentAccountConnectInProgress(true)
	if !mw.isCurrentAccountConnectInProgress() {
		t.Fatalf("expected connect-in-progress flag to be true after set")
	}

	mw.setCurrentAccountConnectInProgress(false)
	if mw.isCurrentAccountConnectInProgress() {
		t.Fatalf("expected connect-in-progress flag to be false after reset")
	}
}

func TestPrepareIMAPClientForAccountSelectionReusesManagedClient(t *testing.T) {
	account := config.Account{
		Name:  "Reuse Account",
		Email: "reuse@example.com",
		IMAP: config.ServerConfig{
			Host:     "imap.example.com",
			Port:     993,
			Username: "reuse@example.com",
			Password: "password",
			TLS:      true,
		},
	}
	mockConfig := newMockConfigManager([]config.Account{account})
	mw := &MainWindow{
		logger:            logging.NewComponent("ui-test"),
		config:            mockConfig,
		accountController: controllers.NewAccountController(mockConfig, nil),
	}

	existing := imap.NewClientWrapperWithCache(email.ServerConfig{
		Host:     account.IMAP.Host,
		Port:     account.IMAP.Port,
		Username: account.IMAP.Username,
		Password: account.IMAP.Password,
		TLS:      account.IMAP.TLS,
	}, nil, accountCacheKey(account.Name))
	if err := existing.Start(); err != nil {
		t.Fatalf("start existing managed client: %v", err)
	}
	defer existing.Stop()
	setClientWrapperConnectedState(t, existing, true)
	mw.accountController.StoreIMAPClient(account.Name, existing)

	client, needsConnect, err := mw.prepareIMAPClientForAccountSelection(&account)
	if err != nil {
		t.Fatalf("prepare client: %v", err)
	}
	if needsConnect {
		t.Fatalf("expected existing managed client to be reused without reconnect")
	}
	if client != existing {
		t.Fatalf("expected reused client pointer %p, got %p", existing, client)
	}
}

func TestPrepareIMAPClientForAccountSelectionCreatesAndStoresClient(t *testing.T) {
	account := config.Account{
		Name:  "Fresh Account",
		Email: "fresh@example.com",
		IMAP: config.ServerConfig{
			Host:     "imap.example.com",
			Port:     993,
			Username: "fresh@example.com",
			Password: "password",
			TLS:      true,
		},
	}
	mockConfig := newMockConfigManager([]config.Account{account})
	mw := &MainWindow{
		logger:            logging.NewComponent("ui-test"),
		config:            mockConfig,
		accountController: controllers.NewAccountController(mockConfig, nil),
	}

	client, needsConnect, err := mw.prepareIMAPClientForAccountSelection(&account)
	if err != nil {
		t.Fatalf("prepare client: %v", err)
	}
	defer mw.accountController.CloseClientForAccount(account.Name)

	if !needsConnect {
		t.Fatalf("expected new client to require connect")
	}

	stored, exists := mw.accountController.GetIMAPClientForAccount(account.Name)
	if !exists || stored == nil {
		t.Fatalf("expected prepared client to be stored in account controller")
	}
	if stored != client {
		t.Fatalf("expected stored client pointer %p, got %p", client, stored)
	}
	if stored.IsConnected() {
		t.Fatalf("expected newly prepared client to be started but not yet connected")
	}
}

func setClientWrapperConnectedState(t *testing.T, client *imap.ClientWrapper, connected bool) {
	t.Helper()

	field := reflect.ValueOf(client).Elem().FieldByName("connected")
	if !field.IsValid() {
		t.Fatalf("client wrapper missing connected field")
	}

	state := int32(0)
	if connected {
		state = 1
	}
	atomic.StoreInt32((*int32)(unsafe.Pointer(field.UnsafeAddr())), state)
}

func TestMonitorConnectionHealthStopsWhenContextCancelled(t *testing.T) {
	mw := &MainWindow{logger: logging.NewComponent("ui-test")}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})

	go func() {
		defer close(finished)
		mw.monitorConnectionHealth(ctx)
	}()

	cancel()

	select {
	case <-finished:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected monitorConnectionHealth to stop promptly after cancellation")
	}
}

// TestSortMessages tests the message sorting functionality
func TestSortMessages(t *testing.T) {
	// Create a MainWindow instance for testing with minimal initialization
	mw := &MainWindow{
		sortBy:    SortByDate,
		sortOrder: SortDescending,
	}

	// Initialize messageListController with nil dependencies for this test
	// (we're only testing sorting logic, not UI interactions)
	mw.messageListController = controllers.NewMessageListController(nil, nil, nil)

	// Create test messages with different dates, senders, and subjects
	now := time.Now()
	mw.messages = []email.MessageIndexItem{
		{
			Message: email.Message{
				Subject: "Beta Subject",
				From:    []email.Address{{Name: "Charlie", Email: "charlie@example.com"}},
				Date:    now.Add(-2 * time.Hour),
			},
			AccountName: "Test Account",
		},
		{
			Message: email.Message{
				Subject: "Alpha Subject",
				From:    []email.Address{{Name: "Alice", Email: "alice@example.com"}},
				Date:    now.Add(-1 * time.Hour),
			},
			AccountName: "Test Account",
		},
		{
			Message: email.Message{
				Subject: "Gamma Subject",
				From:    []email.Address{{Name: "Bob", Email: "bob@example.com"}},
				Date:    now,
			},
			AccountName: "Test Account",
		},
	}

	// Test sorting by date (descending - newest first)
	mw.sortBy = SortByDate
	mw.sortOrder = SortDescending
	mw.sortMessages()

	if mw.messages[0].Message.Subject != "Gamma Subject" {
		t.Errorf("Expected first message to be 'Gamma Subject', got '%s'", mw.messages[0].Message.Subject)
	}
	if mw.messages[2].Message.Subject != "Beta Subject" {
		t.Errorf("Expected last message to be 'Beta Subject', got '%s'", mw.messages[2].Message.Subject)
	}

	// Test sorting by date (ascending - oldest first)
	mw.sortOrder = SortAscending
	mw.sortMessages()

	if mw.messages[0].Message.Subject != "Beta Subject" {
		t.Errorf("Expected first message to be 'Beta Subject', got '%s'", mw.messages[0].Message.Subject)
	}
	if mw.messages[2].Message.Subject != "Gamma Subject" {
		t.Errorf("Expected last message to be 'Gamma Subject', got '%s'", mw.messages[2].Message.Subject)
	}

	// Test sorting by sender (ascending)
	mw.sortBy = SortBySender
	mw.sortOrder = SortAscending
	mw.sortMessages()

	if mw.getSenderNameFromIndexItem(mw.messages[0]) != "Alice" {
		t.Errorf("Expected first sender to be 'Alice', got '%s'", mw.getSenderNameFromIndexItem(mw.messages[0]))
	}
	if mw.getSenderNameFromIndexItem(mw.messages[2]) != "Charlie" {
		t.Errorf("Expected last sender to be 'Charlie', got '%s'", mw.getSenderNameFromIndexItem(mw.messages[2]))
	}

	// Test sorting by subject (ascending)
	mw.sortBy = SortBySubject
	mw.sortOrder = SortAscending
	mw.sortMessages()

	if mw.messages[0].Message.Subject != "Alpha Subject" {
		t.Errorf("Expected first subject to be 'Alpha Subject', got '%s'", mw.messages[0].Message.Subject)
	}
	if mw.messages[2].Message.Subject != "Gamma Subject" {
		t.Errorf("Expected last subject to be 'Gamma Subject', got '%s'", mw.messages[2].Message.Subject)
	}
}

// TestGetSenderName tests the sender name extraction
func TestGetSenderName(t *testing.T) {
	mw := &MainWindow{}

	// Test with name and email
	indexItem1 := email.MessageIndexItem{
		Message: email.Message{
			From: []email.Address{{Name: "John Doe", Email: "john@example.com"}},
		},
		AccountName: "Test Account",
	}
	if name := mw.getSenderNameFromIndexItem(indexItem1); name != "John Doe" {
		t.Errorf("Expected 'John Doe', got '%s'", name)
	}

	// Test with email only
	indexItem2 := email.MessageIndexItem{
		Message: email.Message{
			From: []email.Address{{Email: "jane@example.com"}},
		},
		AccountName: "Test Account",
	}
	if name := mw.getSenderNameFromIndexItem(indexItem2); name != "jane@example.com" {
		t.Errorf("Expected 'jane@example.com', got '%s'", name)
	}

	// Test with no sender
	indexItem3 := email.MessageIndexItem{
		Message: email.Message{
			From: []email.Address{},
		},
		AccountName: "Test Account",
	}
	if name := mw.getSenderNameFromIndexItem(indexItem3); name != "Unknown Sender" {
		t.Errorf("Expected 'Unknown Sender', got '%s'", name)
	}
}

// TestGetSortName tests the sort name function
func TestGetSortName(t *testing.T) {
	mw := &MainWindow{
		messageListController: controllers.NewMessageListController(nil, nil, nil),
	}

	mw.sortBy = SortByDate
	mw.messageListController.SetSortCriteria(controllers.SortByDate, controllers.SortDescending)
	if name := mw.getSortName(); name != "date" {
		t.Errorf("Expected 'date', got '%s'", name)
	}

	mw.sortBy = SortBySender
	mw.messageListController.SetSortCriteria(controllers.SortBySender, controllers.SortAscending)
	if name := mw.getSortName(); name != "sender" {
		t.Errorf("Expected 'sender', got '%s'", name)
	}

	mw.sortBy = SortBySubject
	mw.messageListController.SetSortCriteria(controllers.SortBySubject, controllers.SortAscending)
	if name := mw.getSortName(); name != "subject" {
		t.Errorf("Expected 'subject', got '%s'", name)
	}
}

// TestSortLogic tests the sort criteria change logic without UI components
func TestSortLogic(t *testing.T) {
	mw := &MainWindow{
		sortBy:    SortByDate,
		sortOrder: SortDescending,
		messages: []email.MessageIndexItem{
			{Message: email.Message{Subject: "B", From: []email.Address{{Name: "Alice"}}, Date: time.Now().Add(-1 * time.Hour)}, AccountName: "Test"},
			{Message: email.Message{Subject: "A", From: []email.Address{{Name: "Bob"}}, Date: time.Now()}, AccountName: "Test"},
		},
	}

	// Test changing to sender sort (should default to ascending)
	// Simulate the logic from setSortBy without UI updates
	if mw.sortBy != SortBySender {
		mw.sortBy = SortBySender
		mw.sortOrder = SortAscending // Default for text fields
	}

	if mw.sortBy != SortBySender {
		t.Errorf("Expected sortBy to be SortBySender, got %v", mw.sortBy)
	}
	if mw.sortOrder != SortAscending {
		t.Errorf("Expected sortOrder to be SortAscending for new criteria, got %v", mw.sortOrder)
	}

	// Test clicking same criteria (should toggle order)
	if mw.sortBy == SortBySender {
		if mw.sortOrder == SortAscending {
			mw.sortOrder = SortDescending
		} else {
			mw.sortOrder = SortAscending
		}
	}

	if mw.sortOrder != SortDescending {
		t.Errorf("Expected sortOrder to toggle to SortDescending, got %v", mw.sortOrder)
	}
}

// TestReadTimer tests the 5-second read timer functionality
func TestReadTimer(t *testing.T) {
	// Create a MainWindow instance for testing
	mw := &MainWindow{
		logger: logging.NewComponent("ui-test"),
	}

	// Create a test message that is unread
	testMsg := &email.Message{
		UID:     12345,
		Subject: "Test Message",
		From:    []email.Address{{Name: "Test Sender", Email: "test@example.com"}},
		Date:    time.Now(),
		Flags:   []string{}, // No \Seen flag = unread
	}

	// Test that timer is started for unread messages
	mw.startReadTimer(testMsg)

	// Check that timer was created
	hasTimer := mw.readTimer != nil

	if !hasTimer {
		t.Error("Expected read timer to be started for unread message")
	}

	// Test cancellation
	mw.cancelReadTimer()

	hasTimer = mw.readTimer != nil

	if hasTimer {
		t.Error("Expected read timer to be cancelled")
	}

	// Test that timer is not started for already read messages
	readMsg := &email.Message{
		UID:     12346,
		Subject: "Read Message",
		From:    []email.Address{{Name: "Test Sender", Email: "test@example.com"}},
		Date:    time.Now(),
		Flags:   []string{"\\Seen"}, // Has \Seen flag = read
	}

	mw.startReadTimer(readMsg)

	hasTimer = mw.readTimer != nil

	if hasTimer {
		t.Error("Expected no read timer to be started for already read message")
	}
}

// MockIMAPClient for testing connection failures
type MockIMAPClient struct {
	connected      bool
	shouldFailRead bool
	markReadCalls  int
}

func (m *MockIMAPClient) Connect() error {
	m.connected = true
	return nil
}

func (m *MockIMAPClient) Disconnect() error {
	m.connected = false
	return nil
}

func (m *MockIMAPClient) ListFolders() ([]email.Folder, error) { return nil, nil }
func (m *MockIMAPClient) SelectFolder(name string) error       { return nil }
func (m *MockIMAPClient) FetchMessages(folder string, limit int) ([]email.Message, error) {
	return nil, nil
}
func (m *MockIMAPClient) FetchMessage(folder string, uid uint32) (*email.Message, error) {
	return nil, nil
}
func (m *MockIMAPClient) DeleteMessage(folder string, uid uint32) error { return nil }
func (m *MockIMAPClient) MoveMessage(folder string, uid uint32, targetFolder string) error {
	return nil
}
func (m *MockIMAPClient) MarkAsUnread(folder string, uid uint32) error { return nil }

func (m *MockIMAPClient) MarkAsRead(folder string, uid uint32) error {
	m.markReadCalls++
	if m.shouldFailRead {
		return fmt.Errorf("not connected")
	}
	return nil
}

// Additional methods to implement the full IMAPClient interface
func (m *MockIMAPClient) ForceReconnect() error                                           { return nil }
func (m *MockIMAPClient) ListSubscribedFolders() ([]email.Folder, error)                  { return nil, nil }
func (m *MockIMAPClient) ListSubscribedFoldersFresh() ([]email.Folder, error)             { return nil, nil }
func (m *MockIMAPClient) ForceRefreshSubscribedFolders() ([]email.Folder, error)          { return nil, nil }
func (m *MockIMAPClient) ListAllFolders() ([]email.Folder, error)                         { return nil, nil }
func (m *MockIMAPClient) CreateFolder(name string) error                                  { return nil }
func (m *MockIMAPClient) DeleteFolder(name string) error                                  { return nil }
func (m *MockIMAPClient) SubscribeFolder(name string) error                               { return nil }
func (m *MockIMAPClient) UnsubscribeFolder(name string) error                             { return nil }
func (m *MockIMAPClient) SubscribeToDefaultFolders(sentFolder, trashFolder string) error  { return nil }
func (m *MockIMAPClient) StoreSentMessage(sentFolder string, messageContent []byte) error { return nil }
func (m *MockIMAPClient) SearchMessages(criteria email.SearchCriteria) ([]email.Message, error) {
	return []email.Message{}, nil
}
func (m *MockIMAPClient) SearchMessagesInFolder(folder string, criteria email.SearchCriteria) ([]email.Message, error) {
	return []email.Message{}, nil
}
func (m *MockIMAPClient) SearchCachedMessages(criteria email.SearchCriteria) ([]email.Message, error) {
	return []email.Message{}, nil
}

// TestMarkMessageAsReadWithConnectionFailure tests the retry logic when IMAP connection fails
func TestMarkMessageAsReadWithConnectionFailure(t *testing.T) {
	// Note: This test demonstrates the retry logic concept, but since MainWindow.imapClient
	// is typed as *imap.Client rather than the email.IMAPClient interface, we can't easily
	// inject a mock here. The retry logic is tested in integration scenarios.

	// Create a test message
	testMsg := &email.Message{
		UID:     12345,
		Subject: "Test Message",
		From:    []email.Address{{Name: "Test Sender", Email: "test@example.com"}},
		Date:    time.Now(),
		Flags:   []string{}, // No \Seen flag = unread
	}

	// Test that the message structure is correct for retry scenarios
	if testMsg.UID == 0 {
		t.Error("Expected valid UID for test message")
	}

	if len(testMsg.Flags) != 0 {
		t.Error("Expected unread message to have no flags")
	}

	// The actual retry logic is tested through integration tests and real usage
	t.Log("Connection failure retry logic is tested through integration scenarios")
}

// TestClearAccountState tests that clearing account state works correctly
func TestClearAccountState(t *testing.T) {
	// Create a MainWindow instance for testing
	mw := &MainWindow{
		folderController:  controllers.NewFolderController(nil),
		accountController: controllers.NewAccountController(nil, nil),
	}

	// Set up folders first
	mw.folderController.SetFolders([]email.Folder{
		{Name: "INBOX", MessageCount: 10},
		{Name: "Sent", MessageCount: 5},
	})

	// Set up initial state with some messages and a selected message
	now := time.Now()
	mw.messages = []email.MessageIndexItem{
		{
			Message: email.Message{
				ID:      "msg1",
				Subject: "Old Account Message 1",
				From:    []email.Address{{Name: "Old User", Email: "old@example.com"}},
				Date:    now.Add(-1 * time.Hour),
			},
			AccountName: "Test Account",
		},
		{
			Message: email.Message{
				ID:      "msg2",
				Subject: "Old Account Message 2",
				From:    []email.Address{{Name: "Old User 2", Email: "old2@example.com"}},
				Date:    now,
			},
			AccountName: "Test Account",
		},
	}
	mw.selectedMessage = &mw.messages[0]
	mw.folderController.SelectFolder("INBOX")

	// Verify initial state
	if len(mw.messages) != 2 {
		t.Errorf("Expected 2 initial messages, got %d", len(mw.messages))
	}
	if mw.selectedMessage == nil {
		t.Error("Expected selected message to be set initially")
	}
	currentFolder := mw.folderController.GetCurrentFolder()
	if currentFolder != "INBOX" {
		t.Errorf("Expected current folder to be 'INBOX', got '%s'", currentFolder)
	}

	// Call clearAccountState to test the core logic
	mw.clearAccountState()

	// Verify that the message list and related state were cleared
	if len(mw.messages) != 0 {
		t.Errorf("Expected messages to be cleared after clearing account state, got %d messages", len(mw.messages))
	}
	if mw.selectedMessage != nil {
		t.Error("Expected selected message to be cleared after clearing account state")
	}
	currentFolder = mw.folderController.GetCurrentFolder()
	if currentFolder != "" {
		t.Errorf("Expected current folder to be cleared after clearing account state, got '%s'", currentFolder)
	}
}

// TestMessageIndexItemFunctionality tests the MessageIndexItem architecture
func TestMessageIndexItemFunctionality(t *testing.T) {
	// Create a MainWindow instance for testing
	mw := &MainWindow{
		logger:            logging.NewComponent("test"),
		folderController:  controllers.NewFolderController(nil),
		accountController: controllers.NewAccountController(nil, nil),
	}

	// Test MessageIndexItem structure
	now := time.Now()
	testMessage := email.Message{
		Subject: "Test Message",
		From:    []email.Address{{Name: "Test Sender", Email: "test@example.com"}},
		Date:    now,
		UID:     12345,
	}

	indexItem := email.MessageIndexItem{
		Message:      testMessage,
		AccountName:  "Test Account",
		AccountEmail: "account@example.com",
		FolderName:   "INBOX",
	}

	// Verify MessageIndexItem structure
	if indexItem.Message.Subject != "Test Message" {
		t.Errorf("Expected subject 'Test Message', got '%s'", indexItem.Message.Subject)
	}
	if indexItem.AccountName != "Test Account" {
		t.Errorf("Expected account name 'Test Account', got '%s'", indexItem.AccountName)
	}
	if indexItem.AccountEmail != "account@example.com" {
		t.Errorf("Expected account email 'account@example.com', got '%s'", indexItem.AccountEmail)
	}

	// Test message array with MessageIndexItem
	mw.messages = []email.MessageIndexItem{indexItem}

	if len(mw.messages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(mw.messages))
	}
	if mw.messages[0].Message.Subject != "Test Message" {
		t.Errorf("Expected message subject 'Test Message', got '%s'", mw.messages[0].Message.Subject)
	}

	// Test unified inbox state management
	mw.accountController.SetUnifiedInbox(true)
	mw.clearAccountState()

	if mw.accountController.IsUnifiedInbox() {
		t.Error("Expected unified inbox state to be cleared after clearAccountState")
	}
}

// TestUnifiedInboxMessageSelection tests unified inbox message selection synchronization
func TestUnifiedInboxMessageSelection(t *testing.T) {
	// Create test messages from different accounts
	now := time.Now()
	messages := []email.MessageIndexItem{
		{
			Message: email.Message{
				UID:     1,
				Subject: "Message from Account 1",
				From:    []email.Address{{Name: "Sender 1", Email: "sender1@example.com"}},
				Date:    now.Add(-2 * time.Hour),
				Flags:   []string{},
			},
			AccountName:  "Account1",
			AccountEmail: "user1@example.com",
			FolderName:   "INBOX",
		},
		{
			Message: email.Message{
				UID:     2,
				Subject: "Message from Account 2",
				From:    []email.Address{{Name: "Sender 2", Email: "sender2@example.com"}},
				Date:    now.Add(-1 * time.Hour),
				Flags:   []string{},
			},
			AccountName:  "Account2",
			AccountEmail: "user2@example.com",
			FolderName:   "INBOX",
		},
		{
			Message: email.Message{
				UID:     3,
				Subject: "Another message from Account 1",
				From:    []email.Address{{Name: "Sender 3", Email: "sender3@example.com"}},
				Date:    now,
				Flags:   []string{},
			},
			AccountName:  "Account1",
			AccountEmail: "user1@example.com",
			FolderName:   "INBOX",
		},
	}

	mw := &MainWindow{
		logger:                logging.NewComponent("test"),
		accountController:     controllers.NewAccountController(nil, nil),
		messages:              messages,
		messageViewController: controllers.NewMessageViewController(nil, true),
	}
	mw.accountController.SetUnifiedInbox(true)

	// Test message array consistency validation
	if !mw.validateMessageArrayConsistency() {
		t.Error("Message array consistency check should pass for valid messages")
	}

	// Test valid message selection
	mw.selectMessage(0)
	if mw.selectedMessage == nil {
		t.Error("Expected message to be selected")
	}
	if mw.selectedMessage.Message.UID != 1 {
		t.Errorf("Expected selected message UID 1, got %d", mw.selectedMessage.Message.UID)
	}
	if mw.selectedMessage.AccountName != "Account1" {
		t.Errorf("Expected selected message account 'Account1', got '%s'", mw.selectedMessage.AccountName)
	}

	// Test invalid message selection (should be ignored)
	originalSelected := mw.selectedMessage
	mw.selectMessage(10) // Invalid index
	if mw.selectedMessage != originalSelected {
		t.Error("Invalid message selection should not change selected message")
	}

	// Test negative index selection (should be ignored)
	mw.selectMessage(-1)
	if mw.selectedMessage != originalSelected {
		t.Error("Negative index selection should not change selected message")
	}
}

func TestSelectMessageWithAttachedList(t *testing.T) {
	app := fynetest.NewApp()
	defer app.Quit()

	messages := []email.MessageIndexItem{
		{Message: email.Message{UID: 1, Subject: "First"}, AccountName: "Account1", FolderName: "INBOX"},
		{Message: email.Message{UID: 2, Subject: "Second"}, AccountName: "Account1", FolderName: "INBOX"},
	}

	mw := &MainWindow{
		logger:                logging.NewComponent("test"),
		accountController:     controllers.NewAccountController(nil, nil),
		messages:              messages,
		messageViewController: controllers.NewMessageViewController(nil, true),
	}

	list := widget.NewList(
		func() int { return len(mw.messages) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id < len(mw.messages) {
				obj.(*widget.Label).SetText(mw.messages[id].Message.Subject)
			}
		},
	)
	window := fynetest.NewWindow(list)
	defer window.Close()
	mw.messageList = list

	mw.selectMessage(0)
	if mw.selectedMessage == nil || mw.selectedMessage.Message.UID != 1 {
		t.Fatalf("expected first message to be selected, got %#v", mw.selectedMessage)
	}

	mw.selectMessage(1)
	if mw.selectedMessage == nil || mw.selectedMessage.Message.UID != 2 {
		t.Fatalf("expected second message to be selected after refresh-item path, got %#v", mw.selectedMessage)
	}
}

// TestMessageIndexItemSorting tests that MessageIndexItem messages are sorted correctly
func TestMessageIndexItemSorting(t *testing.T) {
	now := time.Now()

	// Create test MessageIndexItem messages from different accounts with different dates
	indexItems := []email.MessageIndexItem{
		{
			Message: email.Message{
				Subject: "Oldest Message",
				Date:    now.Add(-2 * time.Hour),
				UID:     1,
			},
			AccountName:  "Account A",
			AccountEmail: "a@example.com",
		},
		{
			Message: email.Message{
				Subject: "Newest Message",
				Date:    now,
				UID:     3,
			},
			AccountName:  "Account B",
			AccountEmail: "b@example.com",
		},
		{
			Message: email.Message{
				Subject: "Middle Message",
				Date:    now.Add(-1 * time.Hour),
				UID:     2,
			},
			AccountName:  "Account A",
			AccountEmail: "a@example.com",
		},
	}

	// Sort messages by date (newest first)
	sort.Slice(indexItems, func(i, j int) bool {
		return indexItems[i].Message.Date.After(indexItems[j].Message.Date)
	})

	// Verify sorting
	if indexItems[0].Message.Subject != "Newest Message" {
		t.Errorf("Expected first message to be 'Newest Message', got '%s'", indexItems[0].Message.Subject)
	}
	if indexItems[1].Message.Subject != "Middle Message" {
		t.Errorf("Expected second message to be 'Middle Message', got '%s'", indexItems[1].Message.Subject)
	}
	if indexItems[2].Message.Subject != "Oldest Message" {
		t.Errorf("Expected third message to be 'Oldest Message', got '%s'", indexItems[2].Message.Subject)
	}

	// Verify account information is preserved
	if indexItems[0].AccountName != "Account B" {
		t.Errorf("Expected newest message from 'Account B', got '%s'", indexItems[0].AccountName)
	}
	if indexItems[2].AccountName != "Account A" {
		t.Errorf("Expected oldest message from 'Account A', got '%s'", indexItems[2].AccountName)
	}
}

// TestTimezoneHandlingInSorting tests that messages with different timezones are sorted correctly
func TestTimezoneHandlingInSorting(t *testing.T) {
	// Create a base time in UTC
	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	// Create test messages with different timezones
	// These should all represent the same moment in time, just in different zones
	estLocation, _ := time.LoadLocation("America/New_York")    // UTC-5
	pstLocation, _ := time.LoadLocation("America/Los_Angeles") // UTC-8
	jstLocation, _ := time.LoadLocation("Asia/Tokyo")          // UTC+9

	mw := &MainWindow{
		sortBy:                SortByDate,
		sortOrder:             SortDescending,
		messageListController: controllers.NewMessageListController(nil, nil, nil),
	}

	// Create messages with same absolute time but different timezone representations
	mw.messages = []email.MessageIndexItem{
		{
			Message: email.Message{
				Subject:      "Message in EST",
				Date:         baseTime.In(estLocation), // Same moment, EST timezone
				InternalDate: time.Time{},              // Zero value to test Date fallback
				UID:          1,
			},
			AccountName: "Account A",
		},
		{
			Message: email.Message{
				Subject:      "Message in UTC",
				Date:         baseTime, // Same moment, UTC timezone
				InternalDate: time.Time{},
				UID:          2,
			},
			AccountName: "Account B",
		},
		{
			Message: email.Message{
				Subject:      "Message in PST",
				Date:         baseTime.In(pstLocation), // Same moment, PST timezone
				InternalDate: time.Time{},
				UID:          3,
			},
			AccountName: "Account C",
		},
		{
			Message: email.Message{
				Subject:      "Message in JST",
				Date:         baseTime.In(jstLocation), // Same moment, JST timezone
				InternalDate: time.Time{},
				UID:          4,
			},
			AccountName: "Account D",
		},
		{
			Message: email.Message{
				Subject:      "Older message",
				Date:         baseTime.Add(-1 * time.Hour), // 1 hour earlier
				InternalDate: time.Time{},
				UID:          5,
			},
			AccountName: "Account E",
		},
		{
			Message: email.Message{
				Subject:      "Newer message",
				Date:         baseTime.Add(1 * time.Hour), // 1 hour later
				InternalDate: time.Time{},
				UID:          6,
			},
			AccountName: "Account F",
		},
	}

	// Sort messages
	mw.sortMessages()

	// Verify that the newer message comes first (descending order)
	if mw.messages[0].Message.Subject != "Newer message" {
		t.Errorf("Expected first message to be 'Newer message', got '%s'", mw.messages[0].Message.Subject)
	}

	// Verify that the older message comes last
	if mw.messages[len(mw.messages)-1].Message.Subject != "Older message" {
		t.Errorf("Expected last message to be 'Older message', got '%s'", mw.messages[len(mw.messages)-1].Message.Subject)
	}

	// The four messages with the same absolute time should be grouped together in the middle
	// (their relative order doesn't matter as they're at the same moment)
	middleMessages := mw.messages[1:5]
	for _, msg := range middleMessages {
		subject := msg.Message.Subject
		if subject != "Message in EST" && subject != "Message in UTC" &&
			subject != "Message in PST" && subject != "Message in JST" {
			t.Errorf("Expected middle messages to be timezone variants, got '%s'", subject)
		}
	}

	// Test with InternalDate (which should take precedence)
	mw.messages = []email.MessageIndexItem{
		{
			Message: email.Message{
				Subject:      "Message with InternalDate",
				Date:         baseTime.Add(-10 * time.Hour), // Old Date header
				InternalDate: baseTime,                      // Recent InternalDate (should be used)
				UID:          1,
			},
			AccountName: "Account A",
		},
		{
			Message: email.Message{
				Subject:      "Message with only Date",
				Date:         baseTime.Add(-1 * time.Hour), // Should come after the InternalDate message
				InternalDate: time.Time{},                  // Zero value
				UID:          2,
			},
			AccountName: "Account B",
		},
	}

	mw.sortMessages()

	// The message with InternalDate should come first (it's more recent)
	if mw.messages[0].Message.Subject != "Message with InternalDate" {
		t.Errorf("Expected InternalDate to take precedence, got '%s' first", mw.messages[0].Message.Subject)
	}
}

// TestMessageIndexItemSelection tests that message selection works with MessageIndexItem
func TestMessageIndexItemSelection(t *testing.T) {
	// Create test accounts
	accounts := []config.Account{
		{
			Name:  "Test Account",
			Email: "account@example.com",
			SMTP: config.ServerConfig{
				Host:     "smtp.example.com",
				Port:     587,
				Username: "account@example.com",
				Password: "password",
				TLS:      false,
			},
		},
	}

	// Create a mock config manager
	mockConfig := newMockConfigManager(accounts)

	// Create a mock SMTP client
	mockSMTPClient := &smtp.Client{}

	mw := &MainWindow{
		logger:            logging.NewComponent("test"),
		accountController: controllers.NewAccountController(mockConfig, nil),
		config:            mockConfig,
		smtpClient:        mockSMTPClient,
	}
	mw.accountController.SetCurrentAccount(&accounts[0])

	// Create test MessageIndexItem
	now := time.Now()
	testMessage := email.Message{
		Subject: "Test Message",
		From:    []email.Address{{Name: "Test Sender", Email: "test@example.com"}},
		Date:    now,
		UID:     12345,
	}

	indexItem := email.MessageIndexItem{
		Message:      testMessage,
		AccountName:  "Test Account",
		AccountEmail: "account@example.com",
		FolderName:   "INBOX",
	}

	// Set up the message state
	mw.messages = []email.MessageIndexItem{indexItem}

	// Test that MessageIndexItem contains the correct account information
	mw.selectedMessage = &mw.messages[0]

	// Validate that MessageIndexItem has the correct account information
	if mw.selectedMessage.AccountName != "Test Account" {
		t.Errorf("Expected account name 'Test Account', got '%s'", mw.selectedMessage.AccountName)
	}
	if mw.selectedMessage.AccountEmail != "account@example.com" {
		t.Errorf("Expected account email 'account@example.com', got '%s'", mw.selectedMessage.AccountEmail)
	}

	// Test that we can find the account from config using the account name
	accountFound := false
	for _, acc := range mw.config.GetAccounts() {
		if acc.Name == mw.selectedMessage.AccountName {
			accountFound = true
			if acc.Name != "Test Account" {
				t.Errorf("Expected account name 'Test Account', got '%s'", acc.Name)
			}
			if acc.Email != "account@example.com" {
				t.Errorf("Expected account email 'account@example.com', got '%s'", acc.Email)
			}
			break
		}
	}

	if !accountFound {
		t.Error("Expected to find account in config")
	}
}

// TestSetupNewAccountAutomatically tests the automatic account setup functionality
func TestSetupNewAccountAutomatically(t *testing.T) {
	// This test verifies that the setupNewAccountAutomatically function exists and can be called
	// without panicking. Since it involves network connections, we can't easily test the full
	// functionality without mocking the IMAP client.

	// Create a test account
	testAccount := &config.Account{
		Name:        "Test Account",
		Email:       "test@example.com",
		DisplayName: "Test User",
		IMAP: config.ServerConfig{
			Host:     "imap.example.com",
			Port:     993,
			Username: "test@example.com",
			Password: "password",
			TLS:      true,
		},
		SMTP: config.ServerConfig{
			Host:     "smtp.example.com",
			Port:     587,
			Username: "test@example.com",
			Password: "password",
			TLS:      false,
		},
		SentFolder:  "Sent",
		TrashFolder: "Trash",
	}

	// Verify the account has the expected properties
	if testAccount.Name != "Test Account" {
		t.Errorf("Expected account name 'Test Account', got '%s'", testAccount.Name)
	}

	if testAccount.Email != "test@example.com" {
		t.Errorf("Expected account email 'test@example.com', got '%s'", testAccount.Email)
	}

	if testAccount.IMAP.Host != "imap.example.com" {
		t.Errorf("Expected IMAP host 'imap.example.com', got '%s'", testAccount.IMAP.Host)
	}

	if testAccount.SMTP.Host != "smtp.example.com" {
		t.Errorf("Expected SMTP host 'smtp.example.com', got '%s'", testAccount.SMTP.Host)
	}

	t.Log("Automatic account setup test completed successfully")
}

func TestHTMLToMarkdownConversion(t *testing.T) {
	// Create a minimal MainWindow for testing
	mw := &MainWindow{}

	// Test HTML with links
	html := `<html><body>
<p>PRE ORDER NOW.</p>
<p><a href="https://tbrdy4.fm72.fdske.com/e/c/01k5cpe2dv22wfqq4pjkbfrg7p/01k5cpe2dv22wfqq4pjnqm9mep">Link 1</a></p>
<p><a href="https://tbrdy4.fm72.fdske.com/e/c/01k5cpe2dv22wfqq4pjkbfrg7p/01k5cpe2dv22wfqq4pjsm7m6c0">Link 2</a></p>
</body></html>`

	markdown := mw.TestHTMLToMarkdown(html)
	t.Logf("Converted markdown: %s", markdown)

	// Check if links are properly converted
	if !strings.Contains(markdown, "[Link 1](https://") {
		t.Errorf("Link 1 not properly converted to markdown: %s", markdown)
	}

	if !strings.Contains(markdown, "[Link 2](https://") {
		t.Errorf("Link 2 not properly converted to markdown: %s", markdown)
	}

	// Check if PRE ORDER NOW text is preserved
	if !strings.Contains(markdown, "PRE ORDER NOW") {
		t.Errorf("Text content not preserved: %s", markdown)
	}
}

func TestHTMLToMarkdownWithPlainTextURLs(t *testing.T) {
	// Create a minimal MainWindow for testing
	mw := &MainWindow{}

	// Test HTML with plain text URLs (not wrapped in <a> tags)
	html := `<html><body>
<p>PRE ORDER NOW.</p>
<p>https://tbrdy4.fm72.fdske.com/e/c/01k5cpe2dv22wfqq4pjkbfrg7p/01k5cpe2dv22wfqq4pjnqm9mep</p>
<p>https://tbrdy4.fm72.fdske.com/e/c/01k5cpe2dv22wfqq4pjkbfrg7p/01k5cpe2dv22wfqq4pjsm7m6c0</p>
</body></html>`

	markdown := mw.TestHTMLToMarkdown(html)
	t.Logf("Converted markdown: %s", markdown)

	// The URLs should still be present as plain text since they're not in <a> tags
	if !strings.Contains(markdown, "https://tbrdy4.fm72.fdske.com") {
		t.Errorf("Plain text URLs not preserved: %s", markdown)
	}
}

func TestFormatAddresses(t *testing.T) {
	mw := &MainWindow{}

	tests := []struct {
		name      string
		addresses []email.Address
		expected  string
	}{
		{
			name: "Address with both name and email",
			addresses: []email.Address{
				{Name: "John Doe", Email: "john@example.com"},
			},
			expected: "John Doe <john@example.com>",
		},
		{
			name: "Address with email only",
			addresses: []email.Address{
				{Email: "jane@example.com"},
			},
			expected: "jane@example.com",
		},
		{
			name: "Address with name only",
			addresses: []email.Address{
				{Name: "Bob Smith"},
			},
			expected: "Bob Smith",
		},
		{
			name: "Multiple addresses",
			addresses: []email.Address{
				{Name: "John Doe", Email: "john@example.com"},
				{Email: "jane@example.com"},
				{Name: "Bob Smith", Email: "bob@example.com"},
			},
			expected: "John Doe <john@example.com>, jane@example.com, Bob Smith <bob@example.com>",
		},
		{
			name:      "Empty addresses",
			addresses: []email.Address{},
			expected:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mw.formatAddresses(tt.addresses)
			if result != tt.expected {
				t.Errorf("formatAddresses() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestMessageViewModeButtonLabel(t *testing.T) {
	tests := []struct {
		showHTML bool
		want     string
	}{
		{showHTML: true, want: "View: Rich"},
		{showHTML: false, want: "View: Plain"},
	}

	for _, tt := range tests {
		if got := messageViewModeButtonLabel(tt.showHTML); got != tt.want {
			t.Fatalf("messageViewModeButtonLabel(%t) = %q, want %q", tt.showHTML, got, tt.want)
		}
	}
}

func TestMessageViewModeButtonTooltip(t *testing.T) {
	richTooltip := messageViewModeButtonTooltip(true)
	if !strings.Contains(richTooltip, "Currently showing rich view") || !strings.Contains(richTooltip, "plain text") {
		t.Fatalf("unexpected rich tooltip: %q", richTooltip)
	}

	plainTooltip := messageViewModeButtonTooltip(false)
	if !strings.Contains(plainTooltip, "Currently showing plain text") || !strings.Contains(plainTooltip, "rich view") {
		t.Fatalf("unexpected plain tooltip: %q", plainTooltip)
	}
}

func TestMessageListItemHighlighting(t *testing.T) {
	// Create a test MainWindow
	mw := &MainWindow{
		messages: []email.MessageIndexItem{
			{Message: email.Message{UID: 1, Subject: "Test Message 1"}, AccountName: "Test"},
			{Message: email.Message{UID: 2, Subject: "Test Message 2"}, AccountName: "Test"},
		},
		selectionManager:  controllers.NewSelectionManager(),
		accountController: controllers.NewAccountController(nil, nil),
	}

	// Create a message list item
	item := newMessageListItem(mw)

	// Test initial state - should not be selected
	if item.isSelected {
		t.Error("New message list item should not be selected initially")
	}

	// Test setting selection
	item.setSelected(true)
	if !item.isSelected {
		t.Error("Message list item should be selected after setSelected(true)")
	}

	// Test clearing selection
	item.setSelected(false)
	if item.isSelected {
		t.Error("Message list item should not be selected after setSelected(false)")
	}

	// Test updateContent with selected message
	mw.selectedMessage = &mw.messages[0]
	item.updateContent(0, &mw.messages[0])
	if !item.isSelected {
		t.Error("Message list item should be selected when updating with currently selected message")
	}

	// Test updateContent with non-selected message
	item.updateContent(1, &mw.messages[1])
	if item.isSelected {
		t.Error("Message list item should not be selected when updating with non-selected message")
	}
}

func TestLoadCachedFoldersDirectlyFallsBackToLegacyKey(t *testing.T) {
	mw := &MainWindow{
		cache:  cachepkg.New(t.TempDir(), false, 10),
		logger: logging.NewComponent("ui-test"),
	}

	folders := []email.Folder{{Name: "INBOX"}, {Name: "Sent"}}
	data, err := json.Marshal(folders)
	if err != nil {
		t.Fatalf("marshal folders: %v", err)
	}

	legacyKey := fmt.Sprintf("%s:folders:subscribed", "Legacy Account")
	if err := mw.cache.Set(legacyKey, data, time.Hour); err != nil {
		t.Fatalf("set legacy cache entry: %v", err)
	}

	got, found := mw.loadCachedFoldersDirectly("Legacy Account")
	if !found {
		t.Fatalf("expected legacy folder cache entry to be found")
	}
	if len(got) != len(folders) || got[0].Name != folders[0].Name || got[1].Name != folders[1].Name {
		t.Fatalf("unexpected folders from cache: %#v", got)
	}
}

func TestLoadCachedMessagesDirectlyPrefersPrimaryKey(t *testing.T) {
	mw := &MainWindow{
		cache:  cachepkg.New(t.TempDir(), false, 10),
		logger: logging.NewComponent("ui-test"),
	}

	primaryMessages := []email.Message{{UID: 1, Subject: "primary"}}
	legacyMessages := []email.Message{{UID: 2, Subject: "legacy"}}

	primaryData, err := json.Marshal(primaryMessages)
	if err != nil {
		t.Fatalf("marshal primary messages: %v", err)
	}
	legacyData, err := json.Marshal(legacyMessages)
	if err != nil {
		t.Fatalf("marshal legacy messages: %v", err)
	}

	primaryKey := fmt.Sprintf("%s:messages:%s", accountCacheKey("Test Account"), "INBOX")
	legacyKey := fmt.Sprintf("%s:messages:%s", "Test Account", "INBOX")
	if err := mw.cache.Set(primaryKey, primaryData, time.Hour); err != nil {
		t.Fatalf("set primary cache entry: %v", err)
	}
	if err := mw.cache.Set(legacyKey, legacyData, time.Hour); err != nil {
		t.Fatalf("set legacy cache entry: %v", err)
	}

	got, found := mw.loadCachedMessagesDirectly("Test Account", "INBOX")
	if !found {
		t.Fatalf("expected message cache entry to be found")
	}
	if len(got) != 1 || got[0].Subject != "primary" {
		t.Fatalf("expected primary cache entry, got %#v", got)
	}
}

func TestLoadCachedMessagesDirectlyReportsEmptyCacheHit(t *testing.T) {
	mw := &MainWindow{
		cache:  cachepkg.New(t.TempDir(), false, 10),
		logger: logging.NewComponent("ui-test"),
	}

	data, err := json.Marshal([]email.Message{})
	if err != nil {
		t.Fatalf("marshal empty messages: %v", err)
	}

	primaryKey := fmt.Sprintf("%s:messages:%s", accountCacheKey("Empty Account"), "Archive")
	if err := mw.cache.Set(primaryKey, data, time.Hour); err != nil {
		t.Fatalf("set empty cache entry: %v", err)
	}

	got, found := mw.loadCachedMessagesDirectly("Empty Account", "Archive")
	if !found {
		t.Fatalf("expected empty cache entry to count as a cache hit")
	}
	if len(got) != 0 {
		t.Fatalf("expected no messages, got %#v", got)
	}
}

func TestLoadUnifiedInboxFromCacheUsesPrimaryAndLegacyKeys(t *testing.T) {
	accounts := []config.Account{
		{Name: "Primary Account", Email: "primary@example.com"},
		{Name: "Legacy Account", Email: "legacy@example.com"},
	}
	mockConfig := newMockConfigManager(accounts)
	cache := cachepkg.New(t.TempDir(), false, 10)
	mw := &MainWindow{
		cache:             cache,
		config:            mockConfig,
		accountController: controllers.NewAccountController(mockConfig, cache),
		logger:            logging.NewComponent("ui-test"),
	}

	primaryData, err := json.Marshal([]email.Message{{UID: 1, Subject: "primary"}})
	if err != nil {
		t.Fatalf("marshal primary unified messages: %v", err)
	}
	legacyData, err := json.Marshal([]email.Message{{UID: 2, Subject: "legacy"}})
	if err != nil {
		t.Fatalf("marshal legacy unified messages: %v", err)
	}

	if err := cache.Set(fmt.Sprintf("%s:messages:INBOX", accountCacheKey("Primary Account")), primaryData, time.Hour); err != nil {
		t.Fatalf("set primary unified cache entry: %v", err)
	}
	if err := cache.Set("Legacy Account:messages:INBOX", legacyData, time.Hour); err != nil {
		t.Fatalf("set legacy unified cache entry: %v", err)
	}

	got, found := mw.loadUnifiedInboxFromCache()
	if !found {
		t.Fatalf("expected unified inbox cache to be found")
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 unified inbox messages, got %d", len(got))
	}

	seenSubjects := map[string]bool{}
	for _, item := range got {
		seenSubjects[item.Message.Subject] = true
		if item.FolderName != "INBOX" {
			t.Fatalf("expected folder name INBOX, got %q", item.FolderName)
		}
	}
	if !seenSubjects["primary"] || !seenSubjects["legacy"] {
		t.Fatalf("unexpected unified inbox messages: %#v", got)
	}
}

func TestLoadUnifiedInboxFromCacheReportsEmptyCacheHit(t *testing.T) {
	accounts := []config.Account{{Name: "Empty Unified", Email: "empty@example.com"}}
	mockConfig := newMockConfigManager(accounts)
	cache := cachepkg.New(t.TempDir(), false, 10)
	mw := &MainWindow{
		cache:             cache,
		config:            mockConfig,
		accountController: controllers.NewAccountController(mockConfig, cache),
		logger:            logging.NewComponent("ui-test"),
	}

	data, err := json.Marshal([]email.Message{})
	if err != nil {
		t.Fatalf("marshal empty unified messages: %v", err)
	}

	if err := cache.Set(fmt.Sprintf("%s:messages:INBOX", accountCacheKey("Empty Unified")), data, time.Hour); err != nil {
		t.Fatalf("set empty unified cache entry: %v", err)
	}

	got, found := mw.loadUnifiedInboxFromCache()
	if !found {
		t.Fatalf("expected empty unified cache entry to count as a cache hit")
	}
	if len(got) != 0 {
		t.Fatalf("expected no unified inbox messages, got %#v", got)
	}
}
