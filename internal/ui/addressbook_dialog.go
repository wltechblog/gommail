package ui

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/wltechblog/gommail/internal/addressbook"
	"github.com/wltechblog/gommail/internal/config"
)

// AddressbookDialog represents the addressbook management dialog
type AddressbookDialog struct {
	app            fyne.App
	window         fyne.Window
	addressbookMgr *addressbook.Manager
	config         config.ConfigManager

	// UI components
	accountSelect   *widget.Select
	searchEntry     *widget.Entry
	contactList     *widget.List
	selectedContact *addressbook.Contact

	// Contact editor components
	nameEntry  *widget.Entry
	emailEntry *widget.Entry
	notesEntry *widget.Entry

	// Buttons
	addButton    *widget.Button
	editButton   *widget.Button
	deleteButton *widget.Button
	saveButton   *widget.Button
	cancelButton *widget.Button

	// Data
	contacts         []*addressbook.Contact
	filteredContacts []*addressbook.Contact
	currentAccount   string

	// Callbacks
	onClosed func()
}

// AddressbookDialogOptions contains options for creating an addressbook dialog
type AddressbookDialogOptions struct {
	OnClosed func()
}

// NewAddressbookDialog creates a new addressbook dialog
func NewAddressbookDialog(app fyne.App, addressbookMgr *addressbook.Manager, cfg config.ConfigManager, opts AddressbookDialogOptions) *AddressbookDialog {
	window := app.NewWindow("Address Book")
	window.Resize(fyne.NewSize(800, 600))

	ad := &AddressbookDialog{
		app:            app,
		window:         window,
		addressbookMgr: addressbookMgr,
		config:         cfg,
		onClosed:       opts.OnClosed,
	}

	ad.setupUI()
	ad.loadContacts()

	// Handle window close
	window.SetCloseIntercept(func() {
		if ad.onClosed != nil {
			ad.onClosed()
		}
		window.Close()
	})

	return ad
}

// Show displays the addressbook dialog
func (ad *AddressbookDialog) Show() {
	ad.window.Show()
}

