package ui

// Code in this file was mechanically split from main_window.go.

import (
	"fmt"
	_ "image/gif"  // Register GIF format
	_ "image/jpeg" // Register JPEG format
	_ "image/png"  // Register PNG format

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/wltechblog/gommail/internal/email"
)

// messageListWithRightClick wraps a Fyne List widget and adds right-click support
// It implements fyne.Widget and fyne.SecondaryTappable to handle right-click events
type messageListWithRightClick struct {
	widget.BaseWidget
	List       *widget.List
	mainWindow *MainWindow
}

// newMessageListWithRightClick creates a new message list
func newMessageListWithRightClick(mainWindow *MainWindow) *messageListWithRightClick {
	// Create the underlying list widget
	list := widget.NewList(
		func() int {
			return len(mainWindow.messages)
		},
		func() fyne.CanvasObject {
			// Create custom message list item that handles right-clicks
			return newMessageListItem(mainWindow)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			// Update the custom message list item
			if item, ok := obj.(*messageListItem); ok && id < len(mainWindow.messages) {
				item.updateContent(id, &mainWindow.messages[id])
			}
		},
	)

	// Handle message selection
	list.OnSelected = func(id widget.ListItemID) {
		mainWindow.logger.Debug("OnSelected: List widget selected ID=%d, messageCount=%d", id, len(mainWindow.messages))
		if id >= 0 && id < len(mainWindow.messages) {
			mainWindow.logger.Debug("OnSelected: Calling selectMessage(%d) for subject: %s", id, mainWindow.messages[id].Message.Subject)
			mainWindow.selectMessage(id)
		} else {
			mainWindow.logger.Warn("OnSelected: Invalid ID %d (messageCount=%d) - ignoring selection", id, len(mainWindow.messages))
		}
	}

	wrapper := &messageListWithRightClick{
		List:       list,
		mainWindow: mainWindow,
	}
	wrapper.ExtendBaseWidget(wrapper)

	return wrapper
}

// CreateRenderer creates a renderer for the messageListWithRightClick widget
func (m *messageListWithRightClick) CreateRenderer() fyne.WidgetRenderer {
	return &messageListRenderer{list: m.List}
}

// TappedSecondary handles right-click events on the message list
func (m *messageListWithRightClick) TappedSecondary(pe *fyne.PointEvent) {
	m.mainWindow.logger.Debug("TappedSecondary: Right-click at position %v", pe.Position)

	// The right-click should act on the currently selected message
	// We don't need to calculate which message was clicked because the user
	// should first left-click to select, then right-click for context menu
	if m.mainWindow.selectedMessage != nil {
		m.mainWindow.logger.Debug("TappedSecondary: Showing context menu for selected message: %s", m.mainWindow.selectedMessage.Message.Subject)
		// Convert the position to canvas coordinates and show context menu
		canvasPos := fyne.CurrentApp().Driver().AbsolutePositionForObject(m)
		canvasPos = canvasPos.Add(pe.Position)
		m.mainWindow.showMessageContextMenuAtPosition(canvasPos)
	} else {
		m.mainWindow.logger.Debug("TappedSecondary: No message selected, cannot show context menu")
	}
}

// messageListRenderer renders the message list widget
type messageListRenderer struct {
	list *widget.List
}

func (r *messageListRenderer) Layout(size fyne.Size) {
	r.list.Resize(size)
}

func (r *messageListRenderer) MinSize() fyne.Size {
	return r.list.MinSize()
}

func (r *messageListRenderer) Refresh() {
	r.list.Refresh()
}

func (r *messageListRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.list}
}

func (r *messageListRenderer) Destroy() {
	// Nothing to destroy
}

type messageListColumnsLayout struct {
	dateWidth   float32
	senderWidth float32
}

