package addressbook

import (
	"testing"
)

func TestNewContact(t *testing.T) {
	contact := NewContact("John Doe", "john@example.com", "test-account")

	if contact.Name != "John Doe" {
		t.Errorf("Expected name 'John Doe', got '%s'", contact.Name)
	}

	if contact.Email != "john@example.com" {
		t.Errorf("Expected email 'john@example.com', got '%s'", contact.Email)
	}

	if contact.AccountName != "test-account" {
		t.Errorf("Expected account 'test-account', got '%s'", contact.AccountName)
	}

	if contact.AutoAdded {
		t.Error("Expected AutoAdded to be false for manually created contact")
	}

	expectedID := "john@example.com:test-account"
	if contact.ID != expectedID {
		t.Errorf("Expected ID '%s', got '%s'", expectedID, contact.ID)
	}
}

func TestNewAutoContact(t *testing.T) {
	contact := NewAutoContact("Jane Smith", "jane@example.com", "test-account")

	if !contact.AutoAdded {
		t.Error("Expected AutoAdded to be true for auto-created contact")
	}
}

func TestContactDisplayName(t *testing.T) {
	// Test with name
	contact1 := NewContact("John Doe", "john@example.com", "test-account")
	if contact1.DisplayName() != "John Doe" {
		t.Errorf("Expected display name 'John Doe', got '%s'", contact1.DisplayName())
	}

	// Test without name
	contact2 := NewContact("", "jane@example.com", "test-account")
	if contact2.DisplayName() != "jane@example.com" {
		t.Errorf("Expected display name 'jane@example.com', got '%s'", contact2.DisplayName())
	}
}

func TestContactString(t *testing.T) {
	// Test with name different from email
	contact1 := NewContact("John Doe", "john@example.com", "test-account")
	expected1 := "John Doe <john@example.com>"
	if contact1.String() != expected1 {
		t.Errorf("Expected string '%s', got '%s'", expected1, contact1.String())
	}

	// Test without name
	contact2 := NewContact("", "jane@example.com", "test-account")
	expected2 := "jane@example.com"
	if contact2.String() != expected2 {
		t.Errorf("Expected string '%s', got '%s'", expected2, contact2.String())
	}

	// Test with name same as email
	contact3 := NewContact("test@example.com", "test@example.com", "test-account")
	expected3 := "test@example.com"
	if contact3.String() != expected3 {
		t.Errorf("Expected string '%s', got '%s'", expected3, contact3.String())
	}
}

func TestContactMatches(t *testing.T) {
	contact := NewContact("John Doe", "john@example.com", "test-account")
	contact.Notes = "Important client"

	testCases := []struct {
		query    string
		expected bool
	}{
		{"john", true},
		{"JOHN", true},
		{"doe", true},
		{"example", true},
		{"important", true},
		{"client", true},
		{"xyz", false},
		{"", true}, // Empty query matches all
	}

	for _, tc := range testCases {
		result := contact.Matches(tc.query)
		if result != tc.expected {
			t.Errorf("Query '%s': expected %v, got %v", tc.query, tc.expected, result)
		}
	}
}

func TestContactListAdd(t *testing.T) {
	cl := NewContactList()

	contact1 := NewContact("John Doe", "john@example.com", "account1")
	err := cl.Add(contact1)
	if err != nil {
		t.Errorf("Failed to add contact: %v", err)
	}

	if cl.Count("") != 1 {
		t.Errorf("Expected 1 contact, got %d", cl.Count(""))
	}

	// Test adding duplicate (should update existing)
	contact2 := NewContact("John Smith", "john@example.com", "account1")
	err = cl.Add(contact2)
	if err != nil {
		t.Errorf("Failed to add duplicate contact: %v", err)
	}

	if cl.Count("") != 1 {
		t.Errorf("Expected 1 contact after duplicate, got %d", cl.Count(""))
	}

	// Verify the name was updated
	retrieved, exists := cl.GetByEmail("john@example.com", "account1")
	if !exists {
		t.Error("Contact not found after update")
	}
	if retrieved.Name != "John Smith" {
		t.Errorf("Expected updated name 'John Smith', got '%s'", retrieved.Name)
	}
}