// setupUI initializes the user interface
func (ad *AddressbookDialog) setupUI() {
	// Create account selector
	accounts := ad.config.GetAccounts()
	accountNames := make([]string, 0, len(accounts)+1)
	accountNames = append(accountNames, "All Accounts")
	for _, account := range accounts {
		accountNames = append(accountNames, account.Name)
	}

	ad.accountSelect = widget.NewSelect(accountNames, func(selected string) {
		if selected == "All Accounts" {
			ad.currentAccount = ""
		} else {
			ad.currentAccount = selected
		}
		ad.filterContacts()
	})

	// Create search entry
	ad.searchEntry = widget.NewEntry()
	ad.searchEntry.SetPlaceHolder("Search contacts...")
	ad.searchEntry.OnChanged = func(text string) {
		ad.filterContacts()
	}

	// Create contact list
	ad.contactList = widget.NewList(
		func() int {
			return len(ad.filteredContacts)
		},
		func() fyne.CanvasObject {
			return container.NewHBox(
				widget.NewLabel("Contact Name"),
				widget.NewLabel("email@example.com"),
			)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id >= len(ad.filteredContacts) {
				return
			}
			contact := ad.filteredContacts[id]
			hbox := obj.(*fyne.Container)
			nameLabel := hbox.Objects[0].(*widget.Label)
			emailLabel := hbox.Objects[1].(*widget.Label)

			nameLabel.SetText(contact.DisplayName())
			emailLabel.SetText(contact.Email)
		},
	)

	ad.contactList.OnSelected = func(id widget.ListItemID) {
		if id >= 0 && id < len(ad.filteredContacts) {
			ad.selectedContact = ad.filteredContacts[id]
			ad.loadContactForEditing()
			ad.updateButtonStates()
		}
	}

	// Create contact editor
	ad.nameEntry = widget.NewEntry()
	ad.nameEntry.SetPlaceHolder("Contact name")

	ad.emailEntry = widget.NewEntry()
	ad.emailEntry.SetPlaceHolder("email@example.com")

	ad.notesEntry = widget.NewMultiLineEntry()
	ad.notesEntry.SetPlaceHolder("Notes (optional)")
	ad.notesEntry.Resize(fyne.NewSize(0, 100))

	// Create buttons
	ad.addButton = widget.NewButton("Add", ad.addContact)
	ad.editButton = widget.NewButton("Edit", ad.editContact)
	ad.deleteButton = widget.NewButton("Delete", ad.deleteContact)
	ad.saveButton = widget.NewButton("Save", ad.saveContact)
	ad.cancelButton = widget.NewButton("Cancel", ad.cancelEdit)

	// Initially disable edit/delete buttons
	ad.updateButtonStates()

	// Create layout
	leftPanel := container.NewVBox(
		widget.NewLabel("Account:"),
		ad.accountSelect,
		widget.NewSeparator(),
		widget.NewLabel("Search:"),
		ad.searchEntry,
		widget.NewSeparator(),
		widget.NewLabel("Contacts:"),
		container.NewScroll(ad.contactList),
	)

	rightPanel := container.NewVBox(
		widget.NewLabel("Contact Details:"),
		widget.NewForm(
			widget.NewFormItem("Name:", ad.nameEntry),
			widget.NewFormItem("Email:", ad.emailEntry),
			widget.NewFormItem("Notes:", ad.notesEntry),
		),
		widget.NewSeparator(),
		container.NewHBox(
			ad.addButton,
			ad.editButton,
			ad.deleteButton,
		),
		container.NewHBox(
			ad.saveButton,
			ad.cancelButton,
		),
	)

	// Create main layout with splitter
	content := container.NewHSplit(leftPanel, rightPanel)
	content.SetOffset(0.5) // 50/50 split

	// Add close button at bottom
	closeButton := widget.NewButton("Close", func() {
		ad.window.Close()
	})

	mainContent := container.NewVBox(
		content,
		widget.NewSeparator(),
		container.NewHBox(
			widget.NewLabel(fmt.Sprintf("Total contacts: %d", len(ad.contacts))),
			layout.NewSpacer(),
			closeButton,
		),
	)

	ad.window.SetContent(mainContent)

	// Set default selection after ALL UI components are created
	ad.accountSelect.SetSelected("All Accounts")
}

// loadContacts loads all contacts from the addressbook manager
func (ad *AddressbookDialog) loadContacts() {
	ad.contacts = ad.addressbookMgr.ListContacts("")
	ad.filterContacts()
}

// filterContacts filters contacts based on current search and account selection
func (ad *AddressbookDialog) filterContacts() {
	// Safety check: ensure UI components are initialized
	if ad.searchEntry == nil || ad.contactList == nil {
		return
	}

	searchText := strings.TrimSpace(ad.searchEntry.Text)

	if searchText == "" {
		// No search filter, just filter by account
		ad.filteredContacts = ad.addressbookMgr.ListContacts(ad.currentAccount)
	} else {
		// Apply both search and account filters
		ad.filteredContacts = ad.addressbookMgr.SearchContacts(searchText, ad.currentAccount)
	}

	ad.contactList.Refresh()
	ad.updateContactCount()
}

// updateContactCount updates the contact count display
func (ad *AddressbookDialog) updateContactCount() {
	// This would need to be implemented by updating the label in the UI
	// For now, we'll refresh the entire window content when needed
}

// loadContactForEditing loads the selected contact into the editor fields
func (ad *AddressbookDialog) loadContactForEditing() {
	if ad.selectedContact == nil {
		ad.clearEditor()
		return
	}

	ad.nameEntry.SetText(ad.selectedContact.Name)
	ad.emailEntry.SetText(ad.selectedContact.Email)
	ad.notesEntry.SetText(ad.selectedContact.Notes)
}

// clearEditor clears all editor fields
func (ad *AddressbookDialog) clearEditor() {
	ad.nameEntry.SetText("")
	ad.emailEntry.SetText("")
	ad.notesEntry.SetText("")
}

