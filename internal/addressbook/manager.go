package addressbook

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"github.com/wltechblog/gommail/internal/config"
	"github.com/wltechblog/gommail/internal/email"
	"github.com/wltechblog/gommail/internal/logging"
)

// Manager handles contact persistence and management using ConfigManager
type Manager struct {
	configMgr config.ConfigManager
	contacts  *ContactList
	logger    *logging.Logger
	profile   string

	// Configuration
	autoCollectEnabled bool
}

// NewManager creates a new contact manager
func NewManager(configMgr config.ConfigManager, profile string) *Manager {
	manager := &Manager{
		configMgr: configMgr,
		contacts:  NewContactList(),
		logger:    logging.NewComponent("addressbook"),
		profile:   profile,
	}

	// Load configuration
	manager.loadConfig()

	// Load existing contacts
	if err := manager.Load(); err != nil {
		manager.logger.Error("Failed to load contacts: %v", err)
	}

	return manager
}

// getPreferences returns the underlying Fyne preferences for direct access
func (m *Manager) getPreferences() fyne.Preferences {
	if prefsConfig, ok := m.configMgr.(*config.PreferencesConfig); ok {
		return prefsConfig.GetPreferences()
	}
	// Fallback - this shouldn't happen in normal usage
	return nil
}

// GetConfigManager returns the underlying ConfigManager for UI components
func (m *Manager) GetConfigManager() config.ConfigManager {
	return m.configMgr
}

// Load loads contacts from preferences
func (m *Manager) Load() error {
	m.logger.Debug("Loading contacts from preferences")

	prefs := m.getPreferences()
	if prefs == nil {
		m.logger.Error("Unable to access preferences - config manager is not PreferencesConfig")
		return fmt.Errorf("unable to access preferences")
	}

	// Get contacts data from preferences
	contactsJSON := prefs.StringWithFallback(m.getPrefsKey("contacts.data"), "[]")
	if contactsJSON == "[]" {
		m.logger.Debug("No contacts found in preferences")
		return nil
	}

	// Deserialize contacts
	if err := m.contacts.FromJSON([]byte(contactsJSON)); err != nil {
		return fmt.Errorf("failed to load contacts from preferences: %w", err)
	}

	m.logger.Info("Loaded %d contacts from preferences", m.contacts.Count(""))
	return nil
}

// Save saves contacts to preferences
func (m *Manager) Save() error {
	m.logger.Debug("Saving contacts to preferences")

	prefs := m.getPreferences()
	if prefs == nil {
		m.logger.Error("Unable to access preferences - config manager is not PreferencesConfig")
		return fmt.Errorf("unable to access preferences")
	}

	// Serialize contacts
	data, err := m.contacts.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to serialize contacts: %w", err)
	}

	// Save to preferences
	prefs.SetString(m.getPrefsKey("contacts.data"), string(data))
	prefs.SetInt(m.getPrefsKey("contacts.count"), m.contacts.Count(""))
	prefs.SetString(m.getPrefsKey("contacts.last_updated"), time.Now().Format(time.RFC3339))

	m.logger.Debug("Saved %d contacts to preferences", m.contacts.Count(""))
	return nil
}

// AddContact adds a new contact
func (m *Manager) AddContact(contact *Contact) error {
	if err := m.contacts.Add(contact); err != nil {
		return err
	}
	return m.Save()
}

// RemoveContact removes a contact by ID
func (m *Manager) RemoveContact(contactID string) error {
	if err := m.contacts.Remove(contactID); err != nil {
		return err
	}
	return m.Save()
}

// GetContact retrieves a contact by ID
func (m *Manager) GetContact(contactID string) (*Contact, bool) {
	return m.contacts.Get(contactID)
}

// GetContactByEmail retrieves a contact by email and account
func (m *Manager) GetContactByEmail(email, accountName string) (*Contact, bool) {
	return m.contacts.GetByEmail(email, accountName)
}

// ListContacts returns all contacts, optionally filtered by account
func (m *Manager) ListContacts(accountName string) []*Contact {
	return m.contacts.List(accountName)
}

// SearchContacts searches for contacts matching the query
func (m *Manager) SearchContacts(query, accountName string) []*Contact {
	return m.contacts.Search(query, accountName)
}

