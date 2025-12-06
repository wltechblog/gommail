package cache

import "fmt"

// Key types for different cache categories
const (
	// IMAP-related cache keys
	KeyTypeMessages          = "messages"
	KeyTypeFolders           = "folders"
	KeyTypeFoldersSubscribed = "folders_subscribed"
	KeyTypeTrackingState     = "tracking_state"

	// Attachment-related cache keys
	KeyTypeAttachment     = "attachment"
	KeyTypeAttachmentMeta = "attachment_meta"

	// Addressbook-related cache keys
	KeyTypeAddressbook = "addressbook"
)

// GenerateKey creates a cache key with the given components
// Format: component1:component2:component3:...
// This provides a consistent key format across the entire application
func GenerateKey(components ...string) string {
	if len(components) == 0 {
		return ""
	}
	if len(components) == 1 {
		return components[0]
	}

	// Calculate total length needed
	totalLen := len(components) - 1 // for colons
	for _, c := range components {
		totalLen += len(c)
	}

	// Build key efficiently
	result := make([]byte, 0, totalLen)
	for i, c := range components {
		if i > 0 {
			result = append(result, ':')
		}
		result = append(result, c...)
	}
	return string(result)
}

// GenerateAccountKey creates a cache key for account-specific data
// Format: accountKey:keyType:identifier
func GenerateAccountKey(accountKey, keyType, identifier string) string {
	return GenerateKey(accountKey, keyType, identifier)
}

// GenerateMessagesKey creates a cache key for messages in a folder
// Format: accountKey:messages:folderName
func GenerateMessagesKey(accountKey, folderName string) string {
	return GenerateKey(accountKey, KeyTypeMessages, folderName)
}

// GenerateFoldersKey creates a cache key for folder list
// Format: accountKey:folders:list
func GenerateFoldersKey(accountKey string) string {
	return GenerateKey(accountKey, KeyTypeFolders, "list")
}

// GenerateSubscribedFoldersKey creates a cache key for subscribed folders
// Format: accountKey:folders:subscribed
func GenerateSubscribedFoldersKey(accountKey string) string {
	return GenerateKey(accountKey, KeyTypeFoldersSubscribed)
}

// GenerateTrackingStateKey creates a cache key for folder tracking state
// Format: accountKey:tracking_state:identifier
func GenerateTrackingStateKey(accountKey, identifier string) string {
	return GenerateKey(accountKey, KeyTypeTrackingState, identifier)
}

// GenerateAttachmentKey creates a cache key for attachment data
// Format: attachment:attachmentID
func GenerateAttachmentKey(attachmentID string) string {
	return fmt.Sprintf("%s:%s", KeyTypeAttachment, attachmentID)
}

// GenerateAttachmentMetaKey creates a cache key for attachment metadata
// Format: attachment_meta:attachmentID
func GenerateAttachmentMetaKey(attachmentID string) string {
	return fmt.Sprintf("%s:%s", KeyTypeAttachmentMeta, attachmentID)
}

// GenerateAddressbookKey creates a cache key for addressbook data
// Format: addressbook:accountKey
func GenerateAddressbookKey(accountKey string) string {
	return GenerateKey(KeyTypeAddressbook, accountKey)
}

// ParseKey splits a cache key into its components
// This is useful for debugging and logging
func ParseKey(key string) []string {
	if key == "" {
		return nil
	}

	// Count colons to pre-allocate slice
	colonCount := 0
	for i := 0; i < len(key); i++ {
		if key[i] == ':' {
			colonCount++
		}
	}

	components := make([]string, 0, colonCount+1)
	start := 0
	for i := 0; i < len(key); i++ {
		if key[i] == ':' {
			components = append(components, key[start:i])
			start = i + 1
		}
	}
	components = append(components, key[start:])
	return components
}

