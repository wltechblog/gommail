package addressbook

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/wltechblog/gommail/internal/logging"
)

// Contact represents a single contact entry
type Contact struct {
	ID          string    `json:"id"`           // Unique identifier
	Name        string    `json:"name"`         // Display name
	Email       string    `json:"email"`        // Email address (primary key for matching)
	Notes       string    `json:"notes"`        // Optional notes
	AccountName string    `json:"account_name"` // Associated account name
	CreatedAt   time.Time `json:"created_at"`   // Creation timestamp
	ModifiedAt  time.Time `json:"modified_at"`  // Last modification timestamp
	AutoAdded   bool      `json:"auto_added"`   // Whether this contact was automatically added
}

// NewContact creates a new contact with generated ID and timestamps
func NewContact(name, email, accountName string) *Contact {
	now := time.Now()
	return &Contact{
		ID:          generateContactID(email, accountName),
		Name:        strings.TrimSpace(name),
		Email:       strings.ToLower(strings.TrimSpace(email)),
		AccountName: accountName,
		CreatedAt:   now,
		ModifiedAt:  now,
		AutoAdded:   false,
	}
}

// NewAutoContact creates a new contact that was automatically added
func NewAutoContact(name, email, accountName string) *Contact {
	contact := NewContact(name, email, accountName)
	contact.AutoAdded = true
	return contact
}

// Update updates the contact's modifiable fields and timestamp
func (c *Contact) Update(name, notes string) {
	c.Name = strings.TrimSpace(name)
	c.Notes = strings.TrimSpace(notes)
	c.ModifiedAt = time.Now()
}

// DisplayName returns the best display name for the contact
func (c *Contact) DisplayName() string {
	if c.Name != "" {
		return c.Name
	}
	return c.Email
}

// String returns a string representation suitable for display
func (c *Contact) String() string {
	if c.Name != "" && c.Name != c.Email {
		return fmt.Sprintf("%s <%s>", c.Name, c.Email)
	}
	return c.Email
}

// Matches checks if the contact matches a search query
func (c *Contact) Matches(query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}

	// Check name, email, and notes
	return strings.Contains(strings.ToLower(c.Name), query) ||
		strings.Contains(strings.ToLower(c.Email), query) ||
		strings.Contains(strings.ToLower(c.Notes), query)
}

// ContactList represents a list of contacts with search and management capabilities
type ContactList struct {
	contacts map[string]*Contact // Key is contact ID
	logger   *logging.Logger
}

// NewContactList creates a new contact list
func NewContactList() *ContactList {
	return &ContactList{
		contacts: make(map[string]*Contact),
		logger:   logging.NewComponent("addressbook"),
	}
}

// Add adds a contact to the list, replacing any existing contact with the same email/account
func (cl *ContactList) Add(contact *Contact) error {
	if contact == nil {
		return fmt.Errorf("contact cannot be nil")
	}

	if contact.Email == "" {
		return fmt.Errorf("contact email cannot be empty")
	}

	if contact.AccountName == "" {
		return fmt.Errorf("contact account name cannot be empty")
	}

	// Check for existing contact with same email and account
	existingID := generateContactID(contact.Email, contact.AccountName)
	if existing, exists := cl.contacts[existingID]; exists {
		// Update existing contact instead of creating duplicate
		existing.Name = contact.Name
		existing.Notes = contact.Notes
		existing.ModifiedAt = time.Now()
		cl.logger.Debug("Updated existing contact: %s (%s)", existing.Email, existing.AccountName)
		return nil
	}

	// Ensure ID is correct
	contact.ID = existingID
	cl.contacts[contact.ID] = contact
	cl.logger.Debug("Added new contact: %s (%s)", contact.Email, contact.AccountName)
	return nil
}

// Remove removes a contact by ID
func (cl *ContactList) Remove(contactID string) error {
	if _, exists := cl.contacts[contactID]; !exists {
		return fmt.Errorf("contact not found: %s", contactID)
	}

	delete(cl.contacts, contactID)
	cl.logger.Debug("Removed contact: %s", contactID)
	return nil
}

// Get retrieves a contact by ID
func (cl *ContactList) Get(contactID string) (*Contact, bool) {
	contact, exists := cl.contacts[contactID]
	return contact, exists
}

// GetByEmail retrieves a contact by email and account name
func (cl *ContactList) GetByEmail(email, accountName string) (*Contact, bool) {
	contactID := generateContactID(email, accountName)
	return cl.Get(contactID)
}

// List returns all contacts, optionally filtered by account
func (cl *ContactList) List(accountName string) []*Contact {
	var result []*Contact
	for _, contact := range cl.contacts {
		if accountName == "" || contact.AccountName == accountName {
			result = append(result, contact)
		}
	}
	return result
}

// Search returns contacts matching the query, optionally filtered by account
func (cl *ContactList) Search(query, accountName string) []*Contact {
	var result []*Contact
	for _, contact := range cl.contacts {
		if (accountName == "" || contact.AccountName == accountName) && contact.Matches(query) {
			result = append(result, contact)
		}
	}
	return result
}

// Count returns the total number of contacts, optionally filtered by account
func (cl *ContactList) Count(accountName string) int {
	if accountName == "" {
		return len(cl.contacts)
	}

	count := 0
	for _, contact := range cl.contacts {
		if contact.AccountName == accountName {
			count++
		}
	}
	return count
}

// Clear removes all contacts, optionally filtered by account
func (cl *ContactList) Clear(accountName string) {
	if accountName == "" {
		cl.contacts = make(map[string]*Contact)
		cl.logger.Debug("Cleared all contacts")
		return
	}

	for id, contact := range cl.contacts {
		if contact.AccountName == accountName {
			delete(cl.contacts, id)
		}
	}
	cl.logger.Debug("Cleared contacts for account: %s", accountName)
}

// ToJSON serializes the contact list to JSON
func (cl *ContactList) ToJSON() ([]byte, error) {
	// Convert map to slice for JSON serialization
	contacts := make([]*Contact, 0, len(cl.contacts))
	for _, contact := range cl.contacts {
		contacts = append(contacts, contact)
	}

	return json.Marshal(contacts)
}

// FromJSON deserializes the contact list from JSON
func (cl *ContactList) FromJSON(data []byte) error {
	var contacts []*Contact
	if err := json.Unmarshal(data, &contacts); err != nil {
		return fmt.Errorf("failed to unmarshal contacts: %w", err)
	}

	// Clear existing contacts and rebuild map
	cl.contacts = make(map[string]*Contact)
	for _, contact := range contacts {
		if contact != nil && contact.Email != "" && contact.AccountName != "" {
			// Ensure ID is correct (for backward compatibility)
			contact.ID = generateContactID(contact.Email, contact.AccountName)
			cl.contacts[contact.ID] = contact
		}
	}

	cl.logger.Debug("Loaded %d contacts from JSON", len(cl.contacts))
	return nil
}

// generateContactID creates a unique ID for a contact based on email and account
func generateContactID(email, accountName string) string {
	return fmt.Sprintf("%s:%s", strings.ToLower(strings.TrimSpace(email)), accountName)
}
