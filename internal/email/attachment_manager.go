package email

import (
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wltechblog/gommail/pkg/cache"
)

// AttachmentManager handles attachment operations including caching, extraction, and viewing
type AttachmentManager struct {
	cache          *cache.Cache
	downloadDir    string
	maxCacheSize   int64
	allowedTypes   map[string]bool
	previewEnabled bool
}

// AttachmentInfo contains metadata about an attachment
type AttachmentInfo struct {
	ID          string    `json:"id"`
	MessageID   string    `json:"message_id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	CachedAt    time.Time `json:"cached_at"`
	CachePath   string    `json:"cache_path"`
	IsViewable  bool      `json:"is_viewable"`
}

// ViewableAttachment represents an attachment that can be previewed
type ViewableAttachment struct {
	Info     AttachmentInfo `json:"info"`
	Data     []byte         `json:"data"`
	Preview  []byte         `json:"preview,omitempty"` // Thumbnail or preview data
	MimeType string         `json:"mime_type"`
}

// NewAttachmentManager creates a new attachment manager
func NewAttachmentManager(cacheDir, downloadDir string) *AttachmentManager {
	// Create cache for attachments
	attachmentCache := cache.New(filepath.Join(cacheDir, "attachments"), true, 100) // 100MB cache

	// Define viewable/previewable file types
	allowedTypes := map[string]bool{
		"text/plain":       true,
		"text/html":        true,
		"text/css":         true,
		"text/javascript":  true,
		"application/json": true,
		"application/xml":  true,
		"image/jpeg":       true,
		"image/png":        true,
		"image/gif":        true,
		"image/webp":       true,
		"image/svg+xml":    true,
		"application/pdf":  true,
	}

	return &AttachmentManager{
		cache:          attachmentCache,
		downloadDir:    downloadDir,
		maxCacheSize:   50 * 1024 * 1024, // 50MB max per attachment
		allowedTypes:   allowedTypes,
		previewEnabled: true,
	}
}

// CacheAttachment stores an attachment in the cache for quick access
func (am *AttachmentManager) CacheAttachment(messageID string, attachment Attachment) (*AttachmentInfo, error) {
	// Generate unique ID for the attachment
	attachmentID := am.generateAttachmentID(messageID, attachment.Filename)

	// Check if attachment is too large for caching
	if attachment.Size > am.maxCacheSize {
		return nil, fmt.Errorf("attachment too large for caching: %d bytes (max: %d)", attachment.Size, am.maxCacheSize)
	}

	// Create attachment info
	info := AttachmentInfo{
		ID:          attachmentID,
		MessageID:   messageID,
		Filename:    attachment.Filename,
		ContentType: attachment.ContentType,
		Size:        attachment.Size,
		CachedAt:    time.Now(),
		IsViewable:  am.isViewableType(attachment.ContentType),
	}

	// Store attachment data in cache
	cacheKey := cache.GenerateAttachmentKey(attachmentID)
	err := am.cache.Set(cacheKey, attachment.Data, 24*time.Hour) // Cache for 24 hours
	if err != nil {
		return nil, fmt.Errorf("failed to cache attachment: %w", err)
	}

	// Store attachment metadata
	metaKey := cache.GenerateAttachmentMetaKey(attachmentID)
	metaData := fmt.Sprintf("%s|%s|%s|%d|%t", info.MessageID, info.Filename, info.ContentType, info.Size, info.IsViewable)
	err = am.cache.Set(metaKey, []byte(metaData), 24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("failed to cache attachment metadata: %w", err)
	}

	return &info, nil
}

// GetCachedAttachment retrieves an attachment from cache
func (am *AttachmentManager) GetCachedAttachment(attachmentID string) (*ViewableAttachment, error) {
	// Get attachment data from cache
	cacheKey := cache.GenerateAttachmentKey(attachmentID)
	data, found, err := am.cache.Get(cacheKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get cached attachment: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("attachment not found in cache: %s", attachmentID)
	}

	// Get attachment metadata
	metaKey := cache.GenerateAttachmentMetaKey(attachmentID)
	metaData, found, err := am.cache.Get(metaKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get attachment metadata: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("attachment metadata not found: %s", attachmentID)
	}

	// Parse metadata
	metaParts := strings.Split(string(metaData), "|")
	if len(metaParts) != 5 {
		return nil, fmt.Errorf("invalid attachment metadata format")
	}

	info := AttachmentInfo{
		ID:          attachmentID,
		MessageID:   metaParts[0],
		Filename:    metaParts[1],
		ContentType: metaParts[2],
		IsViewable:  metaParts[4] == "true",
	}

	viewable := &ViewableAttachment{
		Info:     info,
		Data:     data,
		MimeType: info.ContentType,
	}

	return viewable, nil
}

// ExtractAttachment saves an attachment to the specified directory
func (am *AttachmentManager) ExtractAttachment(attachmentID, targetDir string) (string, error) {
	// Get attachment from cache
	viewable, err := am.GetCachedAttachment(attachmentID)
	if err != nil {
		return "", fmt.Errorf("failed to get attachment for extraction: %w", err)
	}

	// Ensure target directory exists
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create target directory: %w", err)
	}

	// Generate safe filename
	safeFilename := SanitizeFilename(viewable.Info.Filename)
	targetPath := filepath.Join(targetDir, safeFilename)

	// Handle filename conflicts
	targetPath = am.resolveFilenameConflict(targetPath)

	// Write attachment data to file
	err = os.WriteFile(targetPath, viewable.Data, 0644)
	if err != nil {
		return "", fmt.Errorf("failed to write attachment to file: %w", err)
	}

	return targetPath, nil
}

// DownloadAttachment downloads an attachment to the default download directory
func (am *AttachmentManager) DownloadAttachment(attachmentID string) (string, error) {
	return am.ExtractAttachment(attachmentID, am.downloadDir)
}

// GetAttachmentPreview generates a preview for viewable attachments
func (am *AttachmentManager) GetAttachmentPreview(attachmentID string, maxSize int) ([]byte, error) {
	if !am.previewEnabled {
		return nil, fmt.Errorf("preview generation is disabled")
	}

	viewable, err := am.GetCachedAttachment(attachmentID)
	if err != nil {
		return nil, err
	}

	if !viewable.Info.IsViewable {
		return nil, fmt.Errorf("attachment type not previewable: %s", viewable.Info.ContentType)
	}

	// For text files, return truncated content
	if strings.HasPrefix(viewable.Info.ContentType, "text/") {
		if len(viewable.Data) <= maxSize {
			return viewable.Data, nil
		}
		return viewable.Data[:maxSize], nil
	}

	// For images, return the image data (UI will handle resizing)
	if strings.HasPrefix(viewable.Info.ContentType, "image/") {
		return viewable.Data, nil
	}

	// For other types, return first chunk
	if len(viewable.Data) <= maxSize {
		return viewable.Data, nil
	}
	return viewable.Data[:maxSize], nil
}

// ListCachedAttachments returns a list of all cached attachments for a message
func (am *AttachmentManager) ListCachedAttachments(messageID string) ([]AttachmentInfo, error) {
	// This is a simplified implementation - in a real system, you'd maintain an index
	// For now, we'll return an empty list as this would require cache enumeration
	return []AttachmentInfo{}, nil
}

// ClearCache removes all cached attachments
func (am *AttachmentManager) ClearCache() error {
	if am.cache == nil {
		return nil
	}
	return am.cache.Clear()
}

// generateAttachmentID creates a unique ID for an attachment
func (am *AttachmentManager) generateAttachmentID(messageID, filename string) string {
	hash := md5.Sum([]byte(messageID + ":" + filename))
	return fmt.Sprintf("%x", hash)
}

// GenerateAttachmentID creates a unique ID for an attachment (public method)
func (am *AttachmentManager) GenerateAttachmentID(messageID, filename string) string {
	return am.generateAttachmentID(messageID, filename)
}

// isViewableType checks if a content type can be previewed
func (am *AttachmentManager) isViewableType(contentType string) bool {
	// Remove parameters from content type
	if idx := strings.Index(contentType, ";"); idx != -1 {
		contentType = contentType[:idx]
	}
	return am.allowedTypes[strings.ToLower(contentType)]
}

// SanitizeFilename removes dangerous characters from filenames
// This is a standalone function that can be used by both the attachment manager
// and the UI to ensure filenames are safe for the filesystem
func SanitizeFilename(filename string) string {
	// Remove path separators and other dangerous characters
	dangerous := []string{"/", "\\", "..", ":", "*", "?", "\"", "<", ">", "|"}
	safe := filename
	for _, char := range dangerous {
		safe = strings.ReplaceAll(safe, char, "_")
	}
	return safe
}

// resolveFilenameConflict handles filename conflicts by appending numbers
func (am *AttachmentManager) resolveFilenameConflict(targetPath string) string {
	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		return targetPath
	}

	dir := filepath.Dir(targetPath)
	filename := filepath.Base(targetPath)
	ext := filepath.Ext(filename)
	name := strings.TrimSuffix(filename, ext)

	for i := 1; i < 1000; i++ {
		newFilename := fmt.Sprintf("%s_%d%s", name, i, ext)
		newPath := filepath.Join(dir, newFilename)
		if _, err := os.Stat(newPath); os.IsNotExist(err) {
			return newPath
		}
	}

	// Fallback with timestamp
	timestamp := time.Now().Unix()
	newFilename := fmt.Sprintf("%s_%d%s", name, timestamp, ext)
	return filepath.Join(dir, newFilename)
}

// GetAttachmentInfo returns basic information about an attachment without loading data
func (am *AttachmentManager) GetAttachmentInfo(attachmentID string) (*AttachmentInfo, error) {
	metaKey := cache.GenerateAttachmentMetaKey(attachmentID)
	metaData, found, err := am.cache.Get(metaKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get attachment metadata: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("attachment metadata not found: %s", attachmentID)
	}

	// Parse metadata
	metaParts := strings.Split(string(metaData), "|")
	if len(metaParts) != 5 {
		return nil, fmt.Errorf("invalid attachment metadata format")
	}

	info := &AttachmentInfo{
		ID:          attachmentID,
		MessageID:   metaParts[0],
		Filename:    metaParts[1],
		ContentType: metaParts[2],
		IsViewable:  metaParts[4] == "true",
	}

	return info, nil
}

// IsAttachmentCached checks if an attachment is available in cache
func (am *AttachmentManager) IsAttachmentCached(attachmentID string) bool {
	cacheKey := cache.GenerateAttachmentKey(attachmentID)
	_, found, _ := am.cache.Get(cacheKey)
	return found
}

// GetAttachmentSize returns the size of a cached attachment
func (am *AttachmentManager) GetAttachmentSize(attachmentID string) (int64, error) {
	info, err := am.GetAttachmentInfo(attachmentID)
	if err != nil {
		return 0, err
	}
	return info.Size, nil
}

// ValidateAttachment checks if an attachment is safe to process
func (am *AttachmentManager) ValidateAttachment(attachment Attachment) error {
	// Check filename
	if attachment.Filename == "" {
		return fmt.Errorf("attachment filename is empty")
	}

	// Check for dangerous filenames using the shared function
	if err := ValidateAttachmentSafety(attachment.Filename); err != nil {
		return err
	}

	// Check size
	if attachment.Size > am.maxCacheSize {
		return fmt.Errorf("attachment too large: %d bytes (max: %d)", attachment.Size, am.maxCacheSize)
	}

	return nil
}

// ValidateAttachmentSafety checks if a filename is safe to open/execute
// This is a standalone function that can be used by both the attachment manager
// and the UI to prevent opening dangerous file types
func ValidateAttachmentSafety(filename string) error {
	if filename == "" {
		return fmt.Errorf("attachment filename is empty")
	}

	// List of dangerous executable file extensions
	// Windows executables
	dangerous := []string{
		".exe", ".bat", ".cmd", ".com", ".scr", ".pif", ".vbs", ".vbe",
		".js", ".jse", ".wsf", ".wsh", ".msi", ".msp", ".cpl", ".jar",
		".app", ".deb", ".rpm", ".dmg", ".pkg",
		// Unix/Linux executables and scripts
		".sh", ".bash", ".zsh", ".ksh", ".csh", ".tcsh", ".fish",
		".run", ".bin", ".out",
		// Script files that could be dangerous
		".py", ".pl", ".rb", ".php", ".ps1", ".psm1",
		// Other potentially dangerous formats
		".apk", ".ipa", ".appx", ".msix",
	}

	filename = strings.ToLower(filename)
	for _, ext := range dangerous {
		if strings.HasSuffix(filename, ext) {
			return fmt.Errorf("potentially dangerous attachment type: %s (executable or script files are blocked for security)", ext)
		}
	}

	return nil
}

// ProcessMessageAttachments processes all attachments in a message and caches them
func (am *AttachmentManager) ProcessMessageAttachments(msg *Message) ([]AttachmentInfo, error) {
	var attachmentInfos []AttachmentInfo

	for _, attachment := range msg.Attachments {
		// Validate attachment
		if err := am.ValidateAttachment(attachment); err != nil {
			// Log warning but continue with other attachments
			continue
		}

		// Cache attachment
		info, err := am.CacheAttachment(msg.ID, attachment)
		if err != nil {
			// Log error but continue with other attachments
			continue
		}

		attachmentInfos = append(attachmentInfos, *info)
	}

	return attachmentInfos, nil
}

// GetSupportedPreviewTypes returns a list of content types that can be previewed
func (am *AttachmentManager) GetSupportedPreviewTypes() []string {
	var types []string
	for contentType := range am.allowedTypes {
		types = append(types, contentType)
	}
	return types
}

// SetDownloadDirectory updates the default download directory
func (am *AttachmentManager) SetDownloadDirectory(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create download directory: %w", err)
	}
	am.downloadDir = dir
	return nil
}

// GetCacheStats returns statistics about the attachment cache
func (am *AttachmentManager) GetCacheStats() (map[string]interface{}, error) {
	stats := map[string]interface{}{
		"max_cache_size":  am.maxCacheSize,
		"download_dir":    am.downloadDir,
		"preview_enabled": am.previewEnabled,
		"supported_types": len(am.allowedTypes),
	}
	return stats, nil
}
