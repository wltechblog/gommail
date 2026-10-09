package imap

// Code in this file was mechanically split from worker.go.

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/wltechblog/gommail/internal/email"
	"github.com/wltechblog/gommail/pkg/cache"
)

// getCacheKey generates a cache key for the given type and identifier
// Deprecated: Use cache.GenerateAccountKey instead
func (w *IMAPWorker) getCacheKey(keyType, identifier string) string {
	if w.cacheKey == "" {
		return cache.GenerateKey(keyType, identifier)
	}
	return cache.GenerateAccountKey(w.cacheKey, keyType, identifier)
}

// getCachedMessages retrieves messages from cache
func (w *IMAPWorker) getCachedMessages(folder string) ([]email.Message, bool, error) {
	if w.cache == nil {
		return nil, false, nil // No cache available
	}

	key := w.getCacheKey("messages", folder)
	data, found, err := w.cache.Get(key)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get cached messages: %w", err)
	}

	if !found {
		return nil, false, nil
	}

	var messages []email.Message
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, false, fmt.Errorf("failed to unmarshal cached messages: %w", err)
	}

	return messages, true, nil
}

// cacheMessages stores messages in cache
func (w *IMAPWorker) cacheMessages(folder string, messages []email.Message) error {
	if w.cache == nil {
		return nil // No cache available
	}

	data, err := json.Marshal(messages)
	if err != nil {
		return fmt.Errorf("failed to marshal messages: %w", err)
	}

	key := w.getCacheKey("messages", folder)
	return w.cache.Set(key, data, 24*time.Hour) // Cache messages for 24 hours
}

// getCachedFolders retrieves folders from cache
func (w *IMAPWorker) getCachedFolders() ([]email.Folder, bool, error) {
	if w.cache == nil {
		return nil, false, nil // No cache available
	}

	key := w.getCacheKey("folders", "list")
	data, found, err := w.cache.Get(key)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get cached folders: %w", err)
	}

	if !found {
		return nil, false, nil
	}

	var folders []email.Folder
	if err := json.Unmarshal(data, &folders); err != nil {
		return nil, false, fmt.Errorf("failed to unmarshal cached folders: %w", err)
	}

	return folders, true, nil
}

// cacheFolders stores folders in cache
func (w *IMAPWorker) cacheFolders(folders []email.Folder) error {
	if w.cache == nil {
		return nil // No cache available
	}

	data, err := json.Marshal(folders)
	if err != nil {
		return fmt.Errorf("failed to marshal folders: %w", err)
	}

	key := w.getCacheKey("folders", "list")
	return w.cache.Set(key, data, 24*time.Hour) // Cache folders for 24 hours
}

// getCachedSubscribedFolders retrieves subscribed folders from cache
func (w *IMAPWorker) getCachedSubscribedFolders() ([]email.Folder, bool, error) {
	if w.cache == nil {
		return nil, false, nil // No cache available
	}

	key := w.getCacheKey("folders", "subscribed")
	data, found, err := w.cache.Get(key)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get cached subscribed folders: %w", err)
	}

	if !found {
		return nil, false, nil
	}

	var folders []email.Folder
	if err := json.Unmarshal(data, &folders); err != nil {
		return nil, false, fmt.Errorf("failed to unmarshal cached subscribed folders: %w", err)
	}

	return folders, true, nil
}

// cacheSubscribedFolders stores subscribed folders in cache
func (w *IMAPWorker) cacheSubscribedFolders(folders []email.Folder) error {
	if w.cache == nil {
		return nil // No cache available
	}

	data, err := json.Marshal(folders)
	if err != nil {
		return fmt.Errorf("failed to marshal subscribed folders: %w", err)
	}

	key := w.getCacheKey("folders", "subscribed")
	return w.cache.Set(key, data, 24*time.Hour) // Cache subscribed folders for 24 hours
}
