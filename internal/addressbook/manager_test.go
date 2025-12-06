package addressbook

import (
	"bytes"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/wltechblog/gommail/internal/config"
	"github.com/wltechblog/gommail/internal/email"
)

func TestManagerBasicOperations(t *testing.T) {
	// Create test app
	app := test.NewApp()
	defer app.Quit()

	// Create config manager
	configMgr := config.NewPreferencesConfig(app, "test")

	// Create manager
	manager := NewManager(configMgr, "test")

	// Test adding contact
	contact := NewContact("John Doe", "john@example.com", "test-account")
	err := manager.AddContact(contact)
	if err != nil {
		t.Errorf("Failed to add contact: %v", err)
	}

	// Test retrieving contact
	retrieved, exists := manager.GetContact(contact.ID)
	if !exists {
		t.Error("Contact not found after adding")
	}
	if retrieved.Name != "John Doe" {
		t.Errorf("Expected name 'John Doe', got '%s'", retrieved.Name)
	}

	// Test contact count
	count := manager.GetContactCount("")
	if count != 1 {
		t.Errorf("Expected 1 contact, got %d", count)
	}

	// Test removing contact
	err = manager.RemoveContact(contact.ID)
	if err != nil {
		t.Errorf("Failed to remove contact: %v", err)
	}

	count = manager.GetContactCount("")
	if count != 0 {
		t.Errorf("Expected 0 contacts after removal, got %d", count)
	}
}

func TestManagerAutoCollect(t *testing.T) {
	// Create test app
	app := test.NewApp()
	defer app.Quit()

	// Create config manager
	configMgr := config.NewPreferencesConfig(app, "test")

	// Create manager with auto-collect enabled
	manager := NewManager(configMgr, "test")
	manager.SetAutoCollectEnabled(true)

	// Create test message
	message := &email.Message{
		To: []email.Address{
			{Name: "John Doe", Email: "john@example.com"},
			{Name: "Jane Smith", Email: "jane@example.com"},
		},
		CC: []email.Address{
			{Name: "Bob Johnson", Email: "bob@example.com"},
		},
	}

	// Auto-collect from message
	err := manager.AutoCollectFromMessage(message, "test-account")
	if err != nil {
		t.Errorf("Failed to auto-collect: %v", err)
	}

	// Verify contacts were added
	count := manager.GetContactCount("test-account")
	if count != 3 {
		t.Errorf("Expected 3 auto-collected contacts, got %d", count)
	}

	// Verify auto-added flag
	contact, exists := manager.GetContactByEmail("john@example.com", "test-account")
	if !exists {
		t.Error("Auto-collected contact not found")
	}
	if !contact.AutoAdded {
		t.Error("Expected AutoAdded flag to be true")
	}

	// Test with auto-collect disabled
	manager.SetAutoCollectEnabled(false)
	message2 := &email.Message{
		To: []email.Address{
			{Name: "New Person", Email: "new@example.com"},
		},
	}

	err = manager.AutoCollectFromMessage(message2, "test-account")
	if err != nil {
		t.Errorf("Failed to process message with auto-collect disabled: %v", err)
	}

	// Count should remain the same
	newCount := manager.GetContactCount("test-account")
	if newCount != count {
		t.Errorf("Expected count to remain %d with auto-collect disabled, got %d", count, newCount)
	}
}

func TestManagerSearch(t *testing.T) {
	// Create test app
	app := test.NewApp()
	defer app.Quit()

	// Create config manager
	configMgr := config.NewPreferencesConfig(app, "test")

	// Create manager
	manager := NewManager(configMgr, "test")

	// Add test contacts
	contacts := []*Contact{
		NewContact("John Doe", "john@example.com", "account1"),
		NewContact("Jane Smith", "jane@example.com", "account1"),
		NewContact("Bob Johnson", "bob@test.com", "account2"),
	}

	for _, contact := range contacts {
		manager.AddContact(contact)
	}

	// Test search
	results := manager.SearchContacts("john", "")
	if len(results) != 2 { // John Doe and Bob Johnson
		t.Errorf("Expected 2 results for 'john', got %d", len(results))
	}

	// Test search with account filter
	results = manager.SearchContacts("", "account1")
	if len(results) != 2 {
		t.Errorf("Expected 2 results for account1, got %d", len(results))
	}
}

