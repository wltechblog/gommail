package ui

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/wltechblog/gommail/internal/addressbook"
)

// AutocompleteEntry is a custom entry widget with email address autocompletion
type AutocompleteEntry struct {
	widget.Entry

	// Autocompletion components
	addressbookMgr *addressbook.Manager
	accountName    string
	suggestions    []*addressbook.Contact
	dropdown       *widget.List
	dropdownPopup  *widget.PopUp
	canvas         fyne.Canvas

	// Configuration
	maxSuggestions int
	minChars       int
	selectedIndex  int // Track selected index manually
}

// NewAutocompleteEntry creates a new autocomplete entry widget
func NewAutocompleteEntry(addressbookMgr *addressbook.Manager, accountName string, canvas fyne.Canvas) *AutocompleteEntry {
	entry := &AutocompleteEntry{
		addressbookMgr: addressbookMgr,
		accountName:    accountName,
		canvas:         canvas,
		maxSuggestions: 5,
		minChars:       2,
		selectedIndex:  -1, // No selection initially
	}

	entry.ExtendBaseWidget(entry)
	entry.setupAutocompletion()

	return entry
}

// setupAutocompletion configures the autocompletion behavior
func (ae *AutocompleteEntry) setupAutocompletion() {
	// Create dropdown list
	ae.dropdown = widget.NewList(
		func() int {
			return len(ae.suggestions)
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("Contact Name <email@example.com>")
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id >= 0 && id < len(ae.suggestions) {
				contact := ae.suggestions[id]
				label := obj.(*widget.Label)
				label.SetText(contact.String())
			}
		},
	)

	// Handle selection from dropdown
	ae.dropdown.OnSelected = func(id widget.ListItemID) {
		if id >= 0 && id < len(ae.suggestions) {
			ae.selectSuggestion(ae.suggestions[id])
		}
	}

	// Override the OnChanged callback to trigger autocompletion
	originalOnChanged := ae.Entry.OnChanged
	ae.Entry.OnChanged = func(text string) {
		// Call original callback if it exists
		if originalOnChanged != nil {
			originalOnChanged(text)
		}

		// Trigger autocompletion
		ae.updateSuggestions(text)
	}

	// Handle key events for navigation
	ae.Entry.OnSubmitted = func(text string) {
		ae.hideDropdown()
	}
}

// updateSuggestions updates the suggestion list based on current input
func (ae *AutocompleteEntry) updateSuggestions(text string) {
	// Get the current partial address being typed
	partial := ae.getCurrentPartial(text)

	if len(partial) < ae.minChars {
		ae.hideDropdown()
		return
	}

	// Get suggestions from addressbook
	ae.suggestions = ae.addressbookMgr.GetAutoCompleteMatches(partial, ae.accountName, ae.maxSuggestions)

	if len(ae.suggestions) == 0 {
		ae.hideDropdown()
		return
	}

	ae.showDropdown()
}

// getCurrentPartial extracts the current partial email address being typed
func (ae *AutocompleteEntry) getCurrentPartial(text string) string {
	// Handle comma-separated addresses
	parts := strings.Split(text, ",")
	if len(parts) == 0 {
		return ""
	}

	// Get the last part (currently being typed)
	lastPart := strings.TrimSpace(parts[len(parts)-1])

	// Extract email from "Name <email>" format if present
	if strings.Contains(lastPart, "<") && strings.Contains(lastPart, ">") {
		start := strings.LastIndex(lastPart, "<")
		end := strings.LastIndex(lastPart, ">")
		if start < end {
			return strings.TrimSpace(lastPart[start+1 : end])
		}
	}

	return lastPart
}

// selectSuggestion handles selection of a suggestion from the dropdown
func (ae *AutocompleteEntry) selectSuggestion(contact *addressbook.Contact) {
	text := ae.Entry.Text
	parts := strings.Split(text, ",")

	if len(parts) == 0 {
		ae.Entry.SetText(contact.String())
	} else {
		// Replace the last part with the selected contact
		parts[len(parts)-1] = " " + contact.String()
		ae.Entry.SetText(strings.Join(parts, ","))
	}

	ae.hideDropdown()

	// Move cursor to end
	ae.Entry.CursorColumn = len(ae.Entry.Text)
}

