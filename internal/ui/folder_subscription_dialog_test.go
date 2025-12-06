package ui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/wltechblog/gommail/internal/email"
	"github.com/wltechblog/gommail/internal/logging"
)

// MockFolderIMAPClient is a mock implementation of the IMAPClient interface for folder subscription testing
type MockFolderIMAPClient struct {
	allFolders        []email.Folder
	subscribedFolders []email.Folder
	subscriptions     map[string]bool
}

func NewMockFolderIMAPClient() *MockFolderIMAPClient {
	return &MockFolderIMAPClient{
		allFolders: []email.Folder{
			{Name: "INBOX", Subscribed: true},
			{Name: "Sent", Subscribed: true},
			{Name: "Drafts", Subscribed: false},
			{Name: "Trash", Subscribed: true},
			{Name: "Archive", Subscribed: false},
			{Name: "Spam", Subscribed: false},
		},
		subscribedFolders: []email.Folder{
			{Name: "INBOX", Subscribed: true},
			{Name: "Sent", Subscribed: true},
			{Name: "Trash", Subscribed: true},
		},
		subscriptions: map[string]bool{
			"INBOX": true,
			"Sent":  true,
			"Trash": true,
		},
	}
}

// Implement IMAPClient interface methods
func (m *MockFolderIMAPClient) Connect() error                       { return nil }
func (m *MockFolderIMAPClient) Disconnect() error                    { return nil }
func (m *MockFolderIMAPClient) ForceReconnect() error                { return nil }
func (m *MockFolderIMAPClient) IsConnected() bool                    { return true }
func (m *MockFolderIMAPClient) ListFolders() ([]email.Folder, error) { return m.subscribedFolders, nil }
func (m *MockFolderIMAPClient) SelectFolder(name string) error       { return nil }
func (m *MockFolderIMAPClient) FetchMessages(folder string, limit int) ([]email.Message, error) {
	return nil, nil
}
func (m *MockFolderIMAPClient) FetchMessage(folder string, uid uint32) (*email.Message, error) {
	return nil, nil
}
func (m *MockFolderIMAPClient) GetCachedMessages(folder string) ([]email.Message, bool, error) {
	return nil, false, nil
}
func (m *MockFolderIMAPClient) InvalidateFolderCache()               {}
func (m *MockFolderIMAPClient) InvalidateMessageCache(folder string) {}
func (m *MockFolderIMAPClient) InvalidateMessageCacheByUID(folder string, uid uint32) {
}
func (m *MockFolderIMAPClient) RefreshMessagesInBackground(folder string, limit int) {}
func (m *MockFolderIMAPClient) RefreshFoldersInBackground()                          {}

func (m *MockFolderIMAPClient) SubscribeToDefaultFolders(sentFolder, trashFolder string) error {
	return nil
}

func (m *MockFolderIMAPClient) StoreSentMessage(sentFolder string, messageContent []byte) error {
	return nil
}

func (m *MockFolderIMAPClient) ListSubscribedFolders() ([]email.Folder, error) {
	return m.subscribedFolders, nil
}

func (m *MockFolderIMAPClient) ListSubscribedFoldersFresh() ([]email.Folder, error) {
	return m.subscribedFolders, nil
}

func (m *MockFolderIMAPClient) ForceRefreshSubscribedFolders() ([]email.Folder, error) {
	return m.subscribedFolders, nil
}

func (m *MockFolderIMAPClient) ListAllFolders() ([]email.Folder, error) {
	return m.allFolders, nil
}

func (m *MockFolderIMAPClient) CreateFolder(name string) error {
	// Create new folder and add to all folders list
	newFolder := email.Folder{Name: name, Subscribed: true}
	m.allFolders = append(m.allFolders, newFolder)

	// Automatically subscribe to the new folder (as per the real implementation)
	return m.SubscribeFolder(name)
}

func (m *MockFolderIMAPClient) DeleteFolder(name string) error {
	// Remove from all folders list
	for i, folder := range m.allFolders {
		if folder.Name == name {
			m.allFolders = append(m.allFolders[:i], m.allFolders[i+1:]...)
			break
		}
	}

	// Remove from subscribed folders list
	for i, folder := range m.subscribedFolders {
		if folder.Name == name {
			m.subscribedFolders = append(m.subscribedFolders[:i], m.subscribedFolders[i+1:]...)
			break
		}
	}

	// Remove from subscriptions map
	delete(m.subscriptions, name)

	return nil
}

