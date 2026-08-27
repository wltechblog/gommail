package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"

	"github.com/wltechblog/gommail/internal/email"
	"github.com/wltechblog/gommail/internal/logging"
	"github.com/wltechblog/gommail/internal/ui/controllers"
)

// newMultiSelectTestMainWindow builds a minimal MainWindow with a selection
// manager and a small message set for exercising messageListItem selection
// behaviour.
func newMultiSelectTestMainWindow(t *testing.T) *MainWindow {
	t.Helper()
	app := test.NewApp()

	mockConfig := newMockConfigManager(nil)
	mw := &MainWindow{
		logger:            logging.NewComponent("ui-test"),
		config:            mockConfig,
		accountController: controllers.NewAccountController(mockConfig, nil),
		selectionManager:  controllers.NewSelectionManager(),
		// TappedSecondary shows a context-menu popup, which needs a canvas.
		window: app.NewWindow("test"),
	}
	mw.messages = []email.MessageIndexItem{
		{Message: email.Message{UID: 1, Subject: "one"}},
		{Message: email.Message{UID: 2, Subject: "two"}},
		{Message: email.Message{UID: 3, Subject: "three"}},
		{Message: email.Message{UID: 4, Subject: "four"}},
	}
	return mw
}

func multiSelectIndices(t *testing.T, mw *MainWindow) []int {
	t.Helper()
	indices := mw.getSelectedMessageIndices()
	if len(indices) == 0 {
		return nil
	}
	return indices
}

func assertIndices(t *testing.T, mw *MainWindow, want ...int) {
	t.Helper()
	got := multiSelectIndices(t, mw)
	if len(got) != len(want) {
		t.Fatalf("expected selected indices %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected selected indices %v, got %v", want, got)
		}
	}
}

// clickItem simulates the desktop event sequence for a click: MouseDown with
// modifiers, MouseUp, then the synthesized Tapped.
func clickItem(item *messageListItem, modifiers fyne.KeyModifier) {
	item.MouseDown(&desktop.MouseEvent{Modifier: modifiers})
	item.MouseUp(&desktop.MouseEvent{})
	item.Tapped(&fyne.PointEvent{})
}

func TestMultiSelect_PlainClickSelectsSingle(t *testing.T) {
	mw := newMultiSelectTestMainWindow(t)
	item := newMessageListItem(mw)
	item.updateContent(2, &mw.messages[2])

	clickItem(item, 0)

	assertIndices(t, mw, 2)
}

func TestMultiSelect_CtrlClickToggles(t *testing.T) {
	mw := newMultiSelectTestMainWindow(t)
	a := newMessageListItem(mw)
	b := newMessageListItem(mw)

	a.updateContent(0, &mw.messages[0])
	b.updateContent(1, &mw.messages[1])

	// Plain click selects the first message
	clickItem(a, 0)
	assertIndices(t, mw, 0)

	// Ctrl+click adds the second message
	clickItem(b, fyne.KeyModifierControl)
	assertIndices(t, mw, 0, 1)

	// Ctrl+click again on the second message removes it
	clickItem(b, fyne.KeyModifierControl)
	assertIndices(t, mw, 0)

	// Ctrl+click on the only selected message clears the selection entirely
	clickItem(a, fyne.KeyModifierControl)
	assertIndices(t, mw)
}

func TestMultiSelect_ShiftClickExtendsRange(t *testing.T) {
	mw := newMultiSelectTestMainWindow(t)
	a := newMessageListItem(mw)
	c := newMessageListItem(mw)

	a.updateContent(0, &mw.messages[0])
	c.updateContent(2, &mw.messages[2])

	// Anchor on the first message, then Shift+click the third
	clickItem(a, 0)
	clickItem(c, fyne.KeyModifierShift)
	assertIndices(t, mw, 0, 1, 2)

	// Shift+click back to the second message shrinks the range to 0..1
	b := newMessageListItem(mw)
	b.updateContent(1, &mw.messages[1])
	clickItem(b, fyne.KeyModifierShift)
	assertIndices(t, mw, 0, 1)
}

func TestMultiSelect_CtrlShiftExtendsWithoutLosingSelection(t *testing.T) {
	mw := newMultiSelectTestMainWindow(t)
	a := newMessageListItem(mw)
	b := newMessageListItem(mw)
	d := newMessageListItem(mw)

	a.updateContent(0, &mw.messages[0])
	b.updateContent(1, &mw.messages[1])
	d.updateContent(3, &mw.messages[3])

	clickItem(a, 0)
	// Ctrl+click builds a sparse selection
	clickItem(d, fyne.KeyModifierControl)
	assertIndices(t, mw, 0, 3)

	// Ctrl+Shift+click extends a range from the last-clicked anchor (3) down
	// to 1 while keeping previously selected members (0), the same anchor
	// semantics used by desktop file managers.
	b.MouseDown(&desktop.MouseEvent{Modifier: fyne.KeyModifierControl | fyne.KeyModifierShift})
	b.MouseUp(&desktop.MouseEvent{})
	b.Tapped(&fyne.PointEvent{})
	assertIndices(t, mw, 0, 1, 2, 3)
}

func TestMultiSelect_PlainClickCollapsesMultiSelection(t *testing.T) {
	mw := newMultiSelectTestMainWindow(t)
	a := newMessageListItem(mw)
	b := newMessageListItem(mw)

	a.updateContent(0, &mw.messages[0])
	b.updateContent(1, &mw.messages[1])

	clickItem(a, 0)
	clickItem(b, fyne.KeyModifierControl)
	assertIndices(t, mw, 0, 1)

	// A plain click anywhere resets to a single selection
	plain := newMessageListItem(mw)
	plain.updateContent(3, &mw.messages[3])
	clickItem(plain, 0)
	assertIndices(t, mw, 3)
}

func TestMultiSelect_ModifiersConsumedAfterClick(t *testing.T) {
	mw := newMultiSelectTestMainWindow(t)
	item := newMessageListItem(mw)
	item.updateContent(0, &mw.messages[0])

	// A Ctrl press followed by a plain click sequence must NOT leak the
	// modifier into a later interaction on the same item.
	item.MouseDown(&desktop.MouseEvent{Modifier: fyne.KeyModifierControl})
	item.MouseUp(&desktop.MouseEvent{})
	item.Tapped(&fyne.PointEvent{})
	assertIndices(t, mw, 0)

	// Second interaction without modifiers behaves as a plain click.
	other := newMessageListItem(mw)
	other.updateContent(2, &mw.messages[2])
	clickItem(other, 0)
	assertIndices(t, mw, 2)
}

func TestMultiSelect_SelectAllAndClear(t *testing.T) {
	mw := newMultiSelectTestMainWindow(t)

	mw.selectAllMessages()
	assertIndices(t, mw, 0, 1, 2, 3)

	// Clear via the selection manager directly; MainWindow.clearSelection
	// additionally resets the message viewer, which needs full UI wiring.
	mw.selectionManager.ClearSelection()
	assertIndices(t, mw)
}

func TestMultiSelect_RightClickOnUnselectedWithCtrl(t *testing.T) {
	mw := newMultiSelectTestMainWindow(t)
	a := newMessageListItem(mw)
	a.updateContent(0, &mw.messages[0])

	// Establish a selection on the first message
	clickItem(a, 0)

	// Right-click (with Ctrl held) on another message extends the selection
	b := newMessageListItem(mw)
	b.updateContent(2, &mw.messages[2])
	b.MouseDown(&desktop.MouseEvent{Modifier: fyne.KeyModifierControl})
	b.TappedSecondary(&fyne.PointEvent{})
	assertIndices(t, mw, 0, 2)
}
