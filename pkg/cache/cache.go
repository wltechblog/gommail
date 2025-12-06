package cache

import (
	"bytes"
	"compress/zlib"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wltechblog/gommail/internal/logging"
)

// Cache represents a filesystem-based cache with optional compression
type Cache struct {
	directory   string
	compression bool
	maxSizeMB   int
	logger      *logging.Logger
}

// CacheEntry represents a cached item with metadata
type CacheEntry struct {
	Key       string    `json:"key"`
	Data      []byte    `json:"data"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	Size      int64     `json:"size"`
}

// New creates a new cache instance
func New(directory string, compression bool, maxSizeMB int) *Cache {
	logger := logging.NewComponent("cache")
	logger.Debug("Creating new cache instance: directory=%s, compression=%v, maxSizeMB=%d",
		directory, compression, maxSizeMB)

	return &Cache{
		directory:   directory,
		compression: compression,
		maxSizeMB:   maxSizeMB,
		logger:      logger,
	}
}

// Set stores data in the cache with an optional expiration time
func (c *Cache) Set(key string, data []byte, expiration time.Duration) error {
	c.logger.Debug("Setting cache entry: key=%s, size=%d bytes, expiration=%v",
		key, len(data), expiration)

	// Ensure cache directory exists
	if err := os.MkdirAll(c.directory, 0755); err != nil {
		c.logger.Error("Failed to create cache directory %s: %v", c.directory, err)
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	entry := CacheEntry{
		Key:       key,
		Data:      data,
		CreatedAt: time.Now(),
		Size:      int64(len(data)),
	}

	if expiration > 0 {
		entry.ExpiresAt = time.Now().Add(expiration)
		c.logger.Debug("Cache entry will expire at: %v", entry.ExpiresAt)
	}

	originalSize := len(data)

	// Compress data if enabled
	if c.compression {
		c.logger.Debug("Compressing cache data (%d bytes)", originalSize)
		compressed, err := c.compress(data)
		if err != nil {
			c.logger.Error("Failed to compress cache data for key %s: %v", key, err)
			return fmt.Errorf("failed to compress data: %w", err)
		}
		entry.Data = compressed
		compressionRatio := float64(len(compressed)) / float64(originalSize) * 100
		c.logger.Debug("Compression complete: %d -> %d bytes (%.1f%%)",
			originalSize, len(compressed), compressionRatio)
	}

	// Serialize entry
	entryData, err := json.Marshal(entry)
	if err != nil {
		c.logger.Error("Failed to marshal cache entry for key %s: %v", key, err)
		return fmt.Errorf("failed to marshal cache entry: %w", err)
	}

	// Write to file
	filename := c.getFilename(key)
	filepath := filepath.Join(c.directory, filename)

	err = os.WriteFile(filepath, entryData, 0644)
	if err != nil {
		c.logger.Error("Failed to write cache file %s: %v", filepath, err)
		return err
	}

	c.logger.Debug("Cache entry stored successfully: key=%s, file=%s, size=%d bytes",
		key, filename, len(entryData))
	return nil
}

// Get retrieves data from the cache
func (c *Cache) Get(key string) ([]byte, bool, error) {
	c.logger.Debug("Getting cache entry: key=%s", key)

	filename := c.getFilename(key)
	filepath := filepath.Join(c.directory, filename)

	// Read file
	entryData, err := os.ReadFile(filepath)
	if err != nil {
		if os.IsNotExist(err) {
			c.logger.Debug("Cache miss: key=%s (file not found)", key)
			return nil, false, nil
		}
		c.logger.Error("Failed to read cache file %s: %v", filepath, err)
		return nil, false, fmt.Errorf("failed to read cache file: %w", err)
	}

	c.logger.Debug("Cache file read: %s (%d bytes)", filename, len(entryData))

	// Deserialize entry
	var entry CacheEntry
	if err := json.Unmarshal(entryData, &entry); err != nil {
		c.logger.Error("Failed to unmarshal cache entry %s: %v", filename, err)
		return nil, false, fmt.Errorf("failed to unmarshal cache entry: %w", err)
	}

	// Check expiration
	if !entry.ExpiresAt.IsZero() && time.Now().After(entry.ExpiresAt) {
		// Entry expired, delete it
		c.logger.Debug("Cache entry expired: key=%s, expired_at=%v", key, entry.ExpiresAt)
		os.Remove(filepath)
		return nil, false, nil
	}

	// Decompress data if needed
	data := entry.Data
	if c.compression {
		c.logger.Debug("Decompressing cache data (%d bytes)", len(data))
		decompressed, err := c.decompress(data)
		if err != nil {
			c.logger.Error("Failed to decompress cache data for key %s: %v", key, err)
			return nil, false, fmt.Errorf("failed to decompress data: %w", err)
		}
		data = decompressed
		c.logger.Debug("Decompression complete: %d -> %d bytes", len(entry.Data), len(data))
	}

	c.logger.Debug("Cache hit: key=%s, size=%d bytes", key, len(data))
	return data, true, nil
}

// Delete removes an entry from the cache
func (c *Cache) Delete(key string) error {
	c.logger.Debug("Deleting cache entry: key=%s", key)

	filename := c.getFilename(key)
	filepath := filepath.Join(c.directory, filename)

	err := os.Remove(filepath)
	if os.IsNotExist(err) {
		c.logger.Debug("Cache entry already deleted: key=%s", key)
		return nil // Already deleted
	}

	if err != nil {
		c.logger.Error("Failed to delete cache entry %s: %v", key, err)
		return err
	}

	c.logger.Debug("Cache entry deleted successfully: key=%s", key)
	return nil
}

// Clear removes all entries from the cache
func (c *Cache) Clear() error {
	c.logger.Info("Clearing entire cache directory: %s", c.directory)

	err := os.RemoveAll(c.directory)
	if err != nil {
		c.logger.Error("Failed to clear cache directory %s: %v", c.directory, err)
		return err
	}

	c.logger.Info("Cache cleared successfully")
	return nil
}

// Size returns the total size of the cache in bytes
func (c *Cache) Size() (int64, error) {
	var totalSize int64

	err := filepath.Walk(c.directory, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			totalSize += info.Size()
		}
		return nil
	})

	return totalSize, err
}

// Cleanup removes expired entries and enforces size limits
func (c *Cache) Cleanup() error {
	// Remove expired entries
	err := filepath.Walk(c.directory, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}

		// Read and check expiration
		entryData, err := os.ReadFile(path)
		if err != nil {
			return nil // Skip problematic files
		}

		var entry CacheEntry
		if err := json.Unmarshal(entryData, &entry); err != nil {
			return nil // Skip invalid entries
		}

		if !entry.ExpiresAt.IsZero() && time.Now().After(entry.ExpiresAt) {
			os.Remove(path)
		}

		return nil
	})

	if err != nil {
		return err
	}

	// Check size limit and perform LRU eviction if needed
	if c.maxSizeMB > 0 {
		size, err := c.Size()
		if err != nil {
			return err
		}

		maxSize := int64(c.maxSizeMB) * 1024 * 1024
		if size > maxSize {
			c.logger.Debug("Cache size (%d bytes) exceeds limit (%d bytes) - performing LRU eviction", size, maxSize)
			err := c.performLRUEviction(maxSize)
			if err != nil {
				c.logger.Error("Failed to perform LRU eviction: %v", err)
				return err
			}
		}
	}

	return nil
}

// compress compresses data using zlib
func (c *Cache) compress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	writer := zlib.NewWriter(&buf)

	_, err := writer.Write(data)
	if err != nil {
		return nil, err
	}

	err = writer.Close()
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// decompress decompresses data using zlib
func (c *Cache) decompress(data []byte) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	return io.ReadAll(reader)
}

// ListKeys returns all cache keys that match the given prefix
func (c *Cache) ListKeys(prefix string) ([]string, error) {
	c.logger.Debug("Listing cache keys with prefix: %s", prefix)

	var keys []string

	err := filepath.Walk(c.directory, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}

		// Read and check if this cache entry matches the prefix
		entryData, err := os.ReadFile(path)
		if err != nil {
			return nil // Skip problematic files
		}

		var entry CacheEntry
		if err := json.Unmarshal(entryData, &entry); err != nil {
			return nil // Skip invalid entries
		}

		// Check if key matches prefix
		if prefix == "" || strings.HasPrefix(entry.Key, prefix) {
			keys = append(keys, entry.Key)
		}

		return nil
	})

	if err != nil {
		c.logger.Error("Failed to list cache keys: %v", err)
		return nil, err
	}

	c.logger.Debug("Found %d cache keys with prefix '%s'", len(keys), prefix)
	return keys, nil
}

// DeleteByPrefix removes all cache entries that match the given prefix
func (c *Cache) DeleteByPrefix(prefix string) error {
	c.logger.Debug("Deleting cache entries with prefix: %s", prefix)

	keys, err := c.ListKeys(prefix)
	if err != nil {
		return err
	}

	var deletedCount int
	var errors []error

	for _, key := range keys {
		if err := c.Delete(key); err != nil {
			c.logger.Error("Failed to delete cache entry %s: %v", key, err)
			errors = append(errors, err)
		} else {
			deletedCount++
		}
	}

	c.logger.Debug("Deleted %d cache entries with prefix '%s'", deletedCount, prefix)

	if len(errors) > 0 {
		return fmt.Errorf("failed to delete %d cache entries: %v", len(errors), errors)
	}

	return nil
}

// getFilename generates a filename for a cache key
func (c *Cache) getFilename(key string) string {
	hash := md5.Sum([]byte(key))
	return fmt.Sprintf("%x.cache", hash)
}

// CacheEntryInfo holds information about a cache entry for LRU eviction
type CacheEntryInfo struct {
	Key       string
	Size      int64
	CreatedAt time.Time
	FilePath  string
}

// performLRUEviction removes the least recently used cache entries until size is under limit
func (c *Cache) performLRUEviction(maxSize int64) error {
	c.logger.Debug("Starting LRU eviction to reduce cache size below %d bytes", maxSize)

	// Get all cache entries with their metadata
	var entries []CacheEntryInfo
	err := filepath.Walk(c.directory, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".cache") {
			return err
		}

		// Read cache entry to get metadata
		entryData, err := os.ReadFile(path)
		if err != nil {
			c.logger.Warn("Failed to read cache file %s during eviction: %v", path, err)
			return nil // Skip problematic files
		}

		var entry CacheEntry
		if err := json.Unmarshal(entryData, &entry); err != nil {
			c.logger.Warn("Failed to unmarshal cache entry %s during eviction: %v", path, err)
			return nil // Skip invalid entries
		}

		entries = append(entries, CacheEntryInfo{
			Key:       entry.Key,
			Size:      entry.Size,
			CreatedAt: entry.CreatedAt,
			FilePath:  path,
		})

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to walk cache directory: %w", err)
	}

	if len(entries) == 0 {
		c.logger.Debug("No cache entries found for eviction")
		return nil
	}

	// Sort by creation time (oldest first) - this is our LRU approximation
	// In a more sophisticated implementation, we would track access times
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].CreatedAt.Before(entries[j].CreatedAt)
	})

	// Calculate current total size
	var totalSize int64
	for _, entry := range entries {
		totalSize += entry.Size
	}

	c.logger.Debug("Found %d cache entries totaling %d bytes", len(entries), totalSize)

	// Remove entries until we're under the limit
	var removedCount int
	var removedSize int64
	targetSize := maxSize * 80 / 100 // Remove until we're at 80% of limit to avoid frequent evictions

	for _, entry := range entries {
		if totalSize <= targetSize {
			break
		}

		c.logger.Debug("Evicting cache entry: %s (%d bytes)", entry.Key, entry.Size)

		if err := os.Remove(entry.FilePath); err != nil {
			c.logger.Error("Failed to remove cache file %s: %v", entry.FilePath, err)
			continue
		}

		totalSize -= entry.Size
		removedSize += entry.Size
		removedCount++
	}

	c.logger.Debug("LRU eviction complete: removed %d entries (%d bytes), cache size now %d bytes",
		removedCount, removedSize, totalSize)

	return nil
}