func (l *messageListColumnsLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 3 {
		return
	}

	height := size.Height
	if height <= 0 {
		height = objects[0].MinSize().Height
	}

	dateObj := objects[0]
	senderObj := objects[1]
	subjectObj := objects[2]

	dateObj.Resize(fyne.NewSize(l.dateWidth, height))
	dateObj.Move(fyne.NewPos(0, 0))

	senderObj.Resize(fyne.NewSize(l.senderWidth, height))
	senderObj.Move(fyne.NewPos(l.dateWidth, 0))

	subjectWidth := size.Width - l.dateWidth - l.senderWidth
	if subjectWidth < 0 {
		subjectWidth = 0
	}

	subjectObj.Resize(fyne.NewSize(subjectWidth, height))
	subjectObj.Move(fyne.NewPos(l.dateWidth+l.senderWidth, 0))
}

func (l *messageListColumnsLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var height float32
	var subjectWidth float32

	if len(objects) >= 3 {
		subjectWidth = objects[2].MinSize().Width
	}

	if subjectWidth == 0 {
		subjectWidth = 50
	}

	for _, obj := range objects {
		if obj == nil {
			continue
		}

		if h := obj.MinSize().Height; h > height {
			height = h
		}
	}

	return fyne.NewSize(l.dateWidth+l.senderWidth+subjectWidth, height)
}

// messageListItem is a custom widget that represents a single message in the list
// It implements fyne.SecondaryTappable to handle right-click events directly
// and desktop.Mouseable to capture modifier keys (Ctrl/Shift) for multi-select,
// which fyne.PointEvent does not carry.
type messageListItem struct {
	widget.BaseWidget
	mainWindow   *MainWindow
	messageID    int
	dateLabel    *widget.Label
	senderLabel  *widget.Label
	subjectLabel *widget.Label
	content      *fyne.Container
	background   *canvas.Rectangle
	isSelected   bool

	// pendingModifiers holds the modifier state captured by MouseDown for the
	// click currently in progress. Fyne synthesizes Tapped after MouseUp, so
	// the modifiers must be stashed at press time and consumed by Tapped.
	// Event delivery (including the test driver) is single-threaded per
	// interaction, so no extra locking is needed.
	pendingModifiers fyne.KeyModifier
}

// newMessageListItem creates a new message list item widget
func newMessageListItem(mainWindow *MainWindow) *messageListItem {
	dateLabel := widget.NewLabel("Jan 1, 15:04")
	dateLabel.Truncation = fyne.TextTruncateEllipsis

	senderLabel := widget.NewLabel("Sender Name")
	senderLabel.Truncation = fyne.TextTruncateEllipsis

	subjectLabel := widget.NewLabel("Subject line that should be fully visible and not truncated")
	subjectLabel.Wrapping = fyne.TextWrapOff
	subjectLabel.Truncation = fyne.TextTruncateEllipsis

	content := container.New(&messageListColumnsLayout{
		dateWidth:   messageDateColumnWidth,
		senderWidth: messageSenderColumnWidth,
	}, dateLabel, senderLabel, subjectLabel)

	background := canvas.NewRectangle(theme.Color(theme.ColorNameBackground))
	background.Hide()

	item := &messageListItem{
		mainWindow:   mainWindow,
		messageID:    -1,
		dateLabel:    dateLabel,
		senderLabel:  senderLabel,
		subjectLabel: subjectLabel,
		content:      content,
		background:   background,
		isSelected:   false,
	}
	item.ExtendBaseWidget(item)
	return item
}

// CreateRenderer creates a renderer for the message list item
func (m *messageListItem) CreateRenderer() fyne.WidgetRenderer {
	return &messageListItemRenderer{item: m}
}

// messageListItemRenderer renders the message list item
type messageListItemRenderer struct {
	item *messageListItem
}

func (r *messageListItemRenderer) Layout(size fyne.Size) {
	r.item.background.Resize(size)
	r.item.content.Resize(size)
}

func (r *messageListItemRenderer) MinSize() fyne.Size {
	return r.item.content.MinSize()
}

func (r *messageListItemRenderer) Refresh() {
	if r.item.isSelected {
		r.item.background.FillColor = theme.Color(theme.ColorNameSelection)
		r.item.background.Show()
	} else {
		r.item.background.Hide()
	}
	r.item.background.Refresh()
	r.item.content.Refresh()
}

