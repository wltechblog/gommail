package controllers

import (
	"testing"
	"time"

	"github.com/wltechblog/gommail/internal/email"
)

// createTestMessages creates a slice of test messages for testing
func createTestMessages(count int) []email.MessageIndexItem {
	messages := make([]email.MessageIndexItem, count)
	for i := 0; i < count; i++ {
		messages[i] = email.MessageIndexItem{
			Message: email.Message{
				UID:     uint32(i + 1),
				Subject: "Test Message " + string(rune('A'+i)),
				Date:    time.Now(),
			},
			AccountName: "test@example.com",
		}
	}
	return messages
}

func TestNewSelectionManager(t *testing.T) {
	sm := NewSelectionManager()
	
	if sm == nil {
		t.Fatal("NewSelectionManager returned nil")
	}
	
	if sm.selectedMessages == nil {
		t.Error("selectedMessages map not initialized")
	}
	
	if sm.lastSelectedIndex != -1 {
		t.Errorf("lastSelectedIndex should be -1, got %d", sm.lastSelectedIndex)
	}
	
	if sm.multiSelectionMode {
		t.Error("multiSelectionMode should be false by default")
	}
}

func TestSelectMessage(t *testing.T) {
	sm := NewSelectionManager()
	messages := createTestMessages(5)
	
	// Track callback invocations
	selectionChangedCalled := false
	messageSelectedIndex := -1
	
	sm.SetCallbacks(
		func() { selectionChangedCalled = true },
		func(index int) { messageSelectedIndex = index },
	)
	
	// Select message at index 2
	sm.SelectMessage(2, messages)
	
	if !selectionChangedCalled {
		t.Error("onSelectionChanged callback not called")
	}
	
	if messageSelectedIndex != 2 {
		t.Errorf("onMessageSelected callback called with wrong index: got %d, want 2", messageSelectedIndex)
	}
	
	if !sm.IsMessageSelected(2) {
		t.Error("Message at index 2 should be selected")
	}
	
	if sm.GetLastSelectedIndex() != 2 {
		t.Errorf("lastSelectedIndex should be 2, got %d", sm.GetLastSelectedIndex())
	}
	
	selectedMsg := sm.GetSelectedMessage()
	if selectedMsg == nil {
		t.Fatal("GetSelectedMessage returned nil")
	}
	
	if selectedMsg.Message.UID != 3 { // UID is index + 1
		t.Errorf("Selected message has wrong UID: got %d, want 3", selectedMsg.Message.UID)
	}
}

func TestSelectMessageMultiple_CtrlClick(t *testing.T) {
	sm := NewSelectionManager()
	messages := createTestMessages(5)
	
	// Select first message
	sm.SelectMessage(0, messages)
	
	// Ctrl+click on message 2 (should add to selection)
	sm.SelectMessageMultiple(2, true, false, messages)
	
	if !sm.IsMessageSelected(0) {
		t.Error("Message at index 0 should still be selected")
	}
	
	if !sm.IsMessageSelected(2) {
		t.Error("Message at index 2 should be selected")
	}
	
	// Ctrl+click on message 2 again (should toggle off)
	sm.SelectMessageMultiple(2, true, false, messages)
	
	if sm.IsMessageSelected(2) {
		t.Error("Message at index 2 should be deselected after toggle")
	}
	
	if !sm.IsMessageSelected(0) {
		t.Error("Message at index 0 should still be selected")
	}
}

func TestSelectMessageMultiple_ShiftClick(t *testing.T) {
	sm := NewSelectionManager()
	messages := createTestMessages(10)
	
	// Select message at index 2
	sm.SelectMessage(2, messages)
	
	// Shift+click on message 6 (should select range 2-6)
	sm.SelectMessageMultiple(6, false, true, messages)
	
	// Check that messages 2-6 are selected
	for i := 2; i <= 6; i++ {
		if !sm.IsMessageSelected(i) {
			t.Errorf("Message at index %d should be selected", i)
		}
	}
	
	// Check that messages outside range are not selected
	if sm.IsMessageSelected(1) {
		t.Error("Message at index 1 should not be selected")
	}
	if sm.IsMessageSelected(7) {
		t.Error("Message at index 7 should not be selected")
	}
}

func TestSelectMessageMultiple_ShiftClickReverse(t *testing.T) {
	sm := NewSelectionManager()
	messages := createTestMessages(10)
	
	// Select message at index 6
	sm.SelectMessage(6, messages)
	
	// Shift+click on message 2 (should select range 2-6, reversed)
	sm.SelectMessageMultiple(2, false, true, messages)
	
	// Check that messages 2-6 are selected
	for i := 2; i <= 6; i++ {
		if !sm.IsMessageSelected(i) {
			t.Errorf("Message at index %d should be selected", i)
		}
	}
}

func TestSelectMessageMultiple_CtrlShiftClick(t *testing.T) {
	sm := NewSelectionManager()
	messages := createTestMessages(10)
	
	// Select message at index 1
	sm.SelectMessage(1, messages)
	
	// Ctrl+click on message 5 (add to selection)
	sm.SelectMessageMultiple(5, true, false, messages)
	
	// Ctrl+Shift+click on message 8 (should add range 5-8 to existing selection)
	sm.SelectMessageMultiple(8, true, true, messages)
	
	// Check that messages 1, 5-8 are selected
	if !sm.IsMessageSelected(1) {
		t.Error("Message at index 1 should be selected")
	}
	for i := 5; i <= 8; i++ {
		if !sm.IsMessageSelected(i) {
			t.Errorf("Message at index %d should be selected", i)
		}
	}
	
	// Check that messages 2-4 are not selected
	for i := 2; i <= 4; i++ {
		if sm.IsMessageSelected(i) {
			t.Errorf("Message at index %d should not be selected", i)
		}
	}
}