// updateButtonStates updates the enabled/disabled state of buttons
func (ad *AddressbookDialog) updateButtonStates() {
	hasSelection := ad.selectedContact != nil
	ad.editButton.Enable()
	ad.deleteButton.Enable()

	if !hasSelection {
		ad.editButton.Disable()
		ad.deleteButton.Disable()
	}

	// Save/Cancel buttons are initially hidden
	ad.saveButton.Hide()
	ad.cancelButton.Hide()
}

// addContact handles adding a new contact
func (ad *AddressbookDialog) addContact() {
	ad.clearEditor()
	ad.selectedContact = nil
	ad.contactList.UnselectAll()

	// Show save/cancel buttons, hide others
	ad.saveButton.Show()
	ad.cancelButton.Show()
	ad.addButton.Hide()
	ad.editButton.Hide()
	ad.deleteButton.Hide()

	// Focus on name entry
	ad.window.Canvas().Focus(ad.nameEntry)
}

// editContact handles editing the selected contact
func (ad *AddressbookDialog) editContact() {
	if ad.selectedContact == nil {
		return
	}

	// Show save/cancel buttons, hide others
	ad.saveButton.Show()
	ad.cancelButton.Show()
	ad.addButton.Hide()
	ad.editButton.Hide()
	ad.deleteButton.Hide()

	// Focus on name entry
	ad.window.Canvas().Focus(ad.nameEntry)
}

// deleteContact handles deleting the selected contact
func (ad *AddressbookDialog) deleteContact() {
	if ad.selectedContact == nil {
		return
	}

	dialog.ShowConfirm("Delete Contact",
		fmt.Sprintf("Are you sure you want to delete '%s'?", ad.selectedContact.DisplayName()),
		func(confirmed bool) {
			if confirmed {
				err := ad.addressbookMgr.RemoveContact(ad.selectedContact.ID)
				if err != nil {
					dialog.ShowError(fmt.Errorf("failed to delete contact: %w", err), ad.window)
					return
				}

				ad.loadContacts()
				ad.clearEditor()
				ad.selectedContact = nil
				ad.updateButtonStates()
			}
		}, ad.window)
}

// saveContact handles saving the current contact (add or edit)
func (ad *AddressbookDialog) saveContact() {
	name := strings.TrimSpace(ad.nameEntry.Text)
	email := strings.TrimSpace(ad.emailEntry.Text)
	notes := strings.TrimSpace(ad.notesEntry.Text)

	// Validate required fields
	if email == "" {
		dialog.ShowError(fmt.Errorf("email address is required"), ad.window)
		return
	}

	// Determine account for new contacts
	accountName := ad.currentAccount
	if accountName == "" {
		// If "All Accounts" is selected, use the first available account
		accounts := ad.config.GetAccounts()
		if len(accounts) > 0 {
			accountName = accounts[0].Name
		} else {
			dialog.ShowError(fmt.Errorf("no accounts available"), ad.window)
			return
		}
	}

	var err error
	if ad.selectedContact == nil {
		// Adding new contact
		contact := addressbook.NewContact(name, email, accountName)
		contact.Notes = notes
		err = ad.addressbookMgr.AddContact(contact)
	} else {
		// Editing existing contact
		ad.selectedContact.Update(name, notes)
		err = ad.addressbookMgr.AddContact(ad.selectedContact) // AddContact handles updates
	}

	if err != nil {
		dialog.ShowError(fmt.Errorf("failed to save contact: %w", err), ad.window)
		return
	}

	ad.loadContacts()
	ad.cancelEdit()
}

// cancelEdit cancels the current add/edit operation
func (ad *AddressbookDialog) cancelEdit() {
	ad.loadContactForEditing()

	// Show normal buttons, hide save/cancel
	ad.addButton.Show()
	ad.editButton.Show()
	ad.deleteButton.Show()
	ad.saveButton.Hide()
	ad.cancelButton.Hide()

	ad.updateButtonStates()
}