// GetContactCount returns the number of contacts, optionally filtered by account
func (m *Manager) GetContactCount(accountName string) int {
	return m.contacts.Count(accountName)
}

// ClearContacts removes all contacts for an account
func (m *Manager) ClearContacts(accountName string) error {
	m.contacts.Clear(accountName)
	return m.Save()
}

// AutoCollectFromMessage automatically adds contacts from a message if enabled
func (m *Manager) AutoCollectFromMessage(message *email.Message, accountName string) error {
	if !m.autoCollectEnabled {
		return nil // Auto-collection disabled
	}

	if message == nil || accountName == "" {
		return nil
	}

	var collected int

	// Collect from To addresses
	for _, addr := range message.To {
		if m.shouldCollectAddress(addr, accountName) {
			contact := NewAutoContact(addr.Name, addr.Email, accountName)
			if err := m.contacts.Add(contact); err == nil {
				collected++
			}
		}
	}

	// Collect from CC addresses
	for _, addr := range message.CC {
		if m.shouldCollectAddress(addr, accountName) {
			contact := NewAutoContact(addr.Name, addr.Email, accountName)
			if err := m.contacts.Add(contact); err == nil {
				collected++
			}
		}
	}

	// Collect from BCC addresses
	for _, addr := range message.BCC {
		if m.shouldCollectAddress(addr, accountName) {
			contact := NewAutoContact(addr.Name, addr.Email, accountName)
			if err := m.contacts.Add(contact); err == nil {
				collected++
			}
		}
	}

	if collected > 0 {
		m.logger.Debug("Auto-collected %d contacts from message", collected)
		return m.Save()
	}

	return nil
}

// GetAutoCompleteMatches returns contacts matching a partial email/name for autocompletion
func (m *Manager) GetAutoCompleteMatches(partial, accountName string, maxResults int) []*Contact {
	if strings.TrimSpace(partial) == "" {
		return nil
	}

	partial = strings.ToLower(strings.TrimSpace(partial))
	var matches []*Contact

	for _, contact := range m.contacts.List(accountName) {
		// Check if email or name starts with the partial string
		if strings.HasPrefix(strings.ToLower(contact.Email), partial) ||
			strings.HasPrefix(strings.ToLower(contact.Name), partial) {
			matches = append(matches, contact)
			if maxResults > 0 && len(matches) >= maxResults {
				break
			}
		}
	}

	return matches
}