func TestContactListValidation(t *testing.T) {
	cl := NewContactList()

	// Test nil contact
	err := cl.Add(nil)
	if err == nil {
		t.Error("Expected error for nil contact")
	}

	// Test empty email
	contact := &Contact{Name: "Test", AccountName: "account1"}
	err = cl.Add(contact)
	if err == nil {
		t.Error("Expected error for empty email")
	}

	// Test empty account name
	contact = &Contact{Name: "Test", Email: "test@example.com"}
	err = cl.Add(contact)
	if err == nil {
		t.Error("Expected error for empty account name")
	}
}

func TestContactListSearch(t *testing.T) {
	cl := NewContactList()

	contacts := []*Contact{
		NewContact("John Doe", "john@example.com", "account1"),
		NewContact("Jane Smith", "jane@example.com", "account1"),
		NewContact("Bob Johnson", "bob@test.com", "account2"),
	}

	for _, contact := range contacts {
		cl.Add(contact)
	}

	// Test search by name
	results := cl.Search("john", "")
	if len(results) != 2 { // John Doe and Bob Johnson
		t.Errorf("Expected 2 results for 'john', got %d", len(results))
	}

	// Test search by email domain
	results = cl.Search("example.com", "")
	if len(results) != 2 {
		t.Errorf("Expected 2 results for 'example.com', got %d", len(results))
	}

	// Test search with account filter
	results = cl.Search("", "account1")
	if len(results) != 2 {
		t.Errorf("Expected 2 results for account1, got %d", len(results))
	}

	// Test search with both query and account filter
	results = cl.Search("jane", "account1")
	if len(results) != 1 {
		t.Errorf("Expected 1 result for 'jane' in account1, got %d", len(results))
	}
}

func TestContactListJSONSerialization(t *testing.T) {
	cl := NewContactList()

	// Add test contacts
	contacts := []*Contact{
		NewContact("John Doe", "john@example.com", "account1"),
		NewContact("Jane Smith", "jane@example.com", "account2"),
	}

	for _, contact := range contacts {
		cl.Add(contact)
	}

	// Serialize to JSON
	data, err := cl.ToJSON()
	if err != nil {
		t.Errorf("Failed to serialize to JSON: %v", err)
	}

	// Create new list and deserialize
	cl2 := NewContactList()
	err = cl2.FromJSON(data)
	if err != nil {
		t.Errorf("Failed to deserialize from JSON: %v", err)
	}

	// Verify counts match
	if cl2.Count("") != cl.Count("") {
		t.Errorf("Expected %d contacts after deserialization, got %d", cl.Count(""), cl2.Count(""))
	}

	// Verify specific contacts exist
	contact, exists := cl2.GetByEmail("john@example.com", "account1")
	if !exists {
		t.Error("John Doe not found after deserialization")
	}
	if contact.Name != "John Doe" {
		t.Errorf("Expected name 'John Doe', got '%s'", contact.Name)
	}
}

func TestContactListClear(t *testing.T) {
	cl := NewContactList()

	// Add contacts for different accounts
	contacts := []*Contact{
		NewContact("John Doe", "john@example.com", "account1"),
		NewContact("Jane Smith", "jane@example.com", "account1"),
		NewContact("Bob Johnson", "bob@test.com", "account2"),
	}

	for _, contact := range contacts {
		cl.Add(contact)
	}

	// Clear specific account
	cl.Clear("account1")
	if cl.Count("account1") != 0 {
		t.Errorf("Expected 0 contacts for account1 after clear, got %d", cl.Count("account1"))
	}
	if cl.Count("account2") != 1 {
		t.Errorf("Expected 1 contact for account2 after clear, got %d", cl.Count("account2"))
	}

	// Clear all
	cl.Clear("")
	if cl.Count("") != 0 {
		t.Errorf("Expected 0 contacts after clear all, got %d", cl.Count(""))
	}
}

func TestGenerateContactID(t *testing.T) {
	testCases := []struct {
		email      string
		account    string
		expectedID string
	}{
		{"test@example.com", "account1", "test@example.com:account1"},
		{"TEST@EXAMPLE.COM", "account1", "test@example.com:account1"},   // Should be lowercase
		{" test@example.com ", "account1", "test@example.com:account1"}, // Should be trimmed
	}

	for _, tc := range testCases {
		result := generateContactID(tc.email, tc.account)
		if result != tc.expectedID {
			t.Errorf("generateContactID(%s, %s): expected '%s', got '%s'",
				tc.email, tc.account, tc.expectedID, result)
		}
	}
}