func (m *MockFolderIMAPClient) SubscribeFolder(name string) error {
	m.subscriptions[name] = true
	// Update subscribed folders list
	for i, folder := range m.allFolders {
		if folder.Name == name {
			m.allFolders[i].Subscribed = true
			// Add to subscribed folders if not already there
			found := false
			for _, subFolder := range m.subscribedFolders {
				if subFolder.Name == name {
					found = true
					break
				}
			}
			if !found {
				m.subscribedFolders = append(m.subscribedFolders, m.allFolders[i])
			}
			break
		}
	}
	return nil
}

func (m *MockFolderIMAPClient) UnsubscribeFolder(name string) error {
	m.subscriptions[name] = false
	// Update subscribed folders list
	for i, folder := range m.allFolders {
		if folder.Name == name {
			m.allFolders[i].Subscribed = false
			break
		}
	}
	// Remove from subscribed folders
	for i, folder := range m.subscribedFolders {
		if folder.Name == name {
			m.subscribedFolders = append(m.subscribedFolders[:i], m.subscribedFolders[i+1:]...)
			break
		}
	}
	return nil
}

// Remove this duplicate method - it's already defined above with the correct signature

func (m *MockFolderIMAPClient) InvalidateSubscribedFolderCache() {}

func (m *MockFolderIMAPClient) CleanupUnsubscribedFolderCache() error {
	return nil
}

// Cache methods
func (m *MockFolderIMAPClient) FetchFreshMessages(folder string, limit int) ([]email.Message, error) {
	return []email.Message{}, nil
}

func (m *MockFolderIMAPClient) FetchMessageWithFullHeaders(folder string, uid uint32) (*email.Message, error) {
	return nil, nil
}

// Worker lifecycle methods
func (m *MockFolderIMAPClient) Start() error { return nil }
func (m *MockFolderIMAPClient) Stop()        {}

// Connection state and monitoring methods
func (m *MockFolderIMAPClient) SetConnectionStateCallback(callback func(email.ConnectionEvent)) {}
func (m *MockFolderIMAPClient) SetNewMessageCallback(callback func(string, []email.Message))    {}
func (m *MockFolderIMAPClient) StartMonitoring(folder string) error                             { return nil }
func (m *MockFolderIMAPClient) StopMonitoring()                                                 {}
func (m *MockFolderIMAPClient) IsMonitoring() bool                                              { return false }
func (m *MockFolderIMAPClient) IsMonitoringPaused() bool                                        { return false }
func (m *MockFolderIMAPClient) PauseMonitoring()                                                {}
func (m *MockFolderIMAPClient) ResumeMonitoring()                                               {}
func (m *MockFolderIMAPClient) GetMonitoredFolder() string                                      { return "" }
func (m *MockFolderIMAPClient) GetMonitoringMode() email.MonitorMode                            { return email.MonitorMode(0) }
func (m *MockFolderIMAPClient) MarkInitialSyncComplete(folder string)                           {}

// Health and status methods
func (m *MockFolderIMAPClient) GetHealthStatus() map[string]interface{}       { return nil }
func (m *MockFolderIMAPClient) SetHealthCheckInterval(interval time.Duration) {}
func (m *MockFolderIMAPClient) SetReconnectConfig(initialDelay, maxDelay time.Duration, maxAttempts int) {
}
func (m *MockFolderIMAPClient) ForceDisconnect() {}

// Additional methods to implement the full IMAPClient interface
func (m *MockFolderIMAPClient) MarkAsRead(folder string, uid uint32) error {
	return nil
}

func (m *MockFolderIMAPClient) MarkAsUnread(folder string, uid uint32) error {
	return nil
}

func (m *MockFolderIMAPClient) DeleteMessage(folder string, uid uint32) error {
	return nil
}

func (m *MockFolderIMAPClient) MoveMessage(folder string, uid uint32, targetFolder string) error {
	return nil
}

// Search methods to implement the full IMAPClient interface
func (m *MockFolderIMAPClient) SearchMessages(criteria email.SearchCriteria) ([]email.Message, error) {
	return []email.Message{}, nil
}

func (m *MockFolderIMAPClient) SearchMessagesInFolder(folder string, criteria email.SearchCriteria) ([]email.Message, error) {
	return []email.Message{}, nil
}