func (r *messageListItemRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.item.background, r.item.content}
}

func (r *messageListItemRenderer) Destroy() {
	// Nothing to destroy
}

// setSelected updates the selection state of the message list item
func (m *messageListItem) setSelected(selected bool) {
	if m.isSelected != selected {
		m.isSelected = selected
		m.Refresh()
	}
}

// MouseDown captures the modifier keys held when a click starts. fyne's
// PointEvent does not include modifiers (multi-select remains an open
// upstream request, fyne-io/fyne#6341), so this is the only reliable place to
// observe Ctrl/Shift for the ensuing Tapped.
func (m *messageListItem) MouseDown(me *desktop.MouseEvent) {
	m.pendingModifiers = me.Modifier
}

// MouseUp implements desktop.Mouseable. Tapped is synthesized after MouseUp,
// so pendingModifiers are intentionally kept for Tapped to consume.
func (m *messageListItem) MouseUp(_ *desktop.MouseEvent) {
}

// Tapped handles left-click events on the message item
func (m *messageListItem) Tapped(pe *fyne.PointEvent) {
	m.mainWindow.logger.Debug("messageListItem.Tapped: Left-click on message ID=%d", m.messageID)

	// Validate message ID bounds before selection
	if m.messageID < 0 || m.messageID >= len(m.mainWindow.messages) {
		m.mainWindow.logger.Warn("messageListItem.Tapped: Invalid message ID %d (messageCount=%d) - ignoring click", m.messageID, len(m.mainWindow.messages))
		return
	}

	m.mainWindow.logger.Debug("messageListItem.Tapped: Selecting message %d: %s", m.messageID, m.mainWindow.messages[m.messageID].Message.Subject)

	// Consume the modifiers captured at mouse-down time:
	// Ctrl+click toggles a message, Shift+click extends a range from the
	// last selection, plain click collapses to a single selection.
	ctrlPressed := m.pendingModifiers&fyne.KeyModifierControl != 0
	shiftPressed := m.pendingModifiers&fyne.KeyModifierShift != 0
	m.pendingModifiers = 0

	m.mainWindow.selectMessageMultiple(m.messageID, ctrlPressed, shiftPressed)

	// Also trigger the List widget's selection to keep it in sync
	if m.mainWindow.messageListWithRightClick != nil {
		m.mainWindow.messageListWithRightClick.List.Select(m.messageID)
	}
}

// TappedSecondary handles right-click events on the message item
func (m *messageListItem) TappedSecondary(pe *fyne.PointEvent) {
	m.mainWindow.logger.Debug("messageListItem.TappedSecondary: Right-click on message ID=%d", m.messageID)

	// Validate message ID bounds before selection
	if m.messageID < 0 || m.messageID >= len(m.mainWindow.messages) {
		m.mainWindow.logger.Debug("messageListItem.TappedSecondary: Invalid message ID %d (messageCount=%d) - ignoring right-click", m.messageID, len(m.mainWindow.messages))
		m.mainWindow.logger.Warn("Message list item right-click with invalid ID %d (messageCount=%d)", m.messageID, len(m.mainWindow.messages))
		return
	}

	m.mainWindow.logger.Debug("messageListItem.TappedSecondary: Right-click on message %d: %s", m.messageID, m.mainWindow.messages[m.messageID].Message.Subject)

	// Right-click preserves the existing selection when it acts on a member
	// of it (so batch context-menu actions work); otherwise it behaves like
	// a modified click: extend the selection if one exists, or respect
	// Ctrl for toggle-add.
	ctrlPressed := m.pendingModifiers&fyne.KeyModifierControl != 0
	m.pendingModifiers = 0
	selectedMessages := m.mainWindow.getSelectedMessages()
	isCurrentlySelected := m.mainWindow.isMessageSelected(m.messageID)

	if len(selectedMessages) > 0 && !isCurrentlySelected {
		// Add this message to the existing selection
		m.mainWindow.selectMessageMultiple(m.messageID, true, false) // Ctrl+click behavior
	} else if !isCurrentlySelected {
		// Single selection
		m.mainWindow.selectMessageMultiple(m.messageID, ctrlPressed, false)
	}
	// If already selected, keep the selection as-is

	// Also trigger the List widget's selection to keep it in sync
	if m.mainWindow.messageListWithRightClick != nil {
		m.mainWindow.messageListWithRightClick.List.Select(m.messageID)
	}

	// Show context menu
	canvasPos := fyne.CurrentApp().Driver().AbsolutePositionForObject(m)
	canvasPos = canvasPos.Add(pe.Position)
	m.mainWindow.showMessageContextMenuAtPosition(canvasPos)
}

