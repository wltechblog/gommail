# Addressbook Package

The addressbook package provides contact management functionality for the email client, including storage, search, autocompletion, and import/export capabilities.

## Features

- **Per-account contact lists**: Contacts are organized by email account
- **Automatic contact collection**: Automatically add contacts when sending emails
- **Search and autocompletion**: Fast contact lookup and email address completion
- **Import/Export**: Support for CSV format import and export
- **Persistent storage**: Uses Fyne Preferences API for cross-platform storage
- **Profile support**: Multiple independent contact lists using profiles

## Core Components

### Contact

Represents a single contact entry with the following fields:

- `ID`: Unique identifier (generated from email + account name)
- `Name`: Display name
- `Email`: Email address (primary key for matching)
- `Notes`: Optional notes
- `AccountName`: Associated email account
- `CreatedAt`: Creation timestamp
- `ModifiedAt`: Last modification timestamp
- `AutoAdded`: Whether contact was automatically added

### ContactList

Manages a collection of contacts with operations:

- `Add(contact)`: Add a new contact
- `Remove(email, accountName)`: Remove a contact
- `GetByEmail(email, accountName)`: Find contact by email
- `List(accountName)`: List contacts (optionally filtered by account)
- `Search(query, accountName)`: Search contacts by name or email
- `Clear(accountName)`: Remove all contacts for an account

### Manager

High-level contact management with features:

- **CRUD Operations**: Add, update, delete, and list contacts
- **Auto-collection**: Automatically collect addresses from sent messages
- **Search**: Find contacts by partial name or email
- **Autocompletion**: Get matching contacts for address fields
- **Import/Export**: CSV and JSON format support
- **Persistence**: Automatic saving through ConfigManager
- **Configuration Management**: Integrated with application configuration system

## Usage Examples

### Basic Contact Management

```go
// Create config manager
configMgr := config.NewPreferencesConfig(app, "default")

// Create manager
manager := addressbook.NewManager(configMgr, "default")

// Add a contact
contact := addressbook.NewContact("John Doe", "john@example.com", "work-account")
contact.Notes = "Important client"
err := manager.AddContact(contact)

// Search contacts
results := manager.SearchContacts("john", "work-account", 10)

// Get autocompletion matches
matches := manager.GetAutoCompleteMatches("jo", "work-account", 5)
```

### Automatic Contact Collection

```go
// Enable auto-collection
manager.SetAutoCollectEnabled(true)

// Auto-collect from sent message (called automatically by compose window)
message := &email.Message{
    To: []email.Address{{Name: "Jane Smith", Email: "jane@example.com"}},
    // ... other message fields
}
err := manager.AutoCollectFromMessage(message, "work-account")
```

### Import/Export

```go
// Export contacts to CSV
var buf bytes.Buffer
err := manager.ExportContactsToCSV(&buf, "work-account")

// Import contacts from CSV
reader := strings.NewReader(csvData)
imported, err := manager.ImportContactsFromCSV(reader, "work-account", false)
```

## CSV Format

The CSV import/export format includes the following columns:

- `Name`: Contact display name
- `Email`: Email address (required)
- `Notes`: Optional notes
- `Created`: Creation timestamp (export only)
- `Updated`: Last update timestamp (export only)

Example CSV:
```csv
Name,Email,Notes
John Doe,john@example.com,Important client
Jane Smith,jane@example.com,Marketing contact
```

## Integration Points

### Compose Window

The addressbook integrates with the compose window to provide:

- **Autocompletion**: Address fields (To, CC, BCC) show contact suggestions
- **Auto-collection**: Automatically add recipients to contacts when sending

### Settings Window

Addressbook settings are available in the Settings → Addressbook tab:

- **Auto-collection toggle**: Enable/disable automatic contact collection
- **Import/Export buttons**: Manage contact data
- **Addressbook dialog access**: Open the main contact management dialog

### Main Window Menu

Addressbook access through the Tools menu:

- **Address Book** (Ctrl+Shift+B): Open the addressbook dialog
- Keyboard shortcut for quick access

## Storage

Contacts are stored using the ConfigManager system:

**Configuration Settings:**
- Auto-collection setting is stored in the standard configuration through `AddressbookConfig`
- Managed through `ConfigManager.GetAddressbook()` and `ConfigManager.SetAddressbook()`

**Contact Data:**
- Contact data is stored using the underlying Fyne Preferences API with keys:
  - `addressbook.contacts`: JSON-serialized contact data
  - For non-default profiles: `addressbook.{profile}.contacts`

**Integration:**
- Uses ConfigManager interface for configuration management
- Supports both PreferencesConfig and YAML-based configurations
- Automatic migration from YAML to Preferences when needed

## Testing

Comprehensive tests are provided in `*_test.go` files:

- **Unit tests**: All core functionality
- **Integration tests**: Manager operations with persistence
- **CSV tests**: Import/export functionality

Run tests with:
```bash
go test ./internal/addressbook -v
```

## Architecture Notes

The addressbook follows the KISS (Keep It Simple, Stupid) principle:

- **Simple data structures**: Plain structs with JSON serialization
- **Minimal dependencies**: Uses only Fyne and standard library
- **Clear separation**: Contact data, business logic, and UI are separate
- **Profile isolation**: Each profile has independent contact storage

## Future Enhancements

Potential improvements for future versions:

- **vCard support**: Import/export in vCard format
- **Contact groups**: Organize contacts into groups
- **Contact photos**: Support for contact images
- **Sync integration**: Sync with external contact services
- **Advanced search**: Search by custom fields and metadata