func (m *MockFolderIMAPClient) SearchCachedMessages(criteria email.SearchCriteria) ([]email.Message, error) {
	return []email.Message{}, nil
}

func TestNewFolderSubscriptionDialog(t *testing.T) {
	app := test.NewApp()
	mockClient := NewMockFolderIMAPClient()
	logger := logging.NewComponent("test")

	dialog := NewFolderSubscriptionDialog(app, mockClient, logger, nil, nil, nil)

	if dialog == nil {
		t.Fatal("NewFolderSubscriptionDialog returned nil")
	}

	if dialog.window == nil {
		t.Error("Dialog window not created correctly")
	}

	if dialog.imapClient != mockClient {
		t.Error("Dialog IMAP client not set correctly")
	}

	if dialog.logger != logger {
		t.Error("Dialog logger not set correctly")
	}

	// Check that UI components are initialized
	if dialog.folderList == nil {
		t.Error("Folder list not initialized")
	}

	if dialog.subscribeBtn == nil {
		t.Error("Subscribe button not initialized")
	}

	if dialog.unsubscribeBtn == nil {
		t.Error("Unsubscribe button not initialized")
	}

	if dialog.refreshBtn == nil {
		t.Error("Refresh button not initialized")
	}

	if dialog.statusLabel == nil {
		t.Error("Status label not initialized")
	}
}

func TestFolderSubscriptionDialog_IsFolderSubscribed(t *testing.T) {
	app := test.NewApp()
	
	mockClient := NewMockFolderIMAPClient()
	logger := logging.NewComponent("test")

	dialog := NewFolderSubscriptionDialog(app, mockClient, logger, nil, nil, nil)

	// Set up test data
	dialog.subscribedFolders = []email.Folder{
		{Name: "INBOX", Subscribed: true},
		{Name: "Sent", Subscribed: true},
		{Name: "Trash", Subscribed: true},
	}

	// Test subscribed folders
	if !dialog.isFolderSubscribed("INBOX") {
		t.Error("INBOX should be subscribed")
	}

	if !dialog.isFolderSubscribed("Sent") {
		t.Error("Sent should be subscribed")
	}

	if !dialog.isFolderSubscribed("Trash") {
		t.Error("Trash should be subscribed")
	}

	// Test non-subscribed folders
	if dialog.isFolderSubscribed("Drafts") {
		t.Error("Drafts should not be subscribed")
	}

	if dialog.isFolderSubscribed("Archive") {
		t.Error("Archive should not be subscribed")
	}

	if dialog.isFolderSubscribed("NonExistent") {
		t.Error("NonExistent folder should not be subscribed")
	}
}

func TestFolderSubscriptionDialog_SubscribeUnsubscribe(t *testing.T) {
	app := test.NewApp()
	
	mockClient := NewMockFolderIMAPClient()
	logger := logging.NewComponent("test")

	dialog := NewFolderSubscriptionDialog(app, mockClient, logger, nil, nil, nil)

	// Test subscribing to a folder
	dialog.subscribeToFolder("Drafts")

	// Check that the mock client was updated
	if !mockClient.subscriptions["Drafts"] {
		t.Error("Drafts should be subscribed in mock client")
	}

	// Test unsubscribing from a folder
	dialog.unsubscribeFromFolder("Sent")

	// Check that the mock client was updated
	if mockClient.subscriptions["Sent"] {
		t.Error("Sent should be unsubscribed in mock client")
	}
}

func TestFolderSubscriptionDialog_RefreshFolders(t *testing.T) {
	app := test.NewApp()
	
	mockClient := NewMockFolderIMAPClient()
	logger := logging.NewComponent("test")

	dialog := NewFolderSubscriptionDialog(app, mockClient, logger, nil, nil, nil)

	// Test refresh folders
	dialog.refreshFolders()

	// Check that folders were loaded
	if len(dialog.allFolders) == 0 {
		t.Error("All folders should be loaded after refresh")
	}

	if len(dialog.subscribedFolders) == 0 {
		t.Error("Subscribed folders should be loaded after refresh")
	}

	// Verify expected folders are present
	expectedAllFolders := []string{"INBOX", "Sent", "Drafts", "Trash", "Archive", "Spam"}
	if len(dialog.allFolders) != len(expectedAllFolders) {
		t.Errorf("Expected %d all folders, got %d", len(expectedAllFolders), len(dialog.allFolders))
	}

	expectedSubscribedFolders := []string{"INBOX", "Sent", "Trash"}
	if len(dialog.subscribedFolders) != len(expectedSubscribedFolders) {
		t.Errorf("Expected %d subscribed folders, got %d", len(expectedSubscribedFolders), len(dialog.subscribedFolders))
	}
}

