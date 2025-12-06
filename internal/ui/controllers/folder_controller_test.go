package controllers

import (
	"testing"

	"github.com/wltechblog/gommail/internal/email"
)

func TestNewFolderController(t *testing.T) {
	fc := NewFolderController(nil)

	if fc == nil {
		t.Fatal("NewFolderController returned nil")
	}

	if fc.folders == nil {
		t.Error("folders not initialized")
	}

	if fc.currentFolder != "" {
		t.Error("currentFolder should be empty initially")
	}

	if fc.folderLoading {
		t.Error("folderLoading should be false initially")
	}
}

func TestSetCallbacks(t *testing.T) {
	fc := NewFolderController(nil)

	fc.SetCallbacks(
		func(folder string) {},
		func() {},
		func(folderName string, count int) {},
	)

	// Test callbacks are set
	if fc.onFolderSelected == nil {
		t.Error("onFolderSelected callback not set")
	}

	if fc.onFoldersChanged == nil {
		t.Error("onFoldersChanged callback not set")
	}

	if fc.onFolderCountUpdated == nil {
		t.Error("onFolderCountUpdated callback not set")
	}
}

func TestSelectFolder(t *testing.T) {
	fc := NewFolderController(nil)

	// Set up test folders
	folders := []email.Folder{
		{Name: "INBOX", MessageCount: 10},
		{Name: "Sent", MessageCount: 5},
		{Name: "Drafts", MessageCount: 2},
	}
	fc.SetFolders(folders)

	// Track callback
	var selectedFolder string
	fc.SetCallbacks(
		func(folder string) { selectedFolder = folder },
		nil,
		nil,
	)

	// Test selecting a valid folder
	fc.SelectFolder("INBOX")

	if fc.GetCurrentFolder() != "INBOX" {
		t.Errorf("Expected current folder to be INBOX, got %s", fc.GetCurrentFolder())
	}

	if selectedFolder != "INBOX" {
		t.Error("Callback should have been called with INBOX")
	}

	// Test selecting another folder
	fc.SelectFolder("Sent")

	if fc.GetCurrentFolder() != "Sent" {
		t.Errorf("Expected current folder to be Sent, got %s", fc.GetCurrentFolder())
	}

	// Test selecting non-existent folder
	fc.SelectFolder("NonExistent")

	// Should still be "Sent" since NonExistent doesn't exist
	if fc.GetCurrentFolder() != "Sent" {
		t.Error("Current folder should not change when selecting non-existent folder")
	}

	// Test selecting empty folder name (should clear the current folder)
	fc.SelectFolder("")

	if fc.GetCurrentFolder() != "" {
		t.Errorf("Current folder should be cleared when selecting empty folder name, got %s", fc.GetCurrentFolder())
	}
}

func TestGetSetFolders(t *testing.T) {
	fc := NewFolderController(nil)

	folders := []email.Folder{
		{Name: "INBOX", MessageCount: 10},
		{Name: "Sent", MessageCount: 5},
	}

	// Track callback
	foldersChangedCalled := false
	fc.SetCallbacks(
		nil,
		func() { foldersChangedCalled = true },
		nil,
	)

	fc.SetFolders(folders)

	retrievedFolders := fc.GetFolders()

	if len(retrievedFolders) != 2 {
		t.Errorf("Expected 2 folders, got %d", len(retrievedFolders))
	}

	if retrievedFolders[0].Name != "INBOX" {
		t.Errorf("Expected first folder to be INBOX, got %s", retrievedFolders[0].Name)
	}

	if !foldersChangedCalled {
		t.Error("onFoldersChanged callback should have been called")
	}
}

func TestSortFolders(t *testing.T) {
	fc := NewFolderController(nil)

	// Test with unsorted folders
	folders := []email.Folder{
		{Name: "Sent", MessageCount: 5},
		{Name: "Custom", MessageCount: 3},
		{Name: "INBOX", MessageCount: 10},
		{Name: "Drafts", MessageCount: 2},
		{Name: "Archive", MessageCount: 100},
	}

	sorted := fc.SortFolders(folders)

	// INBOX should be first
	if sorted[0].Name != "INBOX" {
		t.Errorf("Expected INBOX to be first, got %s", sorted[0].Name)
	}

	// Common folders should come next in order
	expectedOrder := []string{"INBOX", "Drafts", "Sent", "Archive", "Custom"}
	for i, expected := range expectedOrder {
		if sorted[i].Name != expected {
			t.Errorf("Expected folder at position %d to be %s, got %s", i, expected, sorted[i].Name)
		}
	}

	// Test with empty list
	emptySorted := fc.SortFolders([]email.Folder{})
	if len(emptySorted) != 0 {
		t.Error("Sorting empty folder list should return empty list")
	}
}