func TestSelectAllMessages(t *testing.T) {
	sm := NewSelectionManager()
	messages := createTestMessages(5)
	
	sm.SelectAllMessages(messages)
	
	// Check that all messages are selected
	for i := 0; i < len(messages); i++ {
		if !sm.IsMessageSelected(i) {
			t.Errorf("Message at index %d should be selected", i)
		}
	}
	
	indices := sm.GetSelectedMessageIndices()
	if len(indices) != 5 {
		t.Errorf("Should have 5 selected messages, got %d", len(indices))
	}
}

func TestClearSelection(t *testing.T) {
	sm := NewSelectionManager()
	messages := createTestMessages(5)
	
	// Select some messages
	sm.SelectAllMessages(messages)
	
	// Clear selection
	sm.ClearSelection()
	
	// Check that no messages are selected
	for i := 0; i < len(messages); i++ {
		if sm.IsMessageSelected(i) {
			t.Errorf("Message at index %d should not be selected after clear", i)
		}
	}
	
	if sm.GetSelectedMessage() != nil {
		t.Error("GetSelectedMessage should return nil after clear")
	}
	
	if sm.GetLastSelectedIndex() != -1 {
		t.Errorf("lastSelectedIndex should be -1 after clear, got %d", sm.GetLastSelectedIndex())
	}
}

func TestGetSelectedMessages(t *testing.T) {
	sm := NewSelectionManager()
	messages := createTestMessages(10)
	
	// Select messages at indices 2, 5, 7
	sm.SelectMessage(2, messages)
	sm.SelectMessageMultiple(5, true, false, messages)
	sm.SelectMessageMultiple(7, true, false, messages)
	
	selectedMessages := sm.GetSelectedMessages(messages)
	
	if len(selectedMessages) != 3 {
		t.Errorf("Should have 3 selected messages, got %d", len(selectedMessages))
	}
	
	// Check that the correct messages are returned (should be sorted)
	expectedUIDs := []uint32{3, 6, 8} // UIDs are index + 1
	for i, msg := range selectedMessages {
		if msg.Message.UID != expectedUIDs[i] {
			t.Errorf("Selected message %d has wrong UID: got %d, want %d", i, msg.Message.UID, expectedUIDs[i])
		}
	}
}

func TestGetSelectedMessageIndices(t *testing.T) {
	sm := NewSelectionManager()
	messages := createTestMessages(10)
	
	// Select messages at indices 7, 2, 5 (out of order)
	sm.SelectMessage(7, messages)
	sm.SelectMessageMultiple(2, true, false, messages)
	sm.SelectMessageMultiple(5, true, false, messages)
	
	indices := sm.GetSelectedMessageIndices()
	
	if len(indices) != 3 {
		t.Errorf("Should have 3 selected indices, got %d", len(indices))
	}
	
	// Check that indices are sorted
	expectedIndices := []int{2, 5, 7}
	for i, index := range indices {
		if index != expectedIndices[i] {
			t.Errorf("Index %d: got %d, want %d", i, index, expectedIndices[i])
		}
	}
}

func TestMultiSelectionMode(t *testing.T) {
	sm := NewSelectionManager()
	
	if sm.IsMultiSelectionMode() {
		t.Error("Multi-selection mode should be false by default")
	}
	
	sm.SetMultiSelectionMode(true)
	
	if !sm.IsMultiSelectionMode() {
		t.Error("Multi-selection mode should be true after setting")
	}
	
	sm.SetMultiSelectionMode(false)
	
	if sm.IsMultiSelectionMode() {
		t.Error("Multi-selection mode should be false after unsetting")
	}
}

func TestGetSelectionCount(t *testing.T) {
	sm := NewSelectionManager()
	messages := createTestMessages(10)
	
	if sm.GetSelectionCount() != 0 {
		t.Error("Selection count should be 0 initially")
	}
	
	sm.SelectMessage(0, messages)
	if sm.GetSelectionCount() != 1 {
		t.Errorf("Selection count should be 1, got %d", sm.GetSelectionCount())
	}
	
	sm.SelectMessageMultiple(2, true, false, messages)
	if sm.GetSelectionCount() != 2 {
		t.Errorf("Selection count should be 2, got %d", sm.GetSelectionCount())
	}
	
	sm.ClearSelection()
	if sm.GetSelectionCount() != 0 {
		t.Error("Selection count should be 0 after clear")
	}
}

func TestHasSelection(t *testing.T) {
	sm := NewSelectionManager()
	messages := createTestMessages(5)
	
	if sm.HasSelection() {
		t.Error("Should not have selection initially")
	}
	
	sm.SelectMessage(0, messages)
	if !sm.HasSelection() {
		t.Error("Should have selection after selecting a message")
	}
	
	sm.ClearSelection()
	if sm.HasSelection() {
		t.Error("Should not have selection after clear")
	}
}

