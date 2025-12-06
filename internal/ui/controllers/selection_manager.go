// Package controllers provides UI controller implementations
package controllers

import (
	"sort"
	"sync"

	"github.com/wltechblog/gommail/internal/email"
	"github.com/wltechblog/gommail/internal/logging"
)

// SelectionManagerImpl implements the SelectionManager interface.
// It manages message selection state including single and multiple selection.
type SelectionManagerImpl struct {
	// Selection state
	selectedMessages   map[int]bool // Map of message indices to selection state
	lastSelectedIndex  int          // For shift-click range selection
	selectionMutex     sync.RWMutex // Protect selectedMessages map
	multiSelectionMode bool         // Toggle for multiple selection mode

	// Reference to current selected message (for single selection display)
	selectedMessage *email.MessageIndexItem

	// Callbacks for UI updates
	onSelectionChanged func()
	onMessageSelected  func(index int)

	// Logger
	logger *logging.Logger
}

// NewSelectionManager creates a new SelectionManager instance.
func NewSelectionManager() *SelectionManagerImpl {
	return &SelectionManagerImpl{
		selectedMessages:  make(map[int]bool),
		lastSelectedIndex: -1,
		logger:            logging.NewComponent("selection"),
	}
}

// SetCallbacks sets the callback functions for UI updates.
// onSelectionChanged is called when the selection state changes (for list refresh).
// onMessageSelected is called when a message is selected (for content display).
func (sm *SelectionManagerImpl) SetCallbacks(onSelectionChanged func(), onMessageSelected func(index int)) {
	sm.onSelectionChanged = onSelectionChanged
	sm.onMessageSelected = onMessageSelected
}

// IsMessageSelected returns true if the message at the given index is selected.
func (sm *SelectionManagerImpl) IsMessageSelected(index int) bool {
	sm.selectionMutex.RLock()
	defer sm.selectionMutex.RUnlock()
	return sm.selectedMessages[index]
}

// GetSelectedMessageIndices returns a slice of all selected message indices.
func (sm *SelectionManagerImpl) GetSelectedMessageIndices() []int {
	sm.selectionMutex.RLock()
	defer sm.selectionMutex.RUnlock()
	return sm.getSelectedMessageIndicesUnsafe()
}

// getSelectedMessageIndicesUnsafe returns selected indices without locking.
// For internal use when already locked.
func (sm *SelectionManagerImpl) getSelectedMessageIndicesUnsafe() []int {
	var indices []int
	for index, selected := range sm.selectedMessages {
		if selected {
			indices = append(indices, index)
		}
	}
	sort.Ints(indices)
	return indices
}

// GetSelectedMessages returns a slice of all selected MessageIndexItems.
// This requires access to the messages array, so it takes it as a parameter.
func (sm *SelectionManagerImpl) GetSelectedMessages(messages []email.MessageIndexItem) []*email.MessageIndexItem {
	indices := sm.GetSelectedMessageIndices()
	result := make([]*email.MessageIndexItem, 0, len(indices))

	for _, index := range indices {
		if index >= 0 && index < len(messages) {
			result = append(result, &messages[index])
		}
	}

	return result
}

// GetSelectedMessage returns the currently selected message for display.
func (sm *SelectionManagerImpl) GetSelectedMessage() *email.MessageIndexItem {
	sm.selectionMutex.RLock()
	defer sm.selectionMutex.RUnlock()
	return sm.selectedMessage
}

// SetSelectedMessage sets the currently selected message for display.
func (sm *SelectionManagerImpl) SetSelectedMessage(msg *email.MessageIndexItem) {
	sm.selectionMutex.Lock()
	defer sm.selectionMutex.Unlock()
	sm.selectedMessage = msg
}

// SelectMessage handles single message selection.
// This is called when a message is clicked without modifiers.
func (sm *SelectionManagerImpl) SelectMessage(index int, messages []email.MessageIndexItem) {
	sm.selectionMutex.Lock()
	defer sm.selectionMutex.Unlock()

	if index < 0 || index >= len(messages) {
		sm.logger.Warn("SelectMessage: Invalid index %d (messageCount=%d)", index, len(messages))
		return
	}

	// Clear all selections and select only this one
	sm.selectedMessages = make(map[int]bool)
	sm.selectedMessages[index] = true
	sm.lastSelectedIndex = index
	sm.selectedMessage = &messages[index]

	// Trigger callbacks
	if sm.onSelectionChanged != nil {
		sm.onSelectionChanged()
	}
	if sm.onMessageSelected != nil {
		sm.onMessageSelected(index)
	}
}

