package notification

import (
	"fmt"
	"testing"
	"time"

	"github.com/wltechblog/gommail/internal/logging"
)

func TestNewManager(t *testing.T) {
	config := Config{
		Enabled:        true,
		DefaultTimeout: 5 * time.Second,
		AppName:        "Test gommail client",
		AppIcon:        "test-icon.png",
	}

	manager, err := NewManager(config)
	if err != nil {
		t.Fatalf("Failed to create notification manager: %v", err)
	}
	defer manager.Close()

	if !manager.IsEnabled() {
		t.Error("Manager should be enabled")
	}

	// Test platform support (should work on all platforms, even if just null notifier)
	if manager.notifier == nil {
		t.Error("Notifier should not be nil")
	}
}

func TestShowNewMessage(t *testing.T) {
	config := Config{
		Enabled:        true,
		DefaultTimeout: 3 * time.Second,
		AppName:        "Test gommail client",
	}

	manager, err := NewManager(config)
	if err != nil {
		t.Fatalf("Failed to create notification manager: %v", err)
	}
	defer manager.Close()

	// Test normal message
	err = manager.ShowNewMessage("John Doe <john@example.com>", "Test Subject", "INBOX")
	if err != nil {
		t.Errorf("Failed to show new message notification: %v", err)
	}

	// Test long subject truncation
	longSubject := "This is a very long subject line that should be truncated to avoid overwhelming the user with too much text in the notification"
	err = manager.ShowNewMessage("Jane Smith", longSubject, "Work")
	if err != nil {
		t.Errorf("Failed to show notification with long subject: %v", err)
	}

	// Test long sender truncation
	longSender := "Very Long Sender Name That Should Be Truncated <verylongsendername@example.com>"
	err = manager.ShowNewMessage(longSender, "Short subject", "Personal")
	if err != nil {
		t.Errorf("Failed to show notification with long sender: %v", err)
	}
}

func TestManagerEnabledDisabled(t *testing.T) {
	config := Config{
		Enabled:        false, // Start disabled
		DefaultTimeout: 3 * time.Second,
		AppName:        "Test gommail client",
	}

	manager, err := NewManager(config)
	if err != nil {
		t.Fatalf("Failed to create notification manager: %v", err)
	}
	defer manager.Close()

	if manager.IsEnabled() {
		t.Error("Manager should be disabled initially")
	}

	// Enable notifications
	manager.SetEnabled(true)
	if !manager.IsEnabled() {
		t.Error("Manager should be enabled after SetEnabled(true)")
	}

	// Disable notifications
	manager.SetEnabled(false)
	if manager.IsEnabled() {
		t.Error("Manager should be disabled after SetEnabled(false)")
	}
}

func TestNotificationTruncation(t *testing.T) {
	config := Config{
		Enabled:        true,
		DefaultTimeout: 1 * time.Second,
		AppName:        "Test gommail client",
	}

	manager, err := NewManager(config)
	if err != nil {
		t.Fatalf("Failed to create notification manager: %v", err)
	}
	defer manager.Close()

	// Test subject truncation (should truncate at 50 chars)
	longSubject := "This is a very long subject line that definitely exceeds fifty characters and should be truncated"
	err = manager.ShowNewMessage("Test Sender", longSubject, "INBOX")
	if err != nil {
		t.Errorf("Failed to show notification: %v", err)
	}
}

func TestShowNewMessages(t *testing.T) {
	config := Config{
		Enabled:        true,
		DefaultTimeout: 3 * time.Second,
		AppName:        "Test gommail client",
	}

	manager, err := NewManager(config)
	if err != nil {
		t.Fatalf("Failed to create notification manager: %v", err)
	}
	defer manager.Close()

	// Test empty messages
	err = manager.ShowNewMessages([]MessageInfo{}, "INBOX")
	if err != nil {
		t.Errorf("Failed to show empty messages notification: %v", err)
	}

	// Test single message (should use ShowNewMessage)
	singleMessage := []MessageInfo{
		{Sender: "John Doe", Subject: "Test Subject"},
	}
	err = manager.ShowNewMessages(singleMessage, "INBOX")
	if err != nil {
		t.Errorf("Failed to show single message notification: %v", err)
	}

	// Test multiple messages (should batch)
	multipleMessages := []MessageInfo{
		{Sender: "John Doe", Subject: "First Message"},
		{Sender: "Jane Smith", Subject: "Second Message"},
		{Sender: "Bob Johnson", Subject: "Third Message"},
	}
	err = manager.ShowNewMessages(multipleMessages, "INBOX")
	if err != nil {
		t.Errorf("Failed to show multiple messages notification: %v", err)
	}

	// Test large batch (should summarize)
	largeMessages := make([]MessageInfo, 10)
	for i := 0; i < 10; i++ {
		largeMessages[i] = MessageInfo{
			Sender:  fmt.Sprintf("Sender %d", i+1),
			Subject: fmt.Sprintf("Message %d", i+1),
		}
	}
	err = manager.ShowNewMessages(largeMessages, "INBOX")
	if err != nil {
		t.Errorf("Failed to show large batch notification: %v", err)
	}

	// Test sender truncation (should truncate at 30 chars)
	longSender := "This is a very long sender name that exceeds thirty characters"
	err = manager.ShowNewMessage(longSender, "Test Subject", "INBOX")
	if err != nil {
		t.Errorf("Failed to show notification: %v", err)
	}
}

func TestNullNotifier(t *testing.T) {
	// Use the actual logging package to create a logger
	logger := logging.NewComponent("test")
	notifier := newNullNotifier(logger)

	if notifier.IsSupported() {
		t.Error("Null notifier should not be supported")
	}

	notification := Notification{
		Title: "Test Title",
		Body:  "Test Body",
	}

	err := notifier.Show(notification)
	if err != nil {
		t.Errorf("Null notifier Show should not return error: %v", err)
	}

	err = notifier.Close()
	if err != nil {
		t.Errorf("Null notifier Close should not return error: %v", err)
	}
}
