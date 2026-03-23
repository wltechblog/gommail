package ui

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/wltechblog/gommail/internal/addressbook"
)

// keyProxy is an invisible, zero-size focusable widget that is embedded inside
// the autocomplete popup overlay. In Fyne v2.7+, every widget.PopUp on the
// overlay stack gets its own FocusManager, which shadows the main canvas
// FocusManager. canvas.Focused() queries the top overlay's FocusManager first,
// so focusing the entry (which lives in the content tree) via canvas.Focus(entry)
// has no effect on routing – the overlay's FocusManager returns nil and all
// keystrokes are dropped. By placing keyProxy inside the popup and focusing it,
// we keep focus inside the overlay's FocusManager. keyProxy simply forwards
// every TypedRune / TypedKey event back to the parent AutocompleteEntry so the
// user can continue typing while the suggestion list is visible.
type keyProxy struct {
	widget.BaseWidget
	entry *AutocompleteEntry
}

func newKeyProxy(entry *AutocompleteEntry) *keyProxy {
	p := &keyProxy{entry: entry}
	p.ExtendBaseWidget(p)
	return p
}

// CreateRenderer returns an empty renderer – the proxy is invisible.
func (p *keyProxy) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewWithoutLayout())
}

// FocusGained / FocusLost are required by fyne.Focusable; nothing to do here.
func (p *keyProxy) FocusGained() {}
func (p *keyProxy) FocusLost()   {}

// TypedRune forwards character input to the underlying entry widget.
func (p *keyProxy) TypedRune(r rune) {
	p.entry.Entry.TypedRune(r)
}

// TypedKey forwards key events to the AutocompleteEntry which handles both
// dropdown navigation (Up / Down / Enter / Escape) and normal entry editing.
func (p *keyProxy) TypedKey(key *fyne.KeyEvent) {
	p.entry.TypedKey(key)
}

// AutocompleteEntry is a custom entry widget with email address autocompletion
type AutocompleteEntry struct {
	widget.Entry

	// Autocompletion components
	addressbookMgr *addressbook.Manager
	accountName    string
	suggestions    []*addressbook.Contact
	dropdown       *widget.List
	dropdownPopup  *widget.PopUp
	proxy          *keyProxy // receives keyboard focus while the popup is visible
	canvas         fyne.Canvas

	// Configuration
	maxSuggestions int
	minChars       int
	selectedIndex  int // Track selected index manually

	// suppressSuggestions prevents updateSuggestions from running during
	// programmatic text changes (e.g. when a suggestion is selected).
	suppressSuggestions bool
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
	entry.proxy = newKeyProxy(entry)
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
	if ae.suppressSuggestions {
		return
	}

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

	// Suppress suggestion updates while we programmatically change the text
	ae.suppressSuggestions = true

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

	// Re-focus the entry so the user can continue typing.
	// The overlay has been removed by hideDropdown, so canvas.Focus(ae) now
	// targets the content FocusManager where ae is already registered.
	ae.canvas.Focus(ae)

	ae.suppressSuggestions = false
}

// showDropdown displays the suggestion dropdown
func (ae *AutocompleteEntry) showDropdown() {
	if ae.dropdownPopup != nil {
		// Popup already exists — just refresh its content and reposition.
		ae.selectedIndex = -1
		ae.dropdown.Refresh()

		entryPos := fyne.CurrentApp().Driver().AbsolutePositionForObject(ae)
		entrySize := ae.Size()
		popupPos := fyne.NewPos(entryPos.X, entryPos.Y+entrySize.Height)
		ae.dropdownPopup.ShowAtPosition(popupPos)

		// Keep the proxy focused inside the overlay's FocusManager so that
		// keystrokes continue to be forwarded to the entry.
		ae.canvas.Focus(ae.proxy)
		return
	}

	// Reset selection
	ae.selectedIndex = -1

	// Refresh the dropdown list
	ae.dropdown.Refresh()

	// Build the popup content: the visible dropdown list together with the
	// invisible keyProxy. The proxy must be part of the popup widget tree so
	// that the overlay's FocusManager (created automatically by Fyne when the
	// PopUp is added to the overlay stack) can manage its focus. Without it,
	// canvas.Focused() returns nil while the overlay is visible and all
	// keystrokes are silently discarded.
	ae.dropdownPopup = widget.NewPopUp(
		container.NewStack(ae.dropdown, ae.proxy),
		ae.canvas,
	)

	// Position the popup below the entry
	entryPos := fyne.CurrentApp().Driver().AbsolutePositionForObject(ae)
	entrySize := ae.Size()

	popupPos := fyne.NewPos(entryPos.X, entryPos.Y+entrySize.Height)
	popupSize := fyne.NewSize(entrySize.Width, 150) // Fixed height for dropdown

	ae.dropdownPopup.Resize(popupSize)
	ae.dropdownPopup.ShowAtPosition(popupPos)

	// Focus the proxy inside the overlay so the overlay's FocusManager has a
	// focused widget. TypedRune / TypedKey on the proxy are forwarded to the
	// entry, preserving normal typing and navigation behaviour.
	ae.canvas.Focus(ae.proxy)
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

// FocusLost handles focus lost events.
// Note: with the keyProxy approach, FocusLost on the entry is only called
// when focus moves to a different content widget (e.g. the Subject field).
// Showing the popup does NOT trigger FocusLost on the entry because the
// proxy – which receives focus – lives in the overlay's FocusManager, leaving
// the content FocusManager (which owns the entry) undisturbed.
func (ae *AutocompleteEntry) FocusLost() {
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
