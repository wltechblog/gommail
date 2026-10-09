package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/wltechblog/gommail/internal/addressbook"
	"github.com/wltechblog/gommail/internal/config"
)

// newAutocompleteTestEnv wires an AutocompleteEntry into a real test-driven
// window with an address book containing one known contact.
func newAutocompleteTestEnv(t *testing.T) (fyne.Window, *AutocompleteEntry) {
	t.Helper()
	app := test.NewApp()
	window := app.NewWindow("autocomplete")
	window.Resize(fyne.NewSize(400, 300))

	configMgr := config.NewPreferencesConfig(app, "test")
	mgr := addressbook.NewManager(configMgr, "test")
	if err := mgr.AddContact(&addressbook.Contact{
		Name: "Alice Example", Email: "alice@example.com", AccountName: "test",
	}); err != nil {
		t.Fatalf("AddContact failed: %v", err)
	}

	entry := NewAutocompleteEntry(mgr, "test", window.Canvas())
	window.SetContent(container.NewVBox(widget.NewLabel("To:"), entry))
	window.ShowAndRun()

	return window, entry
}

// TestAutocompleteClickSelectsSuggestion verifies the full click path:
// tapping a dropdown row fires OnSelected, which fills the entry with the
// contact and closes the popup.
//
// Note: this uses the software test driver, whose tap dispatch resolves the
// first matching object rather than the last as the glfw driver does, so it
// cannot reproduce the stack-order bug fixed alongside it (proxy stacked
// after the list swallowed clicks on desktop); that fix is verified against
// the glfw hit-test semantics directly.
func TestAutocompleteClickSelectsSuggestion(t *testing.T) {
	window, entry := newAutocompleteTestEnv(t)
	defer window.Close()

	entry.SetText("ali")
	entry.updateSuggestions("ali")

	if len(entry.suggestions) != 1 {
		t.Fatalf("expected 1 suggestion, got %d", len(entry.suggestions))
	}
	if entry.dropdownPopup == nil || !entry.dropdownPopup.Visible() {
		t.Fatal("dropdown popup not visible")
	}

	// Tap on the first row of the dropdown, offset from the popup origin.
	pos := entry.dropdownPopup.Position().Add(fyne.NewPos(10, 10))
	test.TapCanvas(window.Canvas(), pos)

	if !strings.Contains(entry.Text, "alice@example.com") {
		t.Errorf("click did not select the suggestion; entry text = %q", entry.Text)
	}
	if entry.dropdownPopup != nil {
		t.Error("dropdown still visible after selection")
	}
}