func TestManagerAutoComplete(t *testing.T) {
	// Create test app
	app := test.NewApp()
	defer app.Quit()

	// Create config manager
	configMgr := config.NewPreferencesConfig(app, "test")

	// Create manager
	manager := NewManager(configMgr, "test")

	// Add test contacts
	contacts := []*Contact{
		NewContact("John Doe", "john@example.com", "test-account"),
		NewContact("Jane Smith", "jane@example.com", "test-account"),
		NewContact("Johnny Cash", "johnny@music.com", "test-account"),
	}

	for _, contact := range contacts {
		manager.AddContact(contact)
	}

	// Test autocompletion by email prefix
	matches := manager.GetAutoCompleteMatches("john", "test-account", 10)
	if len(matches) != 2 { // john@example.com and johnny@music.com
		t.Errorf("Expected 2 matches for 'john', got %d", len(matches))
	}

	// Test autocompletion by name prefix
	matches = manager.GetAutoCompleteMatches("Jane", "test-account", 10)
	if len(matches) != 1 {
		t.Errorf("Expected 1 match for 'Jane', got %d", len(matches))
	}

	// Test with max results limit
	matches = manager.GetAutoCompleteMatches("j", "test-account", 2)
	if len(matches) != 2 {
		t.Errorf("Expected 2 matches with limit, got %d", len(matches))
	}

	// Test empty query
	matches = manager.GetAutoCompleteMatches("", "test-account", 10)
	if len(matches) != 0 {
		t.Errorf("Expected 0 matches for empty query, got %d", len(matches))
	}
}

func TestManagerImportExport(t *testing.T) {
	// Create test app
	app := test.NewApp()
	defer app.Quit()

	// Create config manager
	configMgr := config.NewPreferencesConfig(app, "test")

	// Create manager
	manager := NewManager(configMgr, "test")

	// Add test contacts
	contacts := []*Contact{
		NewContact("John Doe", "john@example.com", "test-account"),
		NewContact("Jane Smith", "jane@example.com", "test-account"),
	}

	for _, contact := range contacts {
		manager.AddContact(contact)
	}

	// Export contacts
	data, err := manager.ExportContacts("test-account")
	if err != nil {
		t.Errorf("Failed to export contacts: %v", err)
	}

	// Clear contacts
	manager.ClearContacts("test-account")
	if manager.GetContactCount("test-account") != 0 {
		t.Error("Expected 0 contacts after clear")
	}

	// Import contacts
	imported, err := manager.ImportContacts(data, "test-account", false)
	if err != nil {
		t.Errorf("Failed to import contacts: %v", err)
	}

	if imported != 2 {
		t.Errorf("Expected 2 imported contacts, got %d", imported)
	}

	if manager.GetContactCount("test-account") != 2 {
		t.Errorf("Expected 2 contacts after import, got %d", manager.GetContactCount("test-account"))
	}

	// Verify imported contacts
	contact, exists := manager.GetContactByEmail("john@example.com", "test-account")
	if !exists {
		t.Error("Imported contact not found")
	}
	if contact.Name != "John Doe" {
		t.Errorf("Expected name 'John Doe', got '%s'", contact.Name)
	}
}

func TestManagerPersistence(t *testing.T) {
	// Create test app
	app := test.NewApp()
	defer app.Quit()

	// Create config manager
	configMgr := config.NewPreferencesConfig(app, "test-persistence")

	// Create first manager instance
	manager1 := NewManager(configMgr, "test-persistence")

	// Add contact
	contact := NewContact("Test User", "test@example.com", "test-account")
	err := manager1.AddContact(contact)
	if err != nil {
		t.Errorf("Failed to add contact: %v", err)
	}

	// Create second manager instance (should load from preferences)
	manager2 := NewManager(configMgr, "test-persistence")

	// Verify contact was loaded
	retrieved, exists := manager2.GetContactByEmail("test@example.com", "test-account")
	if !exists {
		t.Error("Contact not found in second manager instance")
	}
	if retrieved.Name != "Test User" {
		t.Errorf("Expected name 'Test User', got '%s'", retrieved.Name)
	}
}