// showDropdown displays the suggestion dropdown
func (ae *AutocompleteEntry) showDropdown() {
	if ae.dropdownPopup != nil {
		ae.dropdownPopup.Hide()
	}

	// Reset selection
	ae.selectedIndex = -1

	// Refresh the dropdown list
	ae.dropdown.Refresh()

	// Create popup with the dropdown
	ae.dropdownPopup = widget.NewPopUp(
		container.NewBorder(nil, nil, nil, nil, ae.dropdown),
		ae.canvas,
	)

	// Position the popup below the entry
	entryPos := fyne.CurrentApp().Driver().AbsolutePositionForObject(ae)
	entrySize := ae.Size()

	popupPos := fyne.NewPos(entryPos.X, entryPos.Y+entrySize.Height)
	popupSize := fyne.NewSize(entrySize.Width, 150) // Fixed height for dropdown

	ae.dropdownPopup.Resize(popupSize)
	ae.dropdownPopup.ShowAtPosition(popupPos)
}

// hideDropdown hides the suggestion dropdown
func (ae *AutocompleteEntry) hideDropdown() {
	if ae.dropdownPopup != nil {
		ae.dropdownPopup.Hide()
		ae.dropdownPopup = nil
	}
	ae.selectedIndex = -1
}

// SetAccountName updates the account name for filtering suggestions
func (ae *AutocompleteEntry) SetAccountName(accountName string) {
	ae.accountName = accountName
}

// TypedKey handles key events for dropdown navigation
func (ae *AutocompleteEntry) TypedKey(key *fyne.KeyEvent) {
	switch key.Name {
	case fyne.KeyDown:
		if ae.dropdownPopup != nil && ae.dropdownPopup.Visible() {
			// Navigate down in dropdown
			if ae.selectedIndex < len(ae.suggestions)-1 {
				ae.selectedIndex++
				ae.dropdown.Select(ae.selectedIndex)
			}
			return
		}
	case fyne.KeyUp:
		if ae.dropdownPopup != nil && ae.dropdownPopup.Visible() {
			// Navigate up in dropdown
			if ae.selectedIndex > 0 {
				ae.selectedIndex--
				ae.dropdown.Select(ae.selectedIndex)
			}
			return
		}
	case fyne.KeyReturn, fyne.KeyEnter:
		if ae.dropdownPopup != nil && ae.dropdownPopup.Visible() {
			// Select current suggestion
			if ae.selectedIndex >= 0 && ae.selectedIndex < len(ae.suggestions) {
				ae.selectSuggestion(ae.suggestions[ae.selectedIndex])
			}
			return
		}
	case fyne.KeyEscape:
		if ae.dropdownPopup != nil && ae.dropdownPopup.Visible() {
			ae.hideDropdown()
			return
		}
	}

	// Call parent TypedKey for normal key handling
	ae.Entry.TypedKey(key)
}

// FocusLost handles focus lost events
func (ae *AutocompleteEntry) FocusLost() {
	// Hide dropdown when focus is lost
	ae.hideDropdown()
	ae.Entry.FocusLost()
}

// Tapped handles tap events
func (ae *AutocompleteEntry) Tapped(pe *fyne.PointEvent) {
	// Hide dropdown when entry is tapped (to start fresh)
	ae.hideDropdown()
	ae.Entry.Tapped(pe)
}

// SetMaxSuggestions sets the maximum number of suggestions to show
func (ae *AutocompleteEntry) SetMaxSuggestions(max int) {
	ae.maxSuggestions = max
}

// SetMinChars sets the minimum number of characters before showing suggestions
func (ae *AutocompleteEntry) SetMinChars(min int) {
	ae.minChars = min
}

// Destroy cleans up resources when the widget is destroyed
func (ae *AutocompleteEntry) Destroy() {
	ae.hideDropdown()
}