func TestFolderLoading(t *testing.T) {
	fc := NewFolderController(nil)

	if fc.IsFolderLoading() {
		t.Error("Folder loading should be false initially")
	}

	fc.SetFolderLoading(true)

	if !fc.IsFolderLoading() {
		t.Error("Folder loading should be true after setting")
	}

	fc.SetFolderLoading(false)

	if fc.IsFolderLoading() {
		t.Error("Folder loading should be false after resetting")
	}
}

func TestGetFolderMessageCount(t *testing.T) {
	fc := NewFolderController(nil)

	folders := []email.Folder{
		{Name: "INBOX", MessageCount: 10},
		{Name: "Sent", MessageCount: 5},
		{Name: "Drafts", MessageCount: 2},
	}
	fc.SetFolders(folders)

	// Test getting count for existing folder
	count := fc.GetFolderMessageCount("INBOX")
	if count != 10 {
		t.Errorf("Expected INBOX count to be 10, got %d", count)
	}

	count = fc.GetFolderMessageCount("Sent")
	if count != 5 {
		t.Errorf("Expected Sent count to be 5, got %d", count)
	}

	// Test getting count for non-existent folder
	count = fc.GetFolderMessageCount("NonExistent")
	if count != 0 {
		t.Errorf("Expected count for non-existent folder to be 0, got %d", count)
	}
}

func TestUpdateFolderMessageCount(t *testing.T) {
	fc := NewFolderController(nil)

	folders := []email.Folder{
		{Name: "INBOX", MessageCount: 10},
		{Name: "Sent", MessageCount: 5},
	}
	fc.SetFolders(folders)

	// Track callback
	var updatedFolder string
	var updatedCount int
	fc.SetCallbacks(
		nil,
		nil,
		func(folderName string, count int) {
			updatedFolder = folderName
			updatedCount = count
		},
	)

	// Update count for existing folder
	fc.UpdateFolderMessageCount("INBOX", 15)

	count := fc.GetFolderMessageCount("INBOX")
	if count != 15 {
		t.Errorf("Expected INBOX count to be 15, got %d", count)
	}

	if updatedFolder != "INBOX" || updatedCount != 15 {
		t.Error("Callback should have been called with correct parameters")
	}

	// Update count for non-existent folder (should not crash)
	fc.UpdateFolderMessageCount("NonExistent", 20)
}

func TestFoldersChanged(t *testing.T) {
	fc := NewFolderController(nil)

	current := []email.Folder{
		{Name: "INBOX", MessageCount: 10},
		{Name: "Sent", MessageCount: 5},
	}

	// Test with identical folders
	updated := []email.Folder{
		{Name: "INBOX", MessageCount: 10},
		{Name: "Sent", MessageCount: 5},
	}

	if fc.FoldersChanged(current, updated) {
		t.Error("Identical folders should not be detected as changed")
	}

	// Test with different message count
	updated = []email.Folder{
		{Name: "INBOX", MessageCount: 15}, // Changed count
		{Name: "Sent", MessageCount: 5},
	}

	if !fc.FoldersChanged(current, updated) {
		t.Error("Different message count should be detected as changed")
	}

	// Test with different number of folders
	updated = []email.Folder{
		{Name: "INBOX", MessageCount: 10},
		{Name: "Sent", MessageCount: 5},
		{Name: "Drafts", MessageCount: 2}, // New folder
	}

	if !fc.FoldersChanged(current, updated) {
		t.Error("Different number of folders should be detected as changed")
	}

	// Test with new folder
	updated = []email.Folder{
		{Name: "INBOX", MessageCount: 10},
		{Name: "Drafts", MessageCount: 2}, // Different folder
	}

	if !fc.FoldersChanged(current, updated) {
		t.Error("New folder should be detected as changed")
	}
}