// SetAutoCollectEnabled enables or disables automatic contact collection
func (m *Manager) SetAutoCollectEnabled(enabled bool) error {
	m.autoCollectEnabled = enabled

	// Update configuration through ConfigManager
	addressbookConfig := m.configMgr.GetAddressbook()
	addressbookConfig.AutoCollectEnabled = enabled
	m.configMgr.SetAddressbook(addressbookConfig)

	// Save configuration
	if err := m.configMgr.Save(); err != nil {
		m.logger.Error("Failed to save addressbook configuration: %v", err)
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	m.logger.Debug("Auto-collect enabled: %v", enabled)
	return nil
}

// IsAutoCollectEnabled returns whether automatic contact collection is enabled
func (m *Manager) IsAutoCollectEnabled() bool {
	return m.autoCollectEnabled
}

// ImportContacts imports contacts from JSON data
func (m *Manager) ImportContacts(data []byte, accountName string, replaceExisting bool) (int, error) {
	tempList := NewContactList()
	if err := tempList.FromJSON(data); err != nil {
		return 0, fmt.Errorf("failed to parse import data: %w", err)
	}

	imported := 0
	for _, contact := range tempList.List("") {
		// Update account name if specified
		if accountName != "" {
			contact.AccountName = accountName
			contact.ID = generateContactID(contact.Email, accountName)
		}

		// Check if contact already exists
		if existing, exists := m.contacts.GetByEmail(contact.Email, contact.AccountName); exists {
			if replaceExisting {
				existing.Update(contact.Name, contact.Notes)
				imported++
			}
		} else {
			if err := m.contacts.Add(contact); err == nil {
				imported++
			}
		}
	}

	if imported > 0 {
		if err := m.Save(); err != nil {
			return imported, fmt.Errorf("failed to save imported contacts: %w", err)
		}
	}

	m.logger.Info("Imported %d contacts", imported)
	return imported, nil
}

// ExportContacts exports contacts to JSON data
func (m *Manager) ExportContacts(accountName string) ([]byte, error) {
	contacts := m.contacts.List(accountName)
	return json.Marshal(contacts)
}

// loadConfig loads configuration from ConfigManager
func (m *Manager) loadConfig() {
	addressbookConfig := m.configMgr.GetAddressbook()
	m.autoCollectEnabled = addressbookConfig.AutoCollectEnabled
	m.logger.Debug("Loaded config: auto_collect=%v", m.autoCollectEnabled)
}

// shouldCollectAddress determines if an address should be automatically collected
func (m *Manager) shouldCollectAddress(addr email.Address, accountName string) bool {
	if addr.Email == "" {
		return false
	}

	// Don't collect our own addresses (this would need account info to check properly)
	// For now, just check if it already exists
	_, exists := m.contacts.GetByEmail(addr.Email, accountName)
	return !exists
}

// ImportContactsFromCSV imports contacts from CSV data
func (m *Manager) ImportContactsFromCSV(reader io.Reader, accountName string, replaceExisting bool) (int, error) {
	csvReader := csv.NewReader(reader)

	// Read header row
	headers, err := csvReader.Read()
	if err != nil {
		return 0, fmt.Errorf("failed to read CSV headers: %w", err)
	}

	// Find column indices
	nameCol, emailCol, notesCol := -1, -1, -1
	for i, header := range headers {
		switch strings.ToLower(strings.TrimSpace(header)) {
		case "name", "display name", "full name":
			nameCol = i
		case "email", "email address", "e-mail":
			emailCol = i
		case "notes", "note", "comments", "comment":
			notesCol = i
		}
	}

	if emailCol == -1 {
		return 0, fmt.Errorf("CSV must contain an 'email' column")
	}

	imported := 0
	for {
		record, err := csvReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			m.logger.Warn("Skipping invalid CSV row: %v", err)
			continue
		}

		if len(record) <= emailCol {
			continue
		}

		email := strings.TrimSpace(record[emailCol])
		if email == "" {
			continue
		}

		name := ""
		if nameCol >= 0 && nameCol < len(record) {
			name = strings.TrimSpace(record[nameCol])
		}
		if name == "" {
			name = email // Use email as name if no name provided
		}

		notes := ""
		if notesCol >= 0 && notesCol < len(record) {
			notes = strings.TrimSpace(record[notesCol])
		}

		contact := NewContact(name, email, accountName)
		contact.Notes = notes

		// Check if contact already exists
		if existing, exists := m.contacts.GetByEmail(email, accountName); exists {
			if replaceExisting {
				existing.Update(name, notes)
				imported++
			}
		} else {
			if err := m.contacts.Add(contact); err == nil {
				imported++
			}
		}
	}

	if imported > 0 {
		if err := m.Save(); err != nil {
			return imported, fmt.Errorf("failed to save imported contacts: %w", err)
		}
	}

	m.logger.Info("Imported %d contacts from CSV", imported)
	return imported, nil
}

// ExportContactsToCSV exports contacts to CSV format
func (m *Manager) ExportContactsToCSV(writer io.Writer, accountName string) error {
	contacts := m.contacts.List(accountName)

	csvWriter := csv.NewWriter(writer)
	defer csvWriter.Flush()

	// Write header
	if err := csvWriter.Write([]string{"Name", "Email", "Notes", "Created", "Updated"}); err != nil {
		return fmt.Errorf("failed to write CSV header: %w", err)
	}

	// Write contacts
	for _, contact := range contacts {
		record := []string{
			contact.Name,
			contact.Email,
			contact.Notes,
			contact.CreatedAt.Format("2006-01-02 15:04:05"),
			contact.ModifiedAt.Format("2006-01-02 15:04:05"),
		}

		if err := csvWriter.Write(record); err != nil {
			return fmt.Errorf("failed to write contact record: %w", err)
		}
	}

	m.logger.Info("Exported %d contacts to CSV", len(contacts))
	return nil
}

// getPrefsKey returns a preference key with profile prefix
func (m *Manager) getPrefsKey(key string) string {
	if m.profile == "default" {
		return fmt.Sprintf("addressbook.%s", key)
	}
	return fmt.Sprintf("addressbook.%s.%s", m.profile, key)
}
