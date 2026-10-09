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