// SelectMessageMultiple handles multiple message selection with Ctrl/Shift modifiers.
func (sm *SelectionManagerImpl) SelectMessageMultiple(index int, ctrlPressed, shiftPressed bool, messages []email.MessageIndexItem) {
	if index < 0 || index >= len(messages) {
		return
	}

	sm.selectionMutex.Lock()
	defer sm.selectionMutex.Unlock()

	if shiftPressed && sm.lastSelectedIndex >= 0 {
		// Range selection: select all messages between lastSelectedIndex and current index
		start := sm.lastSelectedIndex
		end := index
		if start > end {
			start, end = end, start
		}

		// Clear existing selection if Ctrl is not pressed
		if !ctrlPressed {
			sm.selectedMessages = make(map[int]bool)
		}

		// Select range
		for i := start; i <= end; i++ {
			sm.selectedMessages[i] = true
		}
	} else if ctrlPressed {
		// Toggle selection of current message
		sm.selectedMessages[index] = !sm.selectedMessages[index]
		sm.lastSelectedIndex = index
	} else {
		// Single selection: clear all others and select this one
		sm.selectedMessages = make(map[int]bool)
		sm.selectedMessages[index] = true
		sm.lastSelectedIndex = index
	}

	// Update the primary selected message for display
	if sm.selectedMessages[index] {
		sm.selectedMessage = &messages[index]
		if sm.onMessageSelected != nil {
			sm.onMessageSelected(index)
		}
	} else if len(sm.selectedMessages) > 0 {
		// If current message was deselected but others are selected, show the first selected one
		indices := sm.getSelectedMessageIndicesUnsafe()
		if len(indices) > 0 {
			sm.selectedMessage = &messages[indices[0]]
			if sm.onMessageSelected != nil {
				sm.onMessageSelected(indices[0])
			}
		}
	} else {
		// No messages selected
		sm.selectedMessage = nil
	}

	// Trigger selection changed callback
	if sm.onSelectionChanged != nil {
		sm.onSelectionChanged()
	}
}

// SelectAllMessages selects all messages in the current list.
func (sm *SelectionManagerImpl) SelectAllMessages(messages []email.MessageIndexItem) {
	sm.selectionMutex.Lock()
	defer sm.selectionMutex.Unlock()

	sm.selectedMessages = make(map[int]bool)
	for i := 0; i < len(messages); i++ {
		sm.selectedMessages[i] = true
	}

	if len(messages) > 0 {
		sm.selectedMessage = &messages[0]
		sm.lastSelectedIndex = 0
		if sm.onMessageSelected != nil {
			sm.onMessageSelected(0)
		}
	}

	if sm.onSelectionChanged != nil {
		sm.onSelectionChanged()
	}
}

// ClearSelection clears all message selections.
func (sm *SelectionManagerImpl) ClearSelection() {
	sm.selectionMutex.Lock()
	defer sm.selectionMutex.Unlock()

	sm.selectedMessages = make(map[int]bool)
	sm.selectedMessage = nil
	sm.lastSelectedIndex = -1

	if sm.onSelectionChanged != nil {
		sm.onSelectionChanged()
	}
}

// IsMultiSelectionMode returns whether multi-selection mode is enabled.
func (sm *SelectionManagerImpl) IsMultiSelectionMode() bool {
	sm.selectionMutex.RLock()
	defer sm.selectionMutex.RUnlock()
	return sm.multiSelectionMode
}

// SetMultiSelectionMode enables or disables multi-selection mode.
func (sm *SelectionManagerImpl) SetMultiSelectionMode(enabled bool) {
	sm.selectionMutex.Lock()
	defer sm.selectionMutex.Unlock()
	sm.multiSelectionMode = enabled
}

// UpdateLastSelectedIndex updates the last selected index.
func (sm *SelectionManagerImpl) UpdateLastSelectedIndex(index int) {
	sm.selectionMutex.Lock()
	defer sm.selectionMutex.Unlock()
	sm.lastSelectedIndex = index
}

// GetLastSelectedIndex returns the last selected index.
func (sm *SelectionManagerImpl) GetLastSelectedIndex() int {
	sm.selectionMutex.RLock()
	defer sm.selectionMutex.RUnlock()
	return sm.lastSelectedIndex
}

// GetSelectionCount returns the number of selected messages.
func (sm *SelectionManagerImpl) GetSelectionCount() int {
	sm.selectionMutex.RLock()
	defer sm.selectionMutex.RUnlock()
	return len(sm.selectedMessages)
}

// HasSelection returns true if any messages are selected.
func (sm *SelectionManagerImpl) HasSelection() bool {
	sm.selectionMutex.RLock()
	defer sm.selectionMutex.RUnlock()
	return len(sm.selectedMessages) > 0
}