func TestManagerConfiguration(t *testing.T) {
	// Create test app
	app := test.NewApp()
	defer app.Quit()

	// Create config manager
	configMgr := config.NewPreferencesConfig(app, "test")

	// Create manager
	manager := NewManager(configMgr, "test")

	// Test default auto-collect setting
	if !manager.IsAutoCollectEnabled() {
		t.Error("Expected auto-collect to be enabled by default")
	}

	// Test disabling auto-collect
	err := manager.SetAutoCollectEnabled(false)
	if err != nil {
		t.Errorf("Failed to set auto-collect: %v", err)
	}

	if manager.IsAutoCollectEnabled() {
		t.Error("Expected auto-collect to be disabled")
	}

	// Test enabling auto-collect
	err = manager.SetAutoCollectEnabled(true)
	if err != nil {
		t.Errorf("Failed to set auto-collect: %v", err)
	}

	if !manager.IsAutoCollectEnabled() {
		t.Error("Expected auto-collect to be enabled")
	}
}

func TestManagerCSVImportExport(t *testing.T) {
	// Create test app
	app := test.NewApp()
	defer app.Quit()

	// Create config manager
	configMgr := config.NewPreferencesConfig(app, "test")

	// Create manager
	manager := NewManager(configMgr, "test")

	// Test CSV export with empty contacts
	var buf bytes.Buffer
	err := manager.ExportContactsToCSV(&buf, "test-account")
	if err != nil {
		t.Errorf("Failed to export empty CSV: %v", err)
	}

	// Should have header only
	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 1 {
		t.Errorf("Expected 1 line (header only), got %d", len(lines))
	}

	// Add some test contacts
	contact1 := NewContact("John Doe", "john@example.com", "test-account")
	contact1.Notes = "Test contact 1"
	contact2 := NewContact("Jane Smith", "jane@example.com", "test-account")
	contact2.Notes = "Test contact 2"

	err = manager.AddContact(contact1)
	if err != nil {
		t.Errorf("Failed to add contact1: %v", err)
	}

	err = manager.AddContact(contact2)
	if err != nil {
		t.Errorf("Failed to add contact2: %v", err)
	}

	// Test CSV export with contacts
	buf.Reset()
	err = manager.ExportContactsToCSV(&buf, "test-account")
	if err != nil {
		t.Errorf("Failed to export CSV: %v", err)
	}

	// Should have header + 2 contacts
	output = buf.String()
	lines = strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 3 {
		t.Errorf("Expected 3 lines (header + 2 contacts), got %d", len(lines))
	}

	// Test CSV import
	csvData := `Name,Email,Notes
Bob Wilson,bob@example.com,Imported contact
Alice Brown,alice@example.com,Another imported contact`

	reader := strings.NewReader(csvData)
	imported, err := manager.ImportContactsFromCSV(reader, "test-account", false)
	if err != nil {
		t.Errorf("Failed to import CSV: %v", err)
	}

	if imported != 2 {
		t.Errorf("Expected 2 imported contacts, got %d", imported)
	}

	// Verify imported contacts
	contacts := manager.ListContacts("test-account")
	if len(contacts) != 4 { // 2 original + 2 imported
		t.Errorf("Expected 4 total contacts, got %d", len(contacts))
	}

	// Find imported contact
	var bobContact *Contact
	for _, contact := range contacts {
		if contact.Email == "bob@example.com" {
			bobContact = contact
			break
		}
	}

	if bobContact == nil {
		t.Error("Imported contact 'bob@example.com' not found")
	} else {
		if bobContact.Name != "Bob Wilson" {
			t.Errorf("Expected name 'Bob Wilson', got '%s'", bobContact.Name)
		}
		if bobContact.Notes != "Imported contact" {
			t.Errorf("Expected notes 'Imported contact', got '%s'", bobContact.Notes)
		}
	}

	// Test import with replace existing
	csvDataReplace := `Name,Email,Notes
John Doe Updated,john@example.com,Updated notes`

	reader = strings.NewReader(csvDataReplace)
	imported, err = manager.ImportContactsFromCSV(reader, "test-account", true)
	if err != nil {
		t.Errorf("Failed to import CSV with replace: %v", err)
	}

	if imported != 1 {
		t.Errorf("Expected 1 updated contact, got %d", imported)
	}

	// Verify updated contact
	contacts = manager.ListContacts("test-account")
	var johnContact *Contact
	for _, contact := range contacts {
		if contact.Email == "john@example.com" {
			johnContact = contact
			break
		}
	}

	if johnContact == nil {
		t.Error("Updated contact 'john@example.com' not found")
	} else {
		if johnContact.Name != "John Doe Updated" {
			t.Errorf("Expected updated name 'John Doe Updated', got '%s'", johnContact.Name)
		}
		if johnContact.Notes != "Updated notes" {
			t.Errorf("Expected updated notes 'Updated notes', got '%s'", johnContact.Notes)
		}
	}
}
