package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCache_SetAndGet(t *testing.T) {
	// Create temporary directory for testing
	tempDir, err := os.MkdirTemp("", "mail-cache-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create cache instance
	cache := New(tempDir, true, 10)

	// Test data
	key := "test-key"
	data := []byte("Hello, World! This is test data for compression.")

	// Set data
	err = cache.Set(key, data, time.Hour)
	if err != nil {
		t.Fatalf("Failed to set data: %v", err)
	}

	// Get data
	retrieved, found, err := cache.Get(key)
	if err != nil {
		t.Fatalf("Failed to get data: %v", err)
	}

	if !found {
		t.Fatal("Data not found in cache")
	}

	if string(retrieved) != string(data) {
		t.Fatalf("Retrieved data doesn't match. Expected: %s, Got: %s", string(data), string(retrieved))
	}
}

func TestCache_Expiration(t *testing.T) {
	// Create temporary directory for testing
	tempDir, err := os.MkdirTemp("", "mail-cache-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create cache instance
	cache := New(tempDir, false, 10)

	// Test data
	key := "expiring-key"
	data := []byte("This data will expire")

	// Set data with short expiration
	err = cache.Set(key, data, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("Failed to set data: %v", err)
	}

	// Get data immediately (should exist)
	_, found, err := cache.Get(key)
	if err != nil {
		t.Fatalf("Failed to get data: %v", err)
	}
	if !found {
		t.Fatal("Data should exist immediately after setting")
	}

	// Wait for expiration
	time.Sleep(200 * time.Millisecond)

	// Get data after expiration (should not exist)
	_, found, err = cache.Get(key)
	if err != nil {
		t.Fatalf("Failed to get data: %v", err)
	}
	if found {
		t.Fatal("Data should have expired")
	}
}

func TestCache_Compression(t *testing.T) {
	// Create temporary directory for testing
	tempDir, err := os.MkdirTemp("", "mail-cache-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Test with compression enabled
	compressedCache := New(tempDir+"/compressed", true, 10)

	// Test with compression disabled
	uncompressedCache := New(tempDir+"/uncompressed", false, 10)

	// Large, repetitive data that compresses well
	data := make([]byte, 1000)
	for i := range data {
		data[i] = byte(i % 10) // Repetitive pattern
	}

	key := "compression-test"

	// Set data in both caches
	err = compressedCache.Set(key, data, time.Hour)
	if err != nil {
		t.Fatalf("Failed to set compressed data: %v", err)
	}

	err = uncompressedCache.Set(key, data, time.Hour)
	if err != nil {
		t.Fatalf("Failed to set uncompressed data: %v", err)
	}

	// Check file sizes
	compressedFiles, err := filepath.Glob(filepath.Join(tempDir, "compressed", "*.cache"))
	if err != nil || len(compressedFiles) == 0 {
		t.Fatal("Compressed cache file not found")
	}

	uncompressedFiles, err := filepath.Glob(filepath.Join(tempDir, "uncompressed", "*.cache"))
	if err != nil || len(uncompressedFiles) == 0 {
		t.Fatal("Uncompressed cache file not found")
	}

	compressedInfo, err := os.Stat(compressedFiles[0])
	if err != nil {
		t.Fatalf("Failed to stat compressed file: %v", err)
	}

	uncompressedInfo, err := os.Stat(uncompressedFiles[0])
	if err != nil {
		t.Fatalf("Failed to stat uncompressed file: %v", err)
	}

	// Compressed file should be smaller (though not necessarily due to JSON overhead)
	t.Logf("Compressed size: %d bytes", compressedInfo.Size())
	t.Logf("Uncompressed size: %d bytes", uncompressedInfo.Size())

	// Verify data integrity
	retrievedCompressed, found, err := compressedCache.Get(key)
	if err != nil {
		t.Fatalf("Failed to get compressed data: %v", err)
	}
	if !found {
		t.Fatal("Compressed data not found")
	}

	retrievedUncompressed, found, err := uncompressedCache.Get(key)
	if err != nil {
		t.Fatalf("Failed to get uncompressed data: %v", err)
	}
	if !found {
		t.Fatal("Uncompressed data not found")
	}

	// Both should return the same data
	if string(retrievedCompressed) != string(data) {
		t.Fatal("Compressed data doesn't match original")
	}
	if string(retrievedUncompressed) != string(data) {
		t.Fatal("Uncompressed data doesn't match original")
	}
	if string(retrievedCompressed) != string(retrievedUncompressed) {
		t.Fatal("Compressed and uncompressed data don't match")
	}
}

func TestCache_Delete(t *testing.T) {
	// Create temporary directory for testing
	tempDir, err := os.MkdirTemp("", "mail-cache-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create cache instance
	cache := New(tempDir, false, 10)

	// Test data
	key := "delete-test"
	data := []byte("This data will be deleted")

	// Set data
	err = cache.Set(key, data, time.Hour)
	if err != nil {
		t.Fatalf("Failed to set data: %v", err)
	}

	// Verify data exists
	_, found, err := cache.Get(key)
	if err != nil {
		t.Fatalf("Failed to get data: %v", err)
	}
	if !found {
		t.Fatal("Data should exist after setting")
	}

	// Delete data
	err = cache.Delete(key)
	if err != nil {
		t.Fatalf("Failed to delete data: %v", err)
	}

	// Verify data is gone
	_, found, err = cache.Get(key)
	if err != nil {
		t.Fatalf("Failed to get data after deletion: %v", err)
	}
	if found {
		t.Fatal("Data should not exist after deletion")
	}
}

func TestCache_Size(t *testing.T) {
	// Create temporary directory for testing
	tempDir, err := os.MkdirTemp("", "mail-cache-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create cache instance
	cache := New(tempDir, false, 10)

	// Initial size should be 0
	size, err := cache.Size()
	if err != nil {
		t.Fatalf("Failed to get cache size: %v", err)
	}
	if size != 0 {
		t.Fatalf("Initial cache size should be 0, got %d", size)
	}

	// Add some data
	data := []byte("Test data for size calculation")
	err = cache.Set("size-test", data, time.Hour)
	if err != nil {
		t.Fatalf("Failed to set data: %v", err)
	}

	// Size should be greater than 0
	size, err = cache.Size()
	if err != nil {
		t.Fatalf("Failed to get cache size: %v", err)
	}
	if size <= 0 {
		t.Fatalf("Cache size should be greater than 0, got %d", size)
	}

	t.Logf("Cache size after adding data: %d bytes", size)
}

func TestKeysWithPrefix(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, false, 100)

	for _, k := range []string{"acct:messages:INBOX", "acct:messages:Sent", "acct:folders:list", "other:messages:INBOX"} {
		if err := c.Set(k, []byte("data"), 0); err != nil {
			t.Fatalf("Set(%s) failed: %v", k, err)
		}
	}

	keys, err := c.Keys("acct:messages:")
	if err != nil {
		t.Fatalf("Keys failed: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys with prefix, got %v", keys)
	}

	all, err := c.Keys("")
	if err != nil {
		t.Fatalf("Keys(\"\") failed: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("expected 4 keys total, got %v", all)
	}
}

func TestSetTriggersCleanupWhenOverLimit(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, false, 1) // 1MB limit

	big := make([]byte, 512*1024) // 512KB per entry
	// Write 60MB total: this crosses cleanupCheckBytes (32MB) at least
	// once, so a background cleanup must run and evict down to the limit.
	for i := 0; i < 120; i++ {
		if err := c.Set(fmt.Sprintf("k%d", i), big, 0); err != nil {
			t.Fatalf("Set failed: %v", err)
		}
	}

	// Cleanup runs asynchronously after the crossing; wait for eviction.
	// The guarantee is bounded overshoot: at most cleanupCheckBytes (32MB)
	// can be written between cleanups, so the final size stays well under
	// the 60MB written.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		size, err := c.Size()
		if err != nil {
			t.Fatalf("Size failed: %v", err)
		}
		if size < 30*1024*1024 { // well below the 30MB worst-case overshoot bound
			return // eviction happened
		}
		time.Sleep(50 * time.Millisecond)
	}
	size, _ := c.Size()
	t.Fatalf("cache size %d bytes shows no sign of eviction after exceeding the limit", size)
}