func TestFolderSubscriptionDialog_OnCloseCallback(t *testing.T) {
	app := test.NewApp()
	
	mockClient := NewMockFolderIMAPClient()
	logger := logging.NewComponent("test")

	// Track if callback was called
	callbackCalled := false
	callback := func() {
		callbackCalled = true
	}

	dialog := NewFolderSubscriptionDialog(app, mockClient, logger, callback, nil, nil)

	// Verify callback is set
	if dialog.onClose == nil {
		t.Error("Expected onClose callback to be set")
	}

	// Simulate dialog close by calling the callback directly
	if dialog.onClose != nil {
		dialog.onClose()
	}

	// Verify callback was called
	if !callbackCalled {
		t.Error("Expected callback to be called when dialog is closed")
	}
}

func TestFolderSubscriptionDialog_CreateFolder(t *testing.T) {
	app := test.NewApp()
	
	mockClient := NewMockFolderIMAPClient()
	logger := logging.NewComponent("test")

	dialog := NewFolderSubscriptionDialog(app, mockClient, logger, nil, nil, nil)

	// Get initial folder count
	initialAllFolders, _ := mockClient.ListAllFolders()
	initialSubscribedFolders, _ := mockClient.ListSubscribedFolders()
	initialAllCount := len(initialAllFolders)
	initialSubscribedCount := len(initialSubscribedFolders)

	// Test creating a new folder
	testFolderName := "TestFolder"
	dialog.createFolder(testFolderName)

	// Verify folder was created and added to all folders
	allFolders, _ := mockClient.ListAllFolders()
	if len(allFolders) != initialAllCount+1 {
		t.Errorf("Expected %d folders after creation, got %d", initialAllCount+1, len(allFolders))
	}

	// Verify folder was automatically subscribed
	subscribedFolders, _ := mockClient.ListSubscribedFolders()
	if len(subscribedFolders) != initialSubscribedCount+1 {
		t.Errorf("Expected %d subscribed folders after creation, got %d", initialSubscribedCount+1, len(subscribedFolders))
	}

	// Verify the new folder exists in both lists
	foundInAll := false
	foundInSubscribed := false
	for _, folder := range allFolders {
		if folder.Name == testFolderName {
			foundInAll = true
			if !folder.Subscribed {
				t.Error("Created folder should be marked as subscribed")
			}
			break
		}
	}
	for _, folder := range subscribedFolders {
		if folder.Name == testFolderName {
			foundInSubscribed = true
			break
		}
	}

	if !foundInAll {
		t.Error("Created folder not found in all folders list")
	}
	if !foundInSubscribed {
		t.Error("Created folder not found in subscribed folders list")
	}
}

func TestFolderSubscriptionDialog_CreateFolderCallsCallback(t *testing.T) {
	app := test.NewApp()
	
	mockClient := NewMockFolderIMAPClient()
	logger := logging.NewComponent("test")

	// Track if callback was called using a channel for synchronization
	callbackCalled := make(chan bool, 1)
	onFolderAdded := func() {
		callbackCalled <- true
	}

	dialog := NewFolderSubscriptionDialog(app, mockClient, logger, nil, onFolderAdded, nil)

	// Test creating a new folder
	testFolderName := "TestCallbackFolder"
	dialog.createFolder(testFolderName)

	// Wait for callback to be called (with timeout)
	select {
	case <-callbackCalled:
		// Callback was called successfully
	case <-time.After(1 * time.Second):
		t.Error("Expected onFolderAdded callback to be called after folder creation to refresh main window")
	}

	// Verify folder was still created properly
	allFolders, _ := mockClient.ListAllFolders()
	foundFolder := false
	for _, folder := range allFolders {
		if folder.Name == testFolderName {
			foundFolder = true
			break
		}
	}
	if !foundFolder {
		t.Error("Created folder not found in all folders list")
	}
}