func TestHandleDeletedFolder(t *testing.T) {
	fc := NewFolderController(nil)

	folders := []email.Folder{
		{Name: "INBOX", MessageCount: 10},
		{Name: "Sent", MessageCount: 5},
		{Name: "Drafts", MessageCount: 2},
	}
	fc.SetFolders(folders)

	// Select a folder
	fc.SelectFolder("Drafts")

	if fc.GetCurrentFolder() != "Drafts" {
		t.Error("Current folder should be Drafts")
	}

	// Track callback
	var selectedFolder string
	fc.SetCallbacks(
		func(folder string) { selectedFolder = folder },
		nil,
		nil,
	)

	// Handle deletion of current folder
	fc.HandleDeletedFolder("Drafts")

	// Should have switched to INBOX
	if fc.GetCurrentFolder() != "INBOX" {
		t.Errorf("Should have switched to INBOX, got %s", fc.GetCurrentFolder())
	}

	if selectedFolder != "INBOX" {
		t.Error("Callback should have been called with INBOX")
	}

	// Handle deletion of non-current folder (should not change)
	fc.SelectFolder("Sent")
	fc.HandleDeletedFolder("INBOX")

	if fc.GetCurrentFolder() != "Sent" {
		t.Error("Current folder should still be Sent")
	}
}

func TestGetFolderNames(t *testing.T) {
	fc := NewFolderController(nil)

	folders := []email.Folder{
		{Name: "INBOX", MessageCount: 10},
		{Name: "Sent", MessageCount: 5},
		{Name: "Drafts", MessageCount: 2},
	}
	fc.SetFolders(folders)
	fc.SelectFolder("INBOX")

	// Get all folder names
	names := fc.GetFolderNames(false)
	if len(names) != 3 {
		t.Errorf("Expected 3 folder names, got %d", len(names))
	}

	// Get folder names excluding current
	names = fc.GetFolderNames(true)
	if len(names) != 2 {
		t.Errorf("Expected 2 folder names (excluding current), got %d", len(names))
	}

	// Verify INBOX is not in the list
	for _, name := range names {
		if name == "INBOX" {
			t.Error("INBOX should be excluded from folder names")
		}
	}
}

func TestFindFolderByName(t *testing.T) {
	fc := NewFolderController(nil)

	folders := []email.Folder{
		{Name: "INBOX", MessageCount: 10},
		{Name: "Sent", MessageCount: 5},
	}
	fc.SetFolders(folders)

	// Find existing folder
	folder := fc.FindFolderByName("INBOX")
	if folder == nil {
		t.Error("Should have found INBOX folder")
	}
	if folder.Name != "INBOX" || folder.MessageCount != 10 {
		t.Error("Found folder has incorrect data")
	}

	// Find non-existent folder
	folder = fc.FindFolderByName("NonExistent")
	if folder != nil {
		t.Error("Should not have found non-existent folder")
	}
}

func TestAutoSelectInbox(t *testing.T) {
	fc := NewFolderController(nil)

	folders := []email.Folder{
		{Name: "Sent", MessageCount: 5},
		{Name: "INBOX", MessageCount: 10},
		{Name: "Drafts", MessageCount: 2},
	}
	fc.SetFolders(folders)

	// Track callback
	var selectedFolder string
	fc.SetCallbacks(
		func(folder string) { selectedFolder = folder },
		nil,
		nil,
	)

	// Auto-select INBOX
	success := fc.AutoSelectInbox()

	if !success {
		t.Error("AutoSelectInbox should return true when INBOX exists")
	}

	if fc.GetCurrentFolder() != "INBOX" {
		t.Error("Current folder should be INBOX")
	}

	if selectedFolder != "INBOX" {
		t.Error("Callback should have been called with INBOX")
	}
}

func TestAutoSelectFirstFolder(t *testing.T) {
	fc := NewFolderController(nil)

	folders := []email.Folder{
		{Name: "Sent", MessageCount: 5},
		{Name: "Drafts", MessageCount: 2},
	}
	fc.SetFolders(folders)

	// Track callback
	var selectedFolder string
	fc.SetCallbacks(
		func(folder string) { selectedFolder = folder },
		nil,
		nil,
	)

	// Auto-select first folder
	success := fc.AutoSelectFirstFolder()

	if !success {
		t.Error("AutoSelectFirstFolder should return true when folders exist")
	}

	if fc.GetCurrentFolder() != "Sent" {
		t.Error("Current folder should be Sent (first folder)")
	}

	if selectedFolder != "Sent" {
		t.Error("Callback should have been called with Sent")
	}

	// Test with empty folder list
	fc.SetFolders([]email.Folder{})
	success = fc.AutoSelectFirstFolder()

	if success {
		t.Error("AutoSelectFirstFolder should return false when no folders exist")
	}
}