// updateContent updates the content of this message list item
func (m *messageListItem) updateContent(messageID int, message *email.MessageIndexItem) {
	m.messageID = messageID

	if message != nil {
		// Update labels with message data - use getDisplayDate for local timezone display
		displayDate := m.mainWindow.getDisplayDate(&message.Message)
		m.dateLabel.SetText(displayDate.Format("Jan 2, 15:04"))

		// Format sender name from address list
		senderName := "Unknown Sender"
		if len(message.Message.From) > 0 {
			if message.Message.From[0].Name != "" {
				senderName = message.Message.From[0].Name
			} else {
				senderName = message.Message.From[0].Email
			}
		}

		// If in unified inbox mode, prepend account name
		if m.mainWindow.accountController.IsUnifiedInbox() {
			senderName = fmt.Sprintf("[%s] %s", message.AccountName, senderName)
		}

		// Truncate long sender names to fit in column. Byte-slicing would
		// cut mid-rune and render invalid UTF-8 for non-ASCII names.
		if len(senderName) > 25 { // Increased limit to accommodate account name
			senderName = truncateRunes(senderName, 22) + "..."
		}
		m.senderLabel.SetText(senderName)

		// Check if message is read by looking at flags
		isRead := false
		for _, flag := range message.Message.Flags {
			if flag == "\\Seen" {
				isRead = true
				break
			}
		}

		// Format subject with unread indicator and apply styling
		subject := message.Message.Subject
		if subject == "" {
			subject = "(No Subject)"
		}

		if !isRead {
			// Apply bold styling for unread messages
			m.dateLabel.TextStyle = fyne.TextStyle{Bold: true}
			m.senderLabel.TextStyle = fyne.TextStyle{Bold: true}
			m.subjectLabel.TextStyle = fyne.TextStyle{Bold: true}
		} else {
			// Normal styling for read messages
			m.dateLabel.TextStyle = fyne.TextStyle{}
			m.senderLabel.TextStyle = fyne.TextStyle{}
			m.subjectLabel.TextStyle = fyne.TextStyle{}
		}
		m.subjectLabel.SetText(subject)

		// Refresh all labels to ensure TextStyle changes are applied
		// This is critical because TextStyle changes don't automatically trigger a refresh
		m.dateLabel.Refresh()
		m.senderLabel.Refresh()
		m.subjectLabel.Refresh()

		// Update selection highlighting based on whether this message is currently selected
		// Check both single selection and multiple selection
		isCurrentlySelected := (m.mainWindow.selectedMessage != nil &&
			m.mainWindow.selectedMessage.Message.UID == message.Message.UID &&
			m.mainWindow.selectedMessage.AccountName == message.AccountName) ||
			m.mainWindow.isMessageSelected(messageID)
		m.setSelected(isCurrentlySelected)
	}
}

// createMessageListWithRightClick creates a message list with right-click support
func (mw *MainWindow) createMessageListWithRightClick() fyne.CanvasObject {
	// Create message list with custom items that handle right-clicks directly
	mw.logger.Debug("createMessageListWithRightClick: Creating message list with custom items")
	return newMessageListWithRightClick(mw)
}

// applyTheme applies the theme setting from configuration to the Fyne app
